// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildkiteRubyRepository is an RSpec repository whose only CI is the
// Buildkite pipeline; the test runs in it with Buildkite selected.
func buildkiteRubyRepository(t *testing.T, pipeline string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\ngem 'rake'\n")
	writeFile(t, root, buildkiteFile, pipeline)
	t.Chdir(root)
	useCIProvider(t, "buildkite")
	return root
}

// buildkiteRubyStep is an RSpec step in image, with commands before and
// after the setup snippet.
func buildkiteRubyStep(image, env, before, after string) string {
	return "steps:\n  - label: rspec\n" + env + "    commands:\n      - bundle install\n" + before + buildkiteSnippetText("ruby", "1.42.0", "datadoghq.com", "      ") + after + "      - bundle exec rspec\n    plugins:\n      - docker#v5.12.0:\n          image: " + image + "\n          environment:\n            - DD_API_KEY\n"
}

// The shared completeness check reads the snippet on Buildkite: the
// language, the pinned installer, an exact tracer release and the selected
// site, before every test command, on a Ruby the image selects.
func TestBuildkiteRubySetupCompleteness(t *testing.T) {
	complete := buildkiteRubyStep("ruby:3.3", "", "", "")
	snippet := buildkiteSnippetText("ruby", "1.42.0", "datadoghq.com", "      ")
	for _, tc := range []struct {
		name, pipeline, site string
		configured           bool
		reason               string
	}{
		{"complete", complete, "", true, ""},
		{"selected site", complete, "datadoghq.com", true, ""},
		{"other site", complete, "datadoghq.eu", false, `set DD_SITE="datadoghq.eu" in it`},
		{"missing", strings.Replace(complete, snippet, "", 1), "", false, "Run the Datadog setup snippet with an exact DD_SET_TRACER_VERSION_RUBY before this test command"},
		{"latest tracer", strings.Replace(complete, `RUBY="1.42.0"`, `RUBY="latest"`, 1), "", false, "is not an exact release"},
		{"unverified installer", strings.Replace(complete, buildkiteInstallerChecksum, strings.Repeat("1", 64), 1), "", false, "without the pinned release and SHA256 ddtest validates"},
		{"agent host", strings.Replace(strings.Replace(complete, "    plugins:\n      - docker#v5.12.0:\n          image: ruby:3.3\n          environment:\n            - DD_API_KEY\n", "", 1), "", "", 1), "", true, "The CI Ruby runtime is unknown: it comes from the Buildkite runner or agent environment, with no image in the configuration, and the configuration selects no Ruby version. Ask the user which Ruby version it runs, then rerun with --ci-runtime"},
		{"replaced preload", buildkiteRubyStep("ruby:3.3", "", "", "      - export RUBYOPT=-W0\n"), "", true, "replaces the RUBYOPT the Datadog setup snippet exports"},
		{"extended preload", buildkiteRubyStep("ruby:3.3", "", "", "      - export RUBYOPT=\"$$RUBYOPT -W0\"\n"), "", true, ""},
		{"step options before the snippet", strings.Replace(buildkiteRubyStep("ruby:3.3", "    env:\n      RUBYOPT: -W0\n", "", ""), "          environment:\n", "          propagate-environment: true\n          environment:\n", 1), "", true, "dropping the job's -W0; restore it after the Datadog setup snippet with export RUBYOPT=\"$$RUBYOPT -W0\""},
		// The docker plugin passes the container only what it lists.
		{"step options the container never receives", buildkiteRubyStep("ruby:3.3", "    env:\n      RUBYOPT: -W0\n", "", ""), "", true, ""},
		{"Ruby 3.0 RubyGems", buildkiteRubyStep("ruby:3.0", "", "", ""), "", true, "Run gem update --system 3.4.22 before it"},
		{"Ruby 3.0 updated RubyGems", buildkiteRubyStep("ruby:3.0", "", "      - gem update --system 3.4.22\n", ""), "", true, ""},
		{"JRuby step excluded", complete + "  - label: jruby\n    command: bundle exec rspec\n    plugins:\n      - docker#v5.12.0:\n          image: jruby:9.4\n", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRubyRepository(t, tc.pipeline)
			var reasons []string
			for _, job := range RubyCIConfigurationForSite(root, "rspec", tc.site).Jobs {
				reasons = append(reasons, job.Status+": "+job.Reason)
			}
			require.Equal(t, tc.configured, rubyDiscovery{}.ciSetupConfigured(root, "rspec", tc.site), strings.Join(reasons, "\n"))
			if tc.reason != "" {
				require.Contains(t, strings.Join(reasons, "\n"), "inconclusive: ")
				require.Contains(t, strings.Join(reasons, "\n"), tc.reason)
			} else if tc.configured {
				require.NotContains(t, strings.Join(reasons, "\n"), "inconclusive: ")
			}
		})
	}
}

// Agent onboarding asks for the snippet with the selected site, then hands
// off validation once every test step runs it.
func TestBuildkiteRubyAgentOnboarding(t *testing.T) {
	snippet := buildkiteSnippetText("ruby", "1.42.0", "datadoghq.com", "      ")
	root := buildkiteRubyRepository(t, strings.Replace(buildkiteRubyStep("ruby:3.3", "", "", ""), snippet, "", 1))
	state, err := rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Reason, "CI does not yet run the Datadog setup snippet")
	require.Contains(t, state.Instruction, "# Enable Datadog Test Optimization for rspec on Buildkite")
	require.Contains(t, state.Instruction, `DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES="ruby" DD_SET_TRACER_VERSION_RUBY="<exact datadog-ci release>" bash .datadog/install_test_visibility.sh`)
	require.Contains(t, state.Instruction, `export DD_SITE="datadoghq.eu"`)
	require.Contains(t, state.Instruction, "add DD_API_KEY to the plugin's environment list")
	require.Contains(t, state.Instruction, "do not edit the Gemfile")
	require.NotContains(t, state.Instruction, "test-visibility-github-action")
	require.NotContains(t, state.Instruction, "GitLab")

	writeFile(t, root, buildkiteFile, strings.Replace(buildkiteRubyStep("ruby:3.3", "", "", ""), `DD_SITE="datadoghq.com"`, `DD_SITE="datadoghq.eu"`, 1))
	state, err = rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.NoError(t, err)
	require.Equal(t, "ready", state.Status)
	require.Contains(t, state.Reason, "CI already runs the Datadog setup snippet")
}

// Options that must follow the snippet are later commands of the same step,
// with every $ escaped for pipeline upload.
func TestBuildkiteMinitestInstructionsExtendTheExportedOptions(t *testing.T) {
	text := providerSetupInstructions(t.TempDir(), buildkite{}, "ruby", "minitest", "", true)
	require.Contains(t, text, "Right after the Datadog setup snippet, append the initializer to the exported options:")
	require.Contains(t, text, `- export RUBYOPT="$$RUBYOPT -r./ci/datadog_test_optimization.rb"`)
	require.NotContains(t, text, "${{ env.RUBYOPT }}")
}

// The committed fixture is buildkite/rspec-junit-example's pipeline: a
// Ruby matrix in the docker plugin and a template uploading the pipeline.
// Its Ruby 3.0 combination needs a RubyGems update, which a command guarded
// on the image updates per combination.
func TestBuildkiteRubyFixture(t *testing.T) {
	root := copyFixture(t, "buildkite-ruby")
	useCIProvider(t, "buildkite")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "rspec --format progress", scope.Commands[0].Command)
	require.Len(t, scope.Commands[0].locations, 2)
	require.Contains(t, strings.Join(scope.Review, "\n"), "Buildkite uploads .buildkite/pipeline.yml with buildkite-agent pipeline upload")
	request := rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", buildkite{}.setupTerms())
	notes := strings.Join(request.Notes, "\n")
	guard := `- if [ "ruby:{{matrix}}" = "ruby:3.0" ]; then gem update --system 3.4.22; fi`
	require.Contains(t, notes, "These CI jobs run ruby:3.0")
	require.Contains(t, notes, guard)
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))

	// The agent's edit: the guard and the snippet before the tests, and the
	// key passed into the container.
	data := readFixtureFile(t, root, buildkiteFile)
	edited := strings.Replace(data, `      - "rspec --format progress"`+"\n", "      "+guard+"\n"+buildkiteSnippetText("ruby", "1.42.0", "datadoghq.com", "      ")+`      - "rspec --format progress"`+"\n", 1)
	edited = strings.Replace(edited, "          workdir: /app\n", "          workdir: /app\n          environment:\n            - DD_API_KEY\n", 1)
	writeFile(t, root, buildkiteFile, edited)
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
	for _, job := range RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs {
		require.Equal(t, "not checked", job.Status, job.Job+": "+job.Reason)
	}
}

// freshshell/fresh's rspec step runs in a Compose service built from a
// Dockerfile FROM ruby:3.4.7-slim-trixie: the Dockerfile selects the Ruby
// the setup is checked against, and its slim base has no curl.
func TestBuildkiteRubyImageBuiltFromADockerfile(t *testing.T) {
	snippet := buildkiteSnippetText("ruby", "1.42.0", "datadoghq.com", "      ")
	pipeline := "steps:\n  - label: rspec\n    command:\n" + snippet + "      - rspec --format doc\n    plugins:\n      - docker-compose#v4.9.0:\n          run: app\n          environment:\n            - DD_API_KEY\n"
	for _, tc := range []struct{ name, dockerfile, status, runtime, reason string }{
		{"a slim base without curl", freshshellDockerfile, "inconclusive", "3.4.7", "whose final stage is FROM ruby:3.4.7-slim-trixie, which has neither curl nor wget, and no RUN instruction of the Dockerfile installs them"},
		{"a slim base installing curl", strings.Replace(freshshellDockerfile, "perl git build-essential", "perl git build-essential curl", 1), "not checked", "3.4.7", ""},
		{"a base ddtest cannot resolve", "ARG RUBY\nFROM ruby:${RUBY}-slim\n", "inconclusive", "", "The Datadog setup snippet runs before this test command, but this job's Ruby version is unknown: its Docker Compose service app is built from Dockerfile, whose base image ruby:${RUBY}-slim reads the build argument RUBY, which has no literal default ddtest can resolve."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRubyRepository(t, pipeline)
			writeFile(t, root, "docker-compose.yml", "version: '2'\nservices:\n  app:\n    build: .\n")
			writeFile(t, root, "Dockerfile", tc.dockerfile)
			jobs := RubyCIConfigurationForSite(root, "rspec", "").Jobs
			require.Len(t, jobs, 1)
			require.Equal(t, tc.status, jobs[0].Status, jobs[0].Reason)
			require.Equal(t, tc.runtime, jobs[0].Runtime)
			require.Contains(t, jobs[0].Reason, tc.reason)
			require.NotContains(t, jobs[0].Reason, "in a job whose image selects a static Ruby version")
			require.Equal(t, tc.reason == "", rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
		})
	}
	t.Run("the instructions name the missing curl before the snippet is written", func(t *testing.T) {
		root := buildkiteRubyRepository(t, strings.Replace(pipeline, snippet, "", 1))
		writeFile(t, root, "docker-compose.yml", "version: '2'\nservices:\n  app:\n    build: .\n")
		writeFile(t, root, "Dockerfile", freshshellDockerfile)
		notes := strings.Join(rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", buildkite{}.setupTerms()).Notes, "\n")
		require.Contains(t, notes, ".buildkite/pipeline.yml / rspec: The step's image is built from Dockerfile, whose final stage is FROM ruby:3.4.7-slim-trixie, which has neither curl nor wget")
		jobs := RubyCIConfigurationForSite(root, "rspec", "").Jobs
		require.Len(t, jobs, 1)
		require.Equal(t, "3.4.7", jobs[0].Runtime)
	})
}
