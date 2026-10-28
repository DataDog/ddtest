// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gitLabRubyRepository is an RSpec repository whose .gitlab-ci.yml is
// config; the test runs in it.
func gitLabRubyRepository(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\n")
	writeFile(t, root, ".gitlab-ci.yml", config)
	t.Chdir(root)
	return root
}

func rubyJob(image, before, after string) string {
	return "rspec:\n  image: " + image + "\n  script:\n" + before + gitLabSetupEntry(gitLabRubySetup) + after + "    - bundle exec rspec\n"
}

// On GitLab the shared completeness check reads the setup script: the
// language, an API key from a CI/CD variable, an exact tracer release and the
// selected site, before every test command, on a Ruby the image selects.
func TestGitLabRubySetupCompleteness(t *testing.T) {
	useCIProvider(t, "gitlab")
	for _, tc := range []struct {
		name, config, site string
		configured         bool
		reason             string
	}{
		{"complete", rubyJob("ruby:3.3", "", ""), "", true, ""},
		{"selected site", rubyJob("ruby:3.3", "", ""), "datadoghq.com", true, ""},
		{"other site", rubyJob("ruby:3.3", "", ""), "datadoghq.eu", false, `set SITE="datadoghq.eu"`},
		{"missing", "rspec:\n  image: ruby:3.3\n  script:\n    - bundle exec rspec\n", "", false, "Run the Datadog setup script with an exact RUBY_TRACER_VERSION"},
		{"other language", strings.Replace(rubyJob("ruby:3.3", "", ""), `LANGUAGES="ruby"`, `LANGUAGES="js"`, 1), "", false, ""},
		{"all languages", strings.Replace(rubyJob("ruby:3.3", "", ""), `LANGUAGES="ruby"`, `LANGUAGES="all"`, 1), "", true, ""},
		{"literal key", strings.Replace(rubyJob("ruby:3.3", "", ""), `"$DD_API_KEY"`, `"0123456789abcdef"`, 1), "", false, "not a CI/CD variable reference"},
		{"latest tracer", strings.Replace(rubyJob("ruby:3.3", "", ""), `"1.42.0"`, `"latest"`, 1), "", false, "is not an exact release"},
		{"tampered script", strings.Replace(rubyJob("ruby:3.3", "", ""), "c82030", "000000", 1), "", false, "not a supported, checksum-verified release"},
		{"after the tests", "rspec:\n  image: ruby:3.3\n  script:\n    - bundle exec rspec\n" + gitLabSetupEntry(gitLabRubySetup), "", false, ""},
		// The setup is complete; validation then reports the runtime as
		// inconclusive, as for GitHub Actions.
		{"floating image", rubyJob("ruby:latest", "", ""), "", true, "in a job whose image selects a static Ruby version"},
		{"replaced preload", rubyJob("ruby:3.3", "", "    - export RUBYOPT=-W0\n"), "", true, "replaces the RUBYOPT the Datadog setup script exports"},
		{"extended preload", rubyJob("ruby:3.3", "", "    - export RUBYOPT=\"$RUBYOPT -W0\"\n"), "", true, ""},
		{"Ruby 2.7 RubyGems", rubyJob("ruby:2.7", "", ""), "", true, "Run gem update --system 3.4.22 before it"},
		{"Ruby 2.7 updated RubyGems", rubyJob("ruby:2.7", "    - gem update --system 3.4.22\n", ""), "", true, ""},
		{"JRuby job excluded", rubyJob("ruby:3.3", "", "") + "jruby:\n  image: jruby:9.4\n  script:\n    - bundle exec rspec\n", "", true, ""},
		{"only JRuby", "jruby:\n  image: jruby:9.4\n  script:\n    - bundle exec rspec\n", "", false, ""},
		{"unresolved include", "include:\n  - project: group/ci\n    file: ruby.yml\n" + rubyJob("ruby:3.3", "", ""), "", false, "project include group/ci cannot be read locally"},
		{"matrix", "rspec:\n  image: ruby:${RUBY}\n  parallel:\n    matrix:\n      - RUBY: ['3.2', '3.3']\n        BUNDLE_GEMFILE: [Gemfile, gemfiles/rails.gemfile]\n  script:\n" + gitLabSetupEntry(gitLabRubySetup) + "    - bundle exec rspec\n", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := gitLabRubyRepository(t, tc.config)
			require.Equal(t, tc.configured, rubyDiscovery{}.ciSetupConfigured(root, "rspec", tc.site))
			var reasons []string
			for _, job := range RubyCIConfigurationForSite(root, "rspec", tc.site).Jobs {
				reasons = append(reasons, job.Status+": "+job.Reason)
			}
			if tc.reason != "" {
				require.Contains(t, strings.Join(reasons, "\n"), "inconclusive: ")
				require.Contains(t, strings.Join(reasons, "\n"), tc.reason)
			} else if tc.configured {
				require.NotContains(t, strings.Join(reasons, "\n"), "inconclusive: ")
			}
		})
	}
}

func TestGitLabRubyInventoryReadsTheImage(t *testing.T) {
	root := gitLabRubyRepository(t, rubyJob("ruby:3.3-slim", "    - "+gitLabCurlInstall+"\n", "")+"jruby:\n  image: jruby:9.4\n  script:\n    - bundle exec rspec\n")
	check := RubyCIConfigurationForSite(root, "rspec", "")
	require.Len(t, check.Jobs, 2)
	byJob := map[string]RuntimeFinding{}
	for _, job := range check.Jobs {
		byJob[job.Job] = job
	}
	require.Equal(t, "3.3", byJob["rspec"].Runtime)
	require.Equal(t, "1.42.0", byJob["rspec"].TracerRequested)
	require.Equal(t, "test-optimization-gitlab-script v1.2.10", byJob["rspec"].Action)
	require.Equal(t, "not checked", byJob["rspec"].Status)
	require.Equal(t, "excluded", byJob["jruby"].Status)
	require.Contains(t, byJob["jruby"].Reason, "JRuby (image jruby:9.4), which the tracer cannot instrument")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	excluded := 0
	for _, entry := range scope.Commands {
		if entry.CIInstrumentationExcluded {
			excluded++
		}
	}
	require.Equal(t, 1, excluded)
}

// Agent onboarding asks for the setup script with the selected site, then
// hands off validation once every test job runs it.
func TestGitLabRubyAgentOnboarding(t *testing.T) {
	root := gitLabRubyRepository(t, "rspec:\n  image: ruby:3.3\n  script:\n    - bundle exec rspec\n")
	useCIProvider(t, "gitlab")
	state, err := rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Reason, "CI does not yet run the Datadog setup script")
	require.Contains(t, state.Instruction, "# Enable Datadog Test Optimization for rspec on GitLab CI")
	require.Contains(t, state.Instruction, `LANGUAGES="ruby" SITE="datadoghq.eu" API_KEY="$DD_API_KEY" RUBY_TRACER_VERSION="<exact datadog-ci release>"`)
	require.Contains(t, state.Instruction, "do not edit the Gemfile")
	require.NotContains(t, state.Instruction, "test-visibility-github-action")
	require.Contains(t, state.ResumeCommand, "--ci-provider")

	writeFile(t, root, ".gitlab-ci.yml", strings.Replace(rubyJob("ruby:3.3", "", ""), `SITE="datadoghq.com"`, `SITE="datadoghq.eu"`, 1))
	state, err = rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.NoError(t, err)
	require.Equal(t, "ready", state.Status)
	require.Contains(t, state.Reason, "CI already runs the Datadog setup script")
}

func TestGitLabMinitestInstructionsExtendTheExportedOptions(t *testing.T) {
	text := providerSetupInstructions(t.TempDir(), gitLabCI{}, "ruby", "minitest", "", true)
	require.Contains(t, text, `- export RUBYOPT="$RUBYOPT -r./ci/datadog_test_optimization.rb"`)
	require.Contains(t, text, "Datadog.configure")
	require.NotContains(t, text, "${{ env.RUBYOPT }}")
}

// planGitLab plans the JavaScript setup in a Jest repository whose GitLab
// configuration is config.
func planGitLab(t *testing.T, framework string, files map[string]string) (*SetupPlan, string, error) {
	t.Helper()
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	for path, contents := range files {
		writeFile(t, root, path, contents)
	}
	useCIProvider(t, "gitlab")
	plan, err := PlanJavaScriptSetup(root, framework, "5.133.0", "datadoghq.com", ">=18")
	if err != nil {
		return nil, root, err
	}
	return plan, root, nil
}

func plannedFile(t *testing.T, plan *SetupPlan, path string) string {
	t.Helper()
	for _, edit := range plan.edits {
		if edit.path == path {
			return string(edit.after)
		}
	}
	t.Fatalf("no edit of %s", path)
	return ""
}

func TestGitLabApplyInsertsTheSetupBeforeTheFirstTest(t *testing.T) {
	plan, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "image: node:22\n\ntest:\n  variables:\n    CI: \"true\"\n  script:\n    - npm ci\n    - npm test # unit tests\n    - npm run report\n"})
	require.NoError(t, err)
	require.Equal(t, `image: node:22

test:
  variables:
    CI: "true"
  script:
    - npm ci
    - |
      if [ -n "${DD_API_KEY:-}" ]; then
        LANGUAGES="js" SITE="datadoghq.com" API_KEY="$DD_API_KEY" JS_TRACER_VERSION="5.133.0" \
        SRC=$(curl -fsSL https://github.com/DataDog/test-optimization-gitlab-script/releases/download/v1.2.10/script.sh); \
        echo "$SRC" | sha256sum | grep -q '^c82030bdbc7cbbbb139751885ca0392a7191c858f5a080ae364e0ec16218b783' && \
          source <(echo "$SRC") || \
          echo "ERROR: SHA256 mismatch. Datadog Test Optimization autoinstrumentation not enabled." >&2
      else
        echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job." >&2
      fi
    - npm test # unit tests
    - npm run report
`, plannedFile(t, plan, ".gitlab-ci.yml"))
	require.Contains(t, plannedFile(t, plan, ".gitignore"), "/.testoptimization/")
}

func TestGitLabApplyAfterApplyIsConfigured(t *testing.T) {
	plan, root, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "test:\n  image: node:22\n  script:\n    - npm ci\n    - npm test\n"})
	require.NoError(t, err)
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	again, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured(), "a second apply changes nothing")
	_, err = PlanJavaScriptSetup(root, "jest", "5.140.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Existing Datadog setup differs")
}

func TestGitLabApplyRegistersTheESMLoader(t *testing.T) {
	plan, _, err := planGitLab(t, "vitest", map[string]string{".gitlab-ci.yml": "test:\n  image: node:22\n  script: npx vitest run\n", "package.json": `{"devDependencies": {"vitest": "3.2.0"}}`})
	require.NoError(t, err)
	require.Equal(t, `test:
  image: node:22
  script:
    - |
      if [ -n "${DD_API_KEY:-}" ]; then
        LANGUAGES="js" SITE="datadoghq.com" API_KEY="$DD_API_KEY" JS_TRACER_VERSION="5.133.0" \
        SRC=$(curl -fsSL https://github.com/DataDog/test-optimization-gitlab-script/releases/download/v1.2.10/script.sh); \
        echo "$SRC" | sha256sum | grep -q '^c82030bdbc7cbbbb139751885ca0392a7191c858f5a080ae364e0ec16218b783' && \
          source <(echo "$SRC") || \
          echo "ERROR: SHA256 mismatch. Datadog Test Optimization autoinstrumentation not enabled." >&2
        if [ -n "${DD_TRACE_ESM_IMPORT:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_ESM_IMPORT"; fi
      else
        echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job." >&2
      fi
    - npx vitest run
`, plannedFile(t, plan, ".gitlab-ci.yml"))
}

func TestGitLabApplySharedSources(t *testing.T) {
	template := ".test:\n  image: node:22\n  script:\n    - npm test\n"
	t.Run("test jobs sharing a template", func(t *testing.T) {
		plan, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "include: ci/templates.yml\nunit:\n  extends: .test\nmore:\n  extends: .test\n  variables:\n    SUITE: more\n", "ci/templates.yml": template})
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(plannedFile(t, plan, "ci/templates.yml"), "sha256sum"))
		require.Contains(t, plan.LocalImpact(), "ci/templates.yml is included by .gitlab-ci.yml; any other project that includes it (include:project) receives the Datadog setup too.")
	})
	t.Run("a job that runs no tests shares it", func(t *testing.T) {
		_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": template + "unit:\n  extends: .test\nother:\n  extends: .test\n  rules:\n    - when: never\nlint:\n  script: !reference [.test, script]\n  variables:\n    NODE_OPTIONS: --check\n"})
		require.NoError(t, err, "a disabled job and a job running the same tests agree")
	})
	t.Run("a YAML anchor shared with a non-test job", func(t *testing.T) {
		_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:22\n  script: &steps\n    - npm ci\n    - npm test\nbuild:\n  image: node:22\n  script: *steps\n  after_script: [echo]\n"})
		require.NoError(t, err)
	})
	t.Run("matrix entries", func(t *testing.T) {
		plan, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:${NODE}\n  parallel:\n    matrix:\n      - NODE: ['20', '22']\n  script:\n    - npm test\n"})
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(plannedFile(t, plan, ".gitlab-ci.yml"), "sha256sum"))
	})
}

func TestGitLabApplyRefusesUnsafeEdits(t *testing.T) {
	for _, tc := range []struct{ name, config, reason string }{
		{"shared with a job that runs no tests", ".tests:\n  image: node:22\n  script:\n    - npm test\nunit:\n  extends: .tests\ndocs:\n  extends: .tests\n  before_script:\n    - cd docs\n", "Job docs also uses this script entry"},
		{"flow list", "unit:\n  image: node:22\n  script: [npm ci, npm test]\n", "not a block list"},
		{"multiline single script", "unit:\n  image: node:22\n  script: |\n    npm ci\n    npm test\n", ""},
		{"unsupported Node", "unit:\n  image: node:16\n  script:\n    - npm test\nmodern:\n  image: node:14\n  script:\n    - npm test\n", "No literal jest CI step found"},
		{"other setup", "unit:\n  image: node:22\n  script:\n" + strings.Replace(gitLabSetupEntry(gitLabJestSetup), "c82030", "000000", 1) + "    - npm test\n", "Existing Datadog setup differs"},
		{"setup after the tests", "unit:\n  image: node:22\n  script:\n    - npm test\n" + gitLabSetupEntry(gitLabJestSetup), "runs after the first jest test command"},
		{"custom instrumentation", "unit:\n  image: node:22\n  variables:\n    DD_TRACE_ENABLED: 'false'\n  script:\n    - npm test\n", "Custom inherited instrumentation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": tc.config, "docs/package.json": `{"scripts": {"test": "echo no tests"}}`})
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			if tc.reason != "" {
				require.Contains(t, gap.Reason, tc.reason)
			}
		})
	}
	_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:22\n  script:\n    - npm test\n"})
	require.NoError(t, err)
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	writeFile(t, root, ".gitlab-ci.yml", "unit:\n  image: node:20\n  script:\n    - npm test\n")
	useCIProvider(t, "gitlab")
	_, err = PlanJavaScriptSetup(root, "jest", "6.22.0", "datadoghq.com", ">=22")
	var runtime *NodeRuntimeGap
	require.ErrorAs(t, err, &runtime)
	_, err = PlanJavaScriptSetup(root, "cucumber", "6.22.0", "datadoghq.com", "")
	var gap *SetupGap
	require.True(t, errors.As(err, &gap) && strings.Contains(gap.Reason, "package scripts"), err)
}

// The Node runtime check reads the image's Node and the setup script's
// tracer; the preload reaches the tests unless a later command replaces it.
func TestGitLabJavaScriptRuntimeCheck(t *testing.T) {
	for _, tc := range []struct{ name, framework, script, status string }{
		{"instrumented", "jest", gitLabSetupEntry(gitLabJestSetup) + "    - npx jest\n", StatusCompatible},
		{"missing setup", "jest", "    - npx jest\n", StatusIncompatible},
		{"replaced options", "jest", gitLabSetupEntry(gitLabJestSetup) + "    - export NODE_OPTIONS=--trace-warnings\n    - npx jest\n", StatusInconclusive},
		{"extended options", "jest", gitLabSetupEntry(gitLabJestSetup) + "    - export NODE_OPTIONS=\"$NODE_OPTIONS --trace-warnings\"\n    - npx jest\n", StatusCompatible},
		{"Vitest without the loader", "vitest", gitLabSetupEntry(gitLabJestSetup) + "    - npx vitest run\n", StatusInconclusive},
		{"Vitest with the loader", "vitest", gitLabSetupEntry(gitLabJestSetup) + "    - " + esmImportCommand + "\n    - npx vitest run\n", StatusCompatible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, jestWorkflow)
			writeFile(t, root, "package.json", `{"devDependencies": {"jest": "30.0.0", "vitest": "3.2.0"}}`)
			writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n"+tc.script)
			useCIProvider(t, "gitlab")
			result := checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
				require.Equal(t, "test-optimization-gitlab-script v1.2.10", action)
				require.Equal(t, "5.133.0", version)
				return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
			}, tc.framework)
			require.Equal(t, tc.status, result.Status, result)
			require.Equal(t, "22", result.Jobs[0].Node)
			require.Equal(t, "image node:22", result.Jobs[0].NodeSource)
		})
	}
}

// copyFixture copies a committed GitLab fixture into a temporary repository.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("testdata", name)
	require.NoError(t, filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeFile(t, root, relative, string(data))
		return nil
	}))
	return root
}

func TestGitLabJavaScriptFixture(t *testing.T) {
	root := copyFixture(t, "gitlab-javascript")
	useCIProvider(t, "")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "gitlab", selected)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	var locations []string
	for _, entry := range scope.Commands {
		for _, location := range entry.locations {
			locations = append(locations, location.String())
		}
		require.NotContains(t, entry.Environment, "npm_config_cache", "a cache location is runner state")
	}
	require.ElementsMatch(t, []string{".gitlab-ci.yml / unit: [20] / step 2 (npm test -- --ci)", ".gitlab-ci.yml / unit: [22] / step 2 (npm test -- --ci)"}, locations, "the disabled legacy job is outside the scope")
	_, err = PlanJavaScriptSetup(root, "jest", "6.22.0", "datadoghq.com", ">=22")
	var runtime *NodeRuntimeGap
	require.ErrorAs(t, err, &runtime, "the Node 20 entry needs an older tracer")
	plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	edited := plannedFile(t, plan, ".gitlab-ci.yml")
	require.Equal(t, 1, strings.Count(edited, "sha256sum"))
	require.Less(t, strings.Index(edited, "sha256sum"), strings.Index(edited, "npm test -- --ci"))
	require.Greater(t, strings.Index(edited, "sha256sum"), strings.Index(edited, "unit:"))
}

func TestGitLabRubyFixture(t *testing.T) {
	root := copyFixture(t, "gitlab-ruby")
	t.Chdir(root)
	useCIProvider(t, "")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 3, "the Ruby versions share a configuration per Gemfile; JRuby is its own")
	gemfiles := map[string]bool{}
	for _, entry := range scope.Commands {
		if !entry.CIInstrumentationExcluded {
			gemfiles[entry.Environment["BUNDLE_GEMFILE"]] = true
		}
	}
	require.Equal(t, map[string]bool{"Gemfile": true, "gemfiles/rake13.gemfile": true}, gemfiles)
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	config, err := os.ReadFile(".gitlab-ci.yml")
	require.NoError(t, err)
	writeFile(t, root, ".gitlab-ci.yml", strings.Replace(string(config), "  script:\n    - bundle exec rspec --format progress", "  script:\n"+gitLabSetupEntry(gitLabRubySetup)+"    - bundle exec rspec --format progress", 1))
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""), "the JRuby job stays excluded")
}

// Scoped JavaScript initialization is set up by hand on GitLab: onboarding
// asks for the setup script, then hands off validation once it is complete.
func TestGitLabCucumberSetupIsManual(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.0"}}`)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n    - npm test\n")
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	state, err := rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code, state.Reason)
	require.Contains(t, state.Instruction, `LANGUAGES="js"`)
	require.Contains(t, state.Instruction, "DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false")
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n"+gitLabSetupEntry(gitLabJestSetup)+"    - "+esmImportCommand+"\n    - npm test\n")
	state, err = rubyAgentStart(t, StartOptions{Format: "json"})
	require.NoError(t, err, state.Reason)
	require.Equal(t, "ready", state.Status)
	require.Equal(t, []string{"testdrive", "--framework", "cucumber", "--all", "--agent", "--yes"}, state.NextCommand[1:7])
}

// gitLabGuardedEntry is the setup entry ddtest writes: the README's entry
// skipped, with a log line, when guard is empty.
func gitLabGuardedEntry(variables, guard string) string {
	plain := strings.Split(strings.TrimSuffix(gitLabSetupEntry(variables), "\n"), "\n")
	lines := []string{"    - |", `      if [ -n "$` + guard + `" ]; then`}
	for _, line := range plain[1:] {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "      else", `        echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job." >&2`, "      fi")
	return strings.Join(lines, "\n") + "\n"
}

// The guarded entry is a complete setup, like the README's plain entry; a
// guard on another variable, or one that sources elsewhere, is not.
func TestGitLabKeyGuardedSetup(t *testing.T) {
	useCIProvider(t, "gitlab")
	for _, tc := range []struct {
		name, entry string
		configured  bool
	}{
		{"guarded", gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY"), true},
		{"plain", gitLabSetupEntry(gitLabRubySetup), true},
		{"guard on another variable", gitLabGuardedEntry(gitLabRubySetup, "CI_COMMIT_TAG"), false},
		{"other condition", strings.Replace(gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY"), `[ -n "$DD_API_KEY" ]`, `[ "$CI_COMMIT_BRANCH" = main ]`, 1), false},
		{"else sources too", strings.Replace(gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY"), `echo "DD_API_KEY is not set`, `source ./other.sh; echo "DD_API_KEY is not set`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := gitLabRubyRepository(t, "rspec:\n  image: ruby:3.3\n  script:\n"+tc.entry+"    - export RUBYOPT=\"$RUBYOPT -W0\"\n    - bundle exec rspec\n")
			require.Equal(t, tc.configured, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
			jobs, _ := gitLabJobs(t, root)
			setup := jobs["rspec"].Steps[0].Setup
			if strings.Contains(tc.name, "condition") || strings.Contains(tc.name, "else") {
				require.Nil(t, setup)
				require.Contains(t, jobs["rspec"].Steps[0].ResolutionError, "combines the Datadog setup script with other commands")
				return
			}
			require.NotNil(t, setup)
			require.Equal(t, map[string]string{"languages": "ruby", "site": "datadoghq.com", "api_key": "$DD_API_KEY", "ruby-tracer-version": "1.42.0"}, setup.Inputs)
			if tc.configured {
				require.Equal(t, rubyInstallerPreload+" -W0", jobs["rspec"].Steps[2].Env["RUBYOPT"], "the script's exports reach later entries")
			} else {
				require.Equal(t, " -W0", jobs["rspec"].Steps[2].Env["RUBYOPT"], "a setup that cannot run exports nothing")
			}
		})
	}
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n"+gitLabGuardedEntry(gitLabJestSetup, "DD_API_KEY")+"    - "+esmImportCommand+"\n    - npx vitest run\n")
	writeFile(t, root, "package.json", `{"devDependencies": {"vitest": "3.2.0"}}`)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "vitest", ""))
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "vitest")
	require.Equal(t, StatusCompatible, result.Status, result)
}

func TestGitLabMissingKeyDisablesTheSetup(t *testing.T) {
	text := gitLabSetupText(ciSetupRequest{Language: "js", Site: "datadoghq.com", TracerInput: "js-tracer-version", TracerVersion: "5.133.0"}, "")
	require.Contains(t, text, `  if [ -n "${DD_API_KEY:-}" ]; then`)
	require.Contains(t, text, `    echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job." >&2`)
	setup, ok := gitLabSetupInvocation(strings.ReplaceAll(strings.TrimPrefix(text, "- |\n  "), "\n  ", "\n"), nil)
	require.True(t, ok)
	require.True(t, setup.Supported, "ddtest recognizes the entry it writes")
	require.Contains(t, gitLabCI{}.apiKeyAction(false), "disabled for those jobs and their tests run untraced")
	require.NotContains(t, gitLabCI{}.apiKeyAction(false), "stops")
	require.Contains(t, providerSetupInstructions(t.TempDir(), gitLabCI{}, "ruby", "rspec", "", true), "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job.")
}

// The setup entry needs Bash; images known to lack it run the job with sh.
func TestGitLabSetupNeedsBash(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, rubyJob("ruby:3.3-alpine", "", ""))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	jobs, _ := gitLabJobs(t, root)
	require.Contains(t, jobs["rspec"].Steps[0].Setup.Problem, "has no Bash")
	require.Empty(t, jobs["rspec"].Steps[1].Env["RUBYOPT"], "a setup that cannot run exports nothing")
	for image, refused := range map[string]bool{"node:22-alpine": true, "node:22-alpine3.20": true, "busybox": true, "gcr.io/distroless/nodejs22-debian12": true, "node:22": false, "registry.example.com/ci/node:22": false} {
		_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: " + image + "\n  script:\n    - npm test\n"})
		if refused {
			var gap *SetupGap
			require.ErrorAs(t, err, &gap, image)
			require.Contains(t, gap.Reason, "Bash-capable image", image)
		} else {
			require.NoError(t, err, image)
		}
	}
}

// The ESM loader export needs Node's module register API.
func TestGitLabApplyChecksTheESMNode(t *testing.T) {
	files := map[string]string{"package.json": `{"devDependencies": {"vitest": "3.2.0"}}`}
	files[".gitlab-ci.yml"] = "test:\n  image: node:18.17.1\n  script:\n    - npx vitest run\n"
	_, _, err := planGitLab(t, "vitest", files)
	var runtime *NodeRuntimeGap
	require.ErrorAs(t, err, &runtime)
	require.True(t, runtime.ESM)
	files[".gitlab-ci.yml"] = "test:\n  image: node:18.19.1\n  script:\n    - npx vitest run\n"
	_, _, err = planGitLab(t, "vitest", files)
	require.NoError(t, err)
}

// JavaScript validation counts a setup only when it is complete.
func TestGitLabJavaScriptRuntimeNeedsACompleteSetup(t *testing.T) {
	for name, setup := range map[string]string{
		"literal key":    strings.Replace(gitLabJestSetup, `"$DD_API_KEY"`, `"abc123"`, 1),
		"no version":     strings.Replace(gitLabJestSetup, ` JS_TRACER_VERSION="5.133.0"`, "", 1),
		"tampered":       "",
		"setup and test": "",
	} {
		t.Run(name, func(t *testing.T) {
			entry := gitLabSetupEntry(setup)
			switch name {
			case "tampered":
				entry = strings.Replace(gitLabSetupEntry(gitLabJestSetup), "c82030", "000000", 1)
			case "setup and test":
				entry = strings.TrimSuffix(gitLabSetupEntry(gitLabJestSetup), "\n") + "\n      npx jest\n"
			}
			root := newJestRepository(t, jestWorkflow)
			writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n"+entry+"    - npx jest\n")
			useCIProvider(t, "gitlab")
			result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
			}, "jest")
			require.NotEqual(t, StatusCompatible, result.Status, result)
			if name != "no version" { // That script still runs, installing the latest release.
				jobs, _ := gitLabJobs(t, root)
				require.NotContains(t, jobs["test"].Steps[1].Env["NODE_OPTIONS"], "DD_TRACE_PACKAGE")
			}
		})
	}
}

// The ESM loader export runs inside the key guard, so a job without the key
// runs its tests untraced; the shell model still tracks the export.
func TestGitLabGuardedESMLoader(t *testing.T) {
	plan, root, err := planGitLab(t, "vitest", map[string]string{".gitlab-ci.yml": "test:\n  image: node:22\n  script:\n    - npx vitest run\n", "package.json": `{"devDependencies": {"vitest": "3.2.0"}}`})
	require.NoError(t, err)
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	jobs, _ := gitLabJobs(t, root)
	require.NotNil(t, jobs["test"].Steps[0].Setup)
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}", jobs["test"].Steps[1].Env["NODE_OPTIONS"])
	require.Empty(t, jobs["test"].Steps[1].ResolutionError)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "vitest", ""))
	again, err := PlanJavaScriptSetup(root, "vitest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured(), "re-apply changes nothing")
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "vitest")
	require.Equal(t, StatusCompatible, result.Status, result)
	// A guarded entry that runs a command besides state changes is not a setup.
	writeFile(t, root, ".gitlab-ci.yml", strings.Replace(plannedFile(t, plan, ".gitlab-ci.yml"), esmImportCommand, "npx vitest run", 1))
	jobs, _ = gitLabJobs(t, root)
	require.Nil(t, jobs["test"].Steps[0].Setup)
}

func TestGitLabApplyRefusesAnUnresolvedLanguageSelector(t *testing.T) {
	_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:22\n  script:\n" + gitLabSetupEntry(strings.Replace(gitLabJestSetup, `LANGUAGES="js"`, `LANGUAGES="$DD_LANGUAGES"`, 1)) + "    - npm test\n"})
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "selects its languages from $DD_LANGUAGES")
}

func TestGitLabApplyMochaNeedsCoverage(t *testing.T) {
	files := map[string]string{"package.json": `{"devDependencies": {"mocha": "11.0.0", "nyc": "17.0.0"}}`, "test/unit.js": "it('a', () => {})\n", "test/esm.mjs": "it('b', () => {})\n"}
	files[".gitlab-ci.yml"] = "unit:\n  image: node:22\n  script:\n    - npx mocha test/unit.js\n"
	_, root, err := planGitLab(t, "mocha", files)
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Wrap it with npx --no-install nyc --")
	status, statusErr := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	require.NoError(t, statusErr)
	require.NotContains(t, string(status), "sha256sum", "nothing is edited")
}

// Every selected Mocha command decides the ESM loader, not only the first.
func TestGitLabApplyMochaLoaderFollowsEveryCommand(t *testing.T) {
	files := map[string]string{"package.json": `{"devDependencies": {"mocha": "11.0.0", "nyc": "17.0.0"}}`, "test/unit.js": "it('a', () => {})\n", "test/esm.mjs": "it('b', () => {})\n"}
	files[".gitlab-ci.yml"] = "unit:\n  image: node:22\n  script:\n    - npx nyc mocha test/unit.js\n    - npx nyc mocha test/esm.mjs\n"
	plan, _, err := planGitLab(t, "mocha", files)
	require.NoError(t, err)
	require.Contains(t, plannedFile(t, plan, ".gitlab-ci.yml"), "--import $DD_TRACE_ESM_IMPORT", "a later ESM command needs the loader")
	files[".gitlab-ci.yml"] = "unit:\n  image: node:22\n  script:\n    - npx nyc mocha test/unit.js\n"
	plan, _, err = planGitLab(t, "mocha", files)
	require.NoError(t, err)
	require.NotContains(t, plannedFile(t, plan, ".gitlab-ci.yml"), "--import")
}

// Modeled on gitlab-org/ruby/gems/gitlab-triage: GitLab's own scanning,
// review and release components run no tests and are reported; an unknown
// remote include still blocks.
func TestGitLabWellKnownComponentsAreCaveats(t *testing.T) {
	root := copyFixture(t, "gitlab-triage")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	review := strings.Join(scope.Review, "\n")
	for _, include := range []string{"gitlab.com/components/sast/sast@~latest is GitLab's SAST scanning component", "danger-review@~latest is GitLab's Danger merge request review component", "gem-release@~latest is GitLab's gem release component", "Jobs/Dependency-Scanning.gitlab-ci.yml is GitLab's dependency scanning template"} {
		require.Contains(t, review, include)
	}
	config, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	require.NoError(t, err)
	writeFile(t, root, ".gitlab-ci.yml", strings.Replace(string(config), "include:\n", "include:\n  - component: gitlab.com/acme/test-suite/run@1.0\n", 1))
	scope, err = DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "component include gitlab.com/acme/test-suite/run@1.0 cannot be read locally")
}

// The instructions name the image entries whose RubyGems the installer
// rejects and a matrix-safe guard, which ddtest follows per entry.
func TestGitLabRubyGemsGuidanceFollowsTheMatrix(t *testing.T) {
	root := copyFixture(t, "gitlab-triage")
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	text := providerSetupInstructions(root, gitLabCI{}, "ruby", "rspec", "", true)
	require.Contains(t, text, "These CI jobs run ruby:3.0, whose RubyGems is older than the 3.3.22")
	require.Contains(t, text, `- if [ "$RUBY_IMAGE" = "ruby:3.0" ]; then gem update --system 3.4.22; fi`)
	config, err := os.ReadFile(".gitlab-ci.yml")
	require.NoError(t, err)
	edited := strings.Replace(string(config), "  script:\n    - bundle exec rake spec", "  script:\n    - if [ \"$RUBY_IMAGE\" = \"ruby:3.0\" ]; then gem update --system 3.4.22; fi\n"+gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY")+"    - bundle exec rake spec", 1)
	writeFile(t, root, ".gitlab-ci.yml", edited)
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	for _, job := range RubyCIConfigurationForSite(root, "rspec", "").Jobs {
		require.Equal(t, "not checked", job.Status, job.Job+": "+job.Reason)
	}
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1, "the guard does not split the configuration")
}

// Image-only matrix entries share one local configuration; each entry's
// runtime is still checked statically.
func TestGitLabRuntimeOnlyMatrixSharesAConfiguration(t *testing.T) {
	root := copyFixture(t, "gitlab-triage")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Len(t, scope.Commands[0].locations, 4)
	runtimes := map[string]bool{}
	for _, job := range RubyCIConfigurationForSite(root, "rspec", "").Jobs {
		runtimes[job.Runtime] = true
	}
	require.Equal(t, map[string]bool{"3.0": true, "3.1": true, "3.2": true, "3.3": true}, runtimes)
}

// Modeled on renewedtab/renewedtab: deploy and packaging jobs never run
// Mocha and stay outside the scope; repository Node scripts that never
// mention Mocha are tooling; a literal config copy is replayable.
func TestGitLabRenewedtabFixture(t *testing.T) {
	root := copyFixture(t, "gitlab-renewedtab")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "npm test", scope.Commands[0].Command)
	var preparation []string
	for _, build := range scope.Commands[0].Prerequisites {
		preparation = append(preparation, build.Command)
	}
	// The repository Node scripts before the tests never mention Mocha; they
	// are replayed as reviewed preparation.
	require.Equal(t, []string{"cp config.example.json config.json", "npm run trans:compile", "npm run trans:check"}, preparation)
	review := strings.Join(scope.Review, "\n")
	for _, want := range []string{"sentry / step 9 (sentry-cli releases new ${VERSION}): Uninstrumented CI job that never runs mocha", "chrome / step 4 (npm run upload:chrome chrome.zip): Uninstrumented CI job that never runs mocha", "webext / step 4 (npm run package:webext): Uninstrumented CI job that never runs mocha"} {
		require.Contains(t, review, want)
	}
	require.NotContains(t, review, "${{", "unresolved values are named by their CI text")
	require.Contains(t, review, "its CI value renewedtab@$CI_COMMIT_TAG reads values only CI knows")
	caveats := strings.Join(scope.Caveats, "\n")
	for _, job := range []string{"chrome", "firefox", "sentry", "webext"} {
		require.Contains(t, caveats, ".gitlab-ci.yml / "+job+": CI job without the Datadog setup that never runs mocha")
	}
	require.Equal(t, 1, strings.Count(caveats, "/ sentry:"), "one caveat per job, not per step")
	// A job running an unknown wrapper, or a script that mentions the
	// framework, stays in scope.
	writeFile(t, root, "utils/upload_chrome.js", "require('child_process').execSync('npx mocha')\n")
	scope, err = DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "chrome / step 4 (npm run upload:chrome chrome.zip)")
}

// The Gemfile pins minitest, but CI runs RSpec: onboarding sets up the
// framework CI runs, not the pinned one.
func TestOnboardingUsesTheFrameworkCIRuns(t *testing.T) {
	root := copyFixture(t, "gitlab-triage")
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	state, err := rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code, state.Reason)
	require.Contains(t, state.ResumeCommand, "rspec")
	require.NotContains(t, state.Instruction+state.Reason, "minitest tests")
}

// After the prescribed edits, renewedtab's CI runtime check only evaluates
// the Mocha job: the deploy and packaging jobs discovery reports as outside
// validation are not consumers, so the check and tracer agreement are
// compatible.
func TestGitLabRenewedtabRuntimeCheckSkipsOutsideJobs(t *testing.T) {
	root := copyFixture(t, "gitlab-renewedtab")
	useCIProvider(t, "gitlab")
	config, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	require.NoError(t, err)
	writeFile(t, root, ".gitlab-ci.yml", strings.Replace(string(config), "    - npm test\n", gitLabGuardedEntry(gitLabJestSetup, "DD_API_KEY")+"    - npx --no-install nyc -- npm test\n", 1))
	manifest, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	writeFile(t, root, "package.json", strings.Replace(string(manifest), `"mocha":`, `"nyc": "17.1.0", "mocha":`, 1))
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "mocha")
	require.Equal(t, StatusCompatible, result.Status, result)
	for _, job := range result.Jobs {
		require.Equal(t, "test", job.Job, job.Reason)
	}
}

// A gemspec listing its files with git ls-files leaves out what .gitignore
// excludes: no gemspec edit is asked for. One using globs needs it.
func TestRubyGemPackagingGuidance(t *testing.T) {
	root := copyFixture(t, "gitlab-triage")
	t.Chdir(root)
	text := providerSetupInstructions(root, gitLabCI{}, "ruby", "rspec", "", true)
	require.Contains(t, text, "The gemspec lists its files with git ls-files, so that .gitignore entry also keeps the reports out of the gem: do not edit the gemspec.")
	require.Contains(t, rubyDiscovery{}.agentInstructions("rspec", ""), "do not edit the gemspec")
	writeFile(t, root, "gitlab-triage.gemspec", "Gem::Specification.new { |spec| spec.files = Dir['**/*'] }\n")
	require.Contains(t, providerSetupInstructions(root, gitLabCI{}, "ruby", "rspec", "", true), "the gemspec lists its files with globs, so exclude .testoptimization/ from its files")
	require.NoError(t, os.Remove(filepath.Join(root, "gitlab-triage.gemspec")))
	require.Contains(t, rubyDiscovery{}.agentInstructions("rspec", ""), "and from gem packaging.", "unchanged without a gemspec")
}

// The guard compares the image as written with each entry's image, so it
// also works when the matrix variable is only the tag.
func TestGitLabRubyGemsGuardForTagMatrices(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, "rspec:\n  image: ruby:${RUBY_VERSION}\n  parallel:\n    matrix:\n      - RUBY_VERSION: ['3.0', '3.3']\n  script:\n    - bundle exec rspec\n")
	text := providerSetupInstructions(root, gitLabCI{}, "ruby", "rspec", "", true)
	guard := `if [ "ruby:${RUBY_VERSION}" = "ruby:3.0" ]; then gem update --system 3.4.22; fi`
	require.Contains(t, text, "- "+guard)
	writeFile(t, root, ".gitlab-ci.yml", "rspec:\n  image: ruby:${RUBY_VERSION}\n  parallel:\n    matrix:\n      - RUBY_VERSION: ['3.0', '3.3']\n  script:\n    - "+guard+"\n"+gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY")+"    - bundle exec rspec\n")
	for _, job := range RubyCIConfigurationForSite(root, "rspec", "").Jobs {
		require.Equal(t, "not checked", job.Status, job.Job+": "+job.Reason)
	}
}

// A package runner's target is an executable: an unknown one keeps a job
// in scope, on GitHub Actions too.
func TestPackageRunnerTargetsKeepJobsInScope(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  release:\n    steps:\n      - run: npx company-runner --ci\n")
	writeScripts(t, root, map[string]string{"test": "mocha"})
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "npx company-runner")
	for _, command := range []string{"npm exec company-runner", "yarn dlx company-runner", "pnpm exec company-runner", "bunx company-runner"} {
		require.True(t, mentionsFramework(root, root, command, "mocha", 0), command)
	}
	require.False(t, mentionsFramework(root, root, "npx web-ext build", "mocha", 0))
}

// A repository Node script before the tests may generate their inputs: it
// stays preparation of the test, replayed after review, on GitHub Actions too.
func TestNodeGeneratorsBeforeTestsArePreparation(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: node scripts/generate.js\n      - run: npx mocha\n")
	writeScripts(t, root, map[string]string{"test": "mocha"})
	writeFile(t, root, "scripts/generate.js", "require('fs').writeFileSync('fixtures.json', '{}')\n")
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Len(t, scope.Commands[0].Prerequisites, 1)
	require.Equal(t, "node scripts/generate.js", scope.Commands[0].Prerequisites[0].Command)
	require.Equal(t, PreparationPackageBuild, scope.Commands[0].Prerequisites[0].Kind)
}

// A test command shared through a YAML alias can't get one insertion for
// every job: apply asks for each job's own entry.
func TestGitLabApplyRefusesAliasedEntries(t *testing.T) {
	_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:22\n  script:\n    - &tests npm test\nmore:\n  image: node:22\n  script:\n    - npm ci\n    - *tests\n"})
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "YAML anchor and alias")
}

// A GitLab Cypress job keeps the Node its image selects.
func TestGitLabCypressKeepsTheImageNode(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, "package.json", `{"devDependencies": {"cypress": "15.1.0"}}`)
	writeFile(t, root, "cypress.config.js", "module.exports = {}\n")
	writeFile(t, root, ".gitlab-ci.yml", "e2e:\n  image: node:22\n  script:\n"+gitLabSetupEntry(gitLabJestSetup)+"    - npx cypress run\n")
	useCIProvider(t, "gitlab")
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "cypress")
	require.NotEmpty(t, result.Jobs)
	for _, job := range result.Jobs {
		require.NotEqual(t, CodeRuntimeUnknown, job.Code, job.Reason)
		require.Equal(t, "22", job.Node)
	}
}

// A repository shell wrapper is followed like a CI step: ci/test.sh running
// npm test (Jest) keeps its job in scope.
func TestShellWrappersAreFollowed(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, "ci/test.sh", "#!/bin/sh\nset -e\nnpm test\n")
	writeFile(t, root, "ci/package.sh", "#!/bin/sh\ntar czf dist.tgz dist\n")
	require.True(t, mentionsFramework(root, root, "./ci/test.sh", "jest", 0))
	require.True(t, mentionsFramework(root, root, "sh ci/test.sh", "jest", 0))
	require.False(t, mentionsFramework(root, root, "./ci/package.sh", "jest", 0))
}

// A Node launcher that starts Mocha through a local module, or a process
// command, may run tests; a generator that does neither is tooling.
func TestNodeLaunchersAreInspectedThroughImports(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "mocha"})
	writeFile(t, root, "runner.js", "require('./lib/launch')\n")
	writeFile(t, root, "lib/launch.js", "const Mocha = require('mocha')\nnew Mocha().run()\n")
	writeFile(t, root, "spawner.js", "const { execSync } = require('child_process')\nexecSync('npm test')\n")
	writeFile(t, root, "computed.js", "const { execSync } = require('child_process')\nexecSync(process.argv[2])\n")
	writeFile(t, root, "generate.js", "const { execSync } = require('child_process')\nexecSync(`formatjs compile ${input} --out-file out.json`)\n")
	for script, mayRun := range map[string]bool{"runner.js": true, "spawner.js": true, "computed.js": true, "generate.js": false} {
		resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "node " + script}, "javascript", "mocha")
		if mayRun {
			require.NotEmpty(t, resolution.Reason, script)
			require.Empty(t, resolution.Builds, script)
		} else {
			require.Len(t, resolution.Builds, 1, script)
		}
	}
}

// A lint or type check after the job's last test is outside the test path.
func TestPostTestChecksAreOutsideTheTestPath(t *testing.T) {
	root := copyFixture(t, "gitlab-renewedtab")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	review := strings.Join(scope.Review, "\n")
	require.Contains(t, review, "test / step 7 (npm run lint): After the job's last mocha test step; lint or type check, outside the test path: tsc --noemit")
	require.NotContains(t, review, "Post-test preparation requires separate review: tsc --noemit")
}

// A guard written with set -u in mind is recognized too.
func TestUnsetSafeKeyGuardIsRecognized(t *testing.T) {
	useCIProvider(t, "gitlab")
	entry := strings.Replace(gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY"), `"$DD_API_KEY" ]`, `"${DD_API_KEY:-}" ]`, 1)
	root := gitLabRubyRepository(t, "rspec:\n  image: ruby:3.3\n  script:\n    - set -u\n"+entry+"    - bundle exec rspec\n")
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
}

// The installer replaces RUBYOPT: options the job set must be restored
// after it, and restored ones validate.
func TestGitLabRubyOptionsAfterTheInstaller(t *testing.T) {
	useCIProvider(t, "gitlab")
	job := "rspec:\n  image: ruby:3.3\n  variables:\n    RUBYOPT: -W0\n  script:\n" + gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY") + "AFTER    - bundle exec rspec\n"
	root := gitLabRubyRepository(t, strings.Replace(job, "AFTER", "    - export RUBYOPT=\"$RUBYOPT -W0\"\n", 1))
	for _, finding := range RubyCIConfigurationForSite(root, "rspec", "").Jobs {
		require.Equal(t, "not checked", finding.Status, finding.Reason)
	}
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, "-W0", scope.Commands[0].Environment["RUBYOPT"])
	root = gitLabRubyRepository(t, strings.Replace(job, "AFTER", "", 1))
	reasons := ""
	for _, finding := range RubyCIConfigurationForSite(root, "rspec", "").Jobs {
		reasons += finding.Reason
	}
	require.Contains(t, reasons, "replaces RUBYOPT, dropping the job's -W0")
}

// The installer checks RubyGems when the setup runs: an update after it is
// too late.
func TestGitLabRubyGemsUpdateMustPrecedeTheSetup(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, rubyJob("ruby:3.0", "", "    - gem update --system 3.4.22\n"))
	reasons := ""
	for _, finding := range RubyCIConfigurationForSite(root, "rspec", "").Jobs {
		reasons += finding.Reason
	}
	require.Contains(t, reasons, "Run gem update --system 3.4.22 before it")
}

// Official slim images have no curl, so the setup script can't be
// downloaded: the setup is incomplete and apply asks for the fix. An
// earlier curl install makes it runnable.
func TestGitLabSlimImagesNeedCurl(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, rubyJob("ruby:3.3-slim", "", ""))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	jobs, _ := gitLabJobs(t, root)
	require.Contains(t, jobs["rspec"].Steps[0].Setup.Problem, "has no curl")
	root = gitLabRubyRepository(t, rubyJob("ruby:3.3-slim", "    - "+gitLabCurlInstall+"\n", ""))
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:22-bookworm-slim\n  script:\n    - npm test\n"})
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, gitLabCurlInstall)
	_, _, err = planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: node:22-slim\n  script:\n    - " + gitLabCurlInstall + "\n    - npm test\n"})
	require.NoError(t, err)
}

// An image ddtest cannot resolve may lack Bash or curl: no apply, no
// complete setup.
func TestGitLabUnresolvedImagesBlockSetup(t *testing.T) {
	useCIProvider(t, "gitlab")
	_, _, err := planGitLab(t, "jest", map[string]string{".gitlab-ci.yml": "unit:\n  image: $NODE_IMAGE\n  script:\n    - npm test\n"})
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "depends on a variable ddtest cannot resolve")
	root := gitLabRubyRepository(t, rubyJob("$RUBY_IMAGE", "", ""))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
}

// Container-launched tests stay in scope.
func TestContainerLaunchedTestsStayInScope(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  e2e:\n    steps:\n      - run: docker compose run --rm tests\n")
	require.True(t, mentionsFramework(root, root, "docker compose run --rm tests", "jest", 0))
	require.True(t, mentionsFramework(root, root, "docker run node:22 npm test", "jest", 0))
}

// A negated key guard runs the setup without the key: not a setup.
func TestGitLabNegatedKeyGuardIsNotASetup(t *testing.T) {
	useCIProvider(t, "gitlab")
	entry := strings.Replace(gitLabGuardedEntry(gitLabRubySetup, "DD_API_KEY"), `if [ -n "$DD_API_KEY" ]`, `if ! [ -n "$DD_API_KEY" ]`, 1)
	root := gitLabRubyRepository(t, "rspec:\n  image: ruby:3.3\n  script:\n"+entry+"    - bundle exec rspec\n")
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
}

// A single-quoted '$DD_API_KEY' is passed to the installer as text, not
// the key, whether in the entry or an earlier assignment.
func TestGitLabLiteralAPIKeyIsNotAReference(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, rubyJob("ruby:3.3", "", "")+"")
	writeFile(t, root, ".gitlab-ci.yml", strings.Replace(rubyJob("ruby:3.3", "", ""), `API_KEY="$DD_API_KEY"`, `API_KEY='$DD_API_KEY'`, 1))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	writeFile(t, root, ".gitlab-ci.yml", "rspec:\n  image: ruby:3.3\n  script:\n    - API_KEY='$DD_API_KEY'\n"+gitLabSetupEntry(`LANGUAGES="ruby" RUBY_TRACER_VERSION="1.42.0"`)+"    - bundle exec rspec\n")
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
	writeFile(t, root, ".gitlab-ci.yml", "rspec:\n  image: ruby:3.3\n  script:\n    - API_KEY=\"$DD_API_KEY\"\n"+gitLabSetupEntry(`LANGUAGES="ruby" RUBY_TRACER_VERSION="1.42.0"`)+"    - bundle exec rspec\n")
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
}

// Changing an exported variable such as PATH reaches later commands:
// a PATH that selects another runtime is not ignored, while prepending the
// repository's node_modules/.bin is.
func TestGitLabInheritedVariableAssignments(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "other:\n  script:\n    - PATH=/opt/node16/bin:$PATH\n    - npm test\nlocal:\n  script:\n    - PATH=$(realpath ./node_modules/.bin):$PATH\n    - npm test\n")
	jobs, _ := gitLabJobs(t, root)
	require.Contains(t, jobs["other"].Steps[1].Env, "PATH")
	require.NotContains(t, jobs["local"].Steps[1].Env, "PATH")
}
