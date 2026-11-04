// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// indented prefixes every line of text with indent.
func indented(text, indent string) string {
	return indent + strings.ReplaceAll(text, "\n", "\n"+indent)
}

// A GitLab job whose image cannot run the setup script is complete with the
// setup without Bash or curl, in its own script entry, before the tests.
func TestGitLabFallbackSetupIsComplete(t *testing.T) {
	useCIProvider(t, "gitlab")
	entry := indented("- |\n"+indented(fallbackText("ruby", "1.42.0", gitLabSnippetForm), "  "), "    ") + "\n"
	for _, image := range []string{"ruby:3.3-slim", "ruby:3.3-alpine", "ruby:3.3"} {
		root := gitLabRubyRepository(t, "rspec:\n  image: "+image+"\n  script:\n    - bundle install\n"+entry+"    - bundle exec rspec\n")
		require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""), image)
		jobs, _ := gitLabJobs(t, root)
		require.Equal(t, rubyInstallerPreload, jobs["rspec"].Steps[2].Env["RUBYOPT"], image)
	}
	// Without the comment it is not complete, and says why.
	root := gitLabRubyRepository(t, "rspec:\n  image: ruby:3.3-slim\n  script:\n"+strings.Replace(entry, "      # Datadog Test Optimization: this image has no bash/curl, so Datadog's official installer can't run here.\n", "", 1)+"    - bundle exec rspec\n")
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	jobs, _ := gitLabJobs(t, root)
	require.Contains(t, jobs["rspec"].Steps[0].Setup.Problem, "lacks the comment")
	// The image's job takes it in the instructions, beside the setup script
	// of the others.
	root = gitLabRubyRepository(t, "slim:\n  image: ruby:3.3-slim\n  script:\n    - bundle exec rspec\nfull:\n  image: ruby:3.3\n  script:\n    - bundle exec rspec\n")
	text := providerSetupInstructions(root, gitLabCI{}, "ruby", "rspec", "", true)
	require.Contains(t, text, "Datadog's GitLab setup script")
	require.Contains(t, text, "These jobs (.gitlab-ci.yml / slim) run in ruby:3.3-slim, which has neither curl nor wget, so Datadog's official installer cannot run there")
	require.Contains(t, text, "  "+fallbackComment[0]+"\n  "+fallbackComment[1]+"\n  if [ -n \"${DD_API_KEY:-}\" ]; then")
	require.Contains(t, text, `DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="gitlab"`)
	require.Contains(t, text, "tell the user that Datadog's official installation script cannot be used in these jobs because their image lacks Bash or curl, and that it's recommended that they add Bash and curl to the image themselves")
	require.Contains(t, text, "Do not change their image or add Bash or curl to it yourself")
}

// A Buildkite step whose image cannot run the installer is complete with the
// setup without Bash or curl, written with $$ in the pipeline.
func TestBuildkiteFallbackSetupIsComplete(t *testing.T) {
	setup := buildkiteEntryText(fallbackText("ruby", "1.42.0", buildkiteSnippetForm), "      ")
	step := func(image, setup string) string {
		return "steps:\n  - label: rspec\n    commands:\n      - bundle install\n" + setup + "      - bundle exec rspec\n    plugins:\n      - docker#v5.12.0:\n          image: " + image + "\n          environment:\n            - DD_API_KEY\n"
	}
	for _, image := range []string{"ruby:3.3-alpine", "ruby:3.3-slim"} {
		root := buildkiteRubyRepository(t, step(image, setup))
		require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""), image)
		jobs := RubyCIConfigurationForSite(root, "rspec", "").Jobs
		require.Len(t, jobs, 1)
		require.Equal(t, fallbackSetupName, jobs[0].Action)
	}
	// An unescaped $ is interpolated at pipeline upload.
	root := buildkiteRubyRepository(t, step("ruby:3.3-alpine", strings.ReplaceAll(setup, "$$PWD", "$PWD")))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	// Without the key in the container, it always skips.
	root = buildkiteRubyRepository(t, strings.Replace(step("ruby:3.3-alpine", setup), "          environment:\n            - DD_API_KEY\n", "", 1))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	text := providerSetupInstructions(buildkiteRubyRepository(t, step("ruby:3.3-alpine", "")), buildkite{}, "ruby", "rspec", "", true)
	require.Contains(t, text, "These jobs (.buildkite/pipeline.yml / rspec) run in ruby:3.3-alpine, which has no Bash, so Datadog's official installer cannot run there")
	require.Contains(t, text, `export DD_TRACER_FOLDER="$$DD_TRACER_BASE/.datadog" DD_SITE="datadoghq.com" DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="buildkite" DD_TRACER_VERSION_RUBY="<exact datadog-ci release>"`)
	require.NotContains(t, text, buildkiteInstallerChecksum, "no step of this pipeline can run the installer")
}

// A Jenkins container stage whose image cannot run the installer is
// complete with the setup without Bash or curl in the sh step of the tests,
// and onboard --apply writes it.
func TestJenkinsFallbackSetupIsComplete(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		stage := jenkinsContainerStage("docker { image 'ruby:3.3-slim' }", "", strings.ReplaceAll(fallbackText("ruby", "1.42.0", jenkinsSnippetForm), "\n", "\n      ")+"\n      bundle exec rspec")
		root := jenkinsApplyRepository(t, "rspec", stage)
		require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
		jobs := RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs
		require.Len(t, jobs, 1)
		require.Equal(t, fallbackSetupName, jobs[0].Action)
	})
	t.Run("apply", func(t *testing.T) {
		root := jenkinsApplyRepository(t, "rspec", jenkinsContainerStage("docker { image 'ruby:3.3-slim' }", "", "bundle exec rspec"))
		plan, err := planProviderSetup(root, "rspec", "datadoghq.com", "1.42.0")
		require.NoError(t, err)
		written := plannedFile(t, plan, "Jenkinsfile")
		require.Contains(t, written, fallbackComment[0])
		require.Contains(t, written, `DD_TRACER_VERSION_RUBY="1.42.0"`)
		require.NotContains(t, written, buildkiteInstallerChecksum)
		_, err = plan.Apply(true)
		require.NoError(t, err)
		require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
	})
}

// A job whose image lacks curl and installs it only after a command that may
// run its tests gets the setup without Bash or curl: the official installer
// would go before that first test command, where curl is still missing. An
// install before every such command still lets the installer run.
func TestFallbackWhenCurlArrivesAfterTests(t *testing.T) {
	install := "apt-get update && apt-get install -y curl"
	fallback := func(t *testing.T, workflows []ciWorkflow, job string) bool {
		t.Helper()
		require.Len(t, workflows, 1)
		_, ok := fallbackJob(workflows[0].Jobs[job])
		return ok
	}
	for _, tc := range []struct {
		name     string
		commands []string
		fallback bool
	}{
		{"installed first", []string{"apt-get update", "apt-get install -y curl", "bundle exec rspec"}, false},
		{"after directory and variables", []string{"cd .", "export CI_NAME=ci", install, "bundle exec rspec"}, false},
		{"installed after the tests", []string{"bundle exec rspec", install, "bundle exec rspec"}, true},
		{"installed after a command that may run them", []string{"bundle exec rake", install, "bundle exec rspec"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("gitlab", func(t *testing.T) {
				useCIProvider(t, "gitlab")
				config := "rspec:\n  image: ruby:3.3-slim\n  script:\n"
				for _, command := range tc.commands {
					config += "    - " + command + "\n"
				}
				workflows, err := gitLabCI{}.readWorkflows(gitLabRubyRepository(t, config))
				require.NoError(t, err)
				require.Equal(t, tc.fallback, fallback(t, workflows, "rspec"))
			})
			t.Run("circleci", func(t *testing.T) {
				config := "version: 2.1\njobs:\n  rspec:\n    docker:\n      - image: ruby:3.3-slim\n    steps:\n      - checkout\n"
				for _, command := range tc.commands {
					config += "      - run: " + command + "\n"
				}
				workflows, err := circleCI{}.readWorkflows(circleRubyRepository(t, config+circleWorkflow("      - rspec\n")))
				require.NoError(t, err)
				require.Equal(t, tc.fallback, fallback(t, workflows, "main/rspec"))
			})
			t.Run("buildkite", func(t *testing.T) {
				var commands string
				for _, command := range tc.commands {
					commands += "      - " + command + "\n"
				}
				workflows, err := buildkite{}.readWorkflows(buildkiteRubyRepository(t, "steps:\n"+buildkiteDockerStep("rspec", "ruby:3.3-slim", commands)))
				require.NoError(t, err)
				require.Equal(t, tc.fallback, fallback(t, workflows, "rspec"))
			})
		})
	}
}

// An existing Java setup without Bash or curl in an image whose JDK cannot
// run its download (JDK 8 here) is incomplete on every provider, as ddtest
// would refuse to prescribe it there, and says why.
func TestFallbackJavaSetupNeedsJDK11OnEveryProvider(t *testing.T) {
	const image = "openjdk:8-jdk-slim"
	gradleRepository := func(t *testing.T, provider, file, config string) string {
		root := springBootRepository(t, springBootScripts)
		require.NoError(t, os.Remove(filepath.Join(root, circleCIFile)))
		writeFile(t, root, file, config)
		useCIProvider(t, provider)
		return root
	}
	for _, tc := range []struct {
		provider, file string
		config         func(fallback string) string
	}{
		{"gitlab", ".gitlab-ci.yml", func(fallback string) string {
			return "test:\n  image: " + image + "\n  script:\n" + indented("- |\n"+indented(fallback, "  "), "    ") + "\n    - ./gradlew test\n"
		}},
		{"buildkite", buildkiteFile, func(fallback string) string {
			return "steps:\n  - label: test\n    commands:\n" + buildkiteEntryText(fallback, "      ") + "      - ./gradlew test\n    plugins:\n      - docker#v5.12.0:\n          image: " + image + "\n          environment:\n            - DD_API_KEY\n"
		}},
		{"jenkins", "Jenkinsfile", func(fallback string) string {
			return jenkinsContainerStage("docker { image '"+image+"' }", "", strings.ReplaceAll(fallback, "\n", "\n      ")+"\n      ./gradlew test")
		}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			form := map[string]snippetForm{"gitlab": gitLabSnippetForm, "buildkite": buildkiteSnippetForm, "jenkins": jenkinsSnippetForm}[tc.provider]
			root := gradleRepository(t, tc.provider, tc.file, tc.config(fallbackText("java", "1.67.1", form)))
			check := JavaCIConfiguration(root, "cucumber-java", "")
			require.Len(t, check.Jobs, 1, check.Reason)
			require.Equal(t, fallbackSetupName, check.Jobs[0].Action, check.Jobs[0].Reason)
			require.NotEqual(t, "not checked", check.Jobs[0].Status)
			require.Contains(t, check.Jobs[0].Reason, image+" has JDK 8")
			require.Contains(t, check.Jobs[0].Reason, fallbackUserRecommendation)
			require.False(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))
		})
	}
}
