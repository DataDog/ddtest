// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/languageguard"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// testCI is a second provider that exists only in tests: GitHub Actions is the
// only real one. It reads .testci.yml, which uses the shared workflow syntax,
// and has no setup mechanism.
type testCI struct{}

const testCIFile = ".testci.yml"

func (testCI) name() string        { return "testci" }
func (testCI) displayName() string { return "Test CI" }
func (testCI) environment() map[string]string {
	return map[string]string{"CI": "true", "TESTCI": "true"}
}

func (testCI) configurationFiles(root string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, testCIFile)); err != nil {
		return nil, nil
	}
	return []string{testCIFile}, nil
}

func (p testCI) readWorkflows(root string) ([]ciWorkflow, error) {
	contents, err := os.ReadFile(filepath.Join(root, testCIFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var workflow ciWorkflow
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		return nil, fmt.Errorf("parse %s: %w", testCIFile, err)
	}
	workflow.Path = testCIFile
	// Steps it cannot run, such as includes, stay unresolved.
	for name, job := range workflow.Jobs {
		for i, step := range job.Steps {
			if step.Uses != "" {
				job.Steps[i].ResolutionError = "unresolved include " + step.Uses
			}
		}
		workflow.Jobs[name] = job
	}
	return []ciWorkflow{workflow}, nil
}

func (p testCI) findTestWorkflows(root, language, framework string) ([]string, []string, error) {
	workflows, err := p.readWorkflows(root)
	if err != nil || len(workflows) == 0 {
		return nil, nil, err
	}
	for _, job := range workflows[0].Jobs {
		for _, step := range job.Steps {
			if resolveTestStep(root, workflows[0], job, step, language, framework).Matched {
				return []string{testCIFile}, nil, nil
			}
		}
	}
	return nil, nil, nil
}

// registerTestCI registers testCI after GitHub Actions for the test.
func registerTestCI(t *testing.T) {
	t.Helper()
	registered := ciProviders
	ciProviders = append(slices.Clone(registered), testCI{})
	UseCIProvider("")
	t.Cleanup(func() {
		ciProviders = registered
		UseCIProvider("")
	})
}

// useCIProvider selects provider for the test.
func useCIProvider(t *testing.T, provider string) {
	t.Helper()
	UseCIProvider(provider)
	t.Cleanup(func() { UseCIProvider("") })
}

const jestWorkflow = "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n"

func writeFile(t *testing.T, root, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(contents), 0o644))
}

func TestGitHubActionsConfigurationFiles(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		github     bool
	}{
		{"no CI", "", false},
		{"another provider", ".gitlab-ci.yml", false},
		{"GitHub metadata only", ".github/CODEOWNERS", false},
		{"workflow documentation", ".github/workflows/README.md", false},
		{"yml workflow", ".github/workflows/test.yml", true},
		{"yaml workflow", ".github/workflows/test.yaml", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// The host CI must not select the provider of local onboarding.
			t.Setenv("GITLAB_CI", "true")
			if tc.path != "" {
				// Selection lists configuration; parsing belongs to discovery.
				writeFile(t, root, tc.path, "invalid: [")
			}
			files, err := gitHubActions{}.configurationFiles(root)
			require.NoError(t, err)
			selected, err := SelectCIProvider(root, "")
			require.NoError(t, err)
			if tc.github {
				require.Equal(t, []string{tc.path}, files)
				require.Equal(t, "github", selected)
				_, err := readWorkflows(root)
				require.ErrorContains(t, err, "parse ")
			} else {
				require.Empty(t, files)
				require.Empty(t, selected)
			}
		})
	}
}

func TestSelectCIProvider(t *testing.T) {
	registerTestCI(t)
	for _, tc := range []struct {
		name, github, testci, requested string
		want                            string
		choice                          []string
		err                             string
	}{
		{name: "no CI configuration"},
		{name: "GitHub only", github: jestWorkflow, want: "github"},
		{name: "test provider only", testci: jestWorkflow, want: "testci"},
		{name: "both run tests", github: jestWorkflow, testci: jestWorkflow, choice: []string{"github", "testci"}},
		{name: "only the test provider runs tests", github: "jobs:\n  lint:\n    steps:\n      - run: echo lint\n", testci: jestWorkflow, want: "testci"},
		{name: "neither runs tests", github: "jobs:\n  lint:\n    steps:\n      - run: echo lint\n", testci: "jobs:\n  deploy:\n    steps:\n      - run: echo deploy\n", want: "github"},
		{name: "unparseable unselected configuration is not a candidate", github: jestWorkflow, testci: "jobs: [", want: "github"},
		{name: "unresolved steps are candidates", github: jestWorkflow, testci: "jobs:\n  test:\n    steps:\n      - uses: ./templates/test.yml\n", choice: []string{"github", "testci"}},
		{name: "explicit provider skips the choice", github: jestWorkflow, testci: jestWorkflow, requested: "testci", want: "testci"},
		{name: "explicit provider is never parsed", github: "jobs: [", requested: "github", want: "github"},
		{name: "unknown provider", github: jestWorkflow, requested: "gitlab", err: `unsupported --ci-provider "gitlab"; supported CI providers: github, testci`},
		{name: "a disabled job is not a test path", github: jestWorkflow, testci: "jobs:\n  test:\n    if: ${{ false }}\n    steps:\n      - run: npm test\n", want: "github"},
		{name: "a disabled step is not a test path", github: jestWorkflow, testci: "jobs:\n  test:\n    steps:\n      - if: ${{ false }}\n        run: npm test\n", want: "github"},
		{name: "an unresolved condition may still run", github: jestWorkflow, testci: "jobs:\n  test:\n    if: ${{ github.event_name == 'push' }}\n    steps:\n      - run: npm test\n", choice: []string{"github", "testci"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, tc.github)
			if tc.github == "" {
				require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
			}
			if tc.testci != "" {
				writeFile(t, root, testCIFile, tc.testci)
			}
			selected, err := SelectCIProvider(root, tc.requested)
			var choice *CIProviderChoice
			switch {
			case tc.err != "":
				require.EqualError(t, err, tc.err)
			case tc.choice != nil:
				require.ErrorAs(t, err, &choice)
				require.Equal(t, tc.choice, choice.Providers)
			default:
				require.NoError(t, err)
				require.Equal(t, tc.want, selected)
			}
		})
	}
}

func TestProviderChoiceAsksTheUserBeforeAnyCommand(t *testing.T) {
	registerTestCI(t)
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, testCIFile, jestWorkflow)
	t.Chdir(root)
	var out bytes.Buffer
	err := StartWithOptions(&out, StartOptions{Format: "json"})
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	var result AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, "ci_provider_required", result.Code)
	require.Equal(t, "ask_user", result.NextAction)
	require.Equal(t, []string{"github", "testci"}, result.RequiredInput)
	require.Empty(t, result.NextCommand)
	require.Empty(t, result.ResumeCommand)
	require.Contains(t, result.Reason, "--ci-provider <provider>")
	require.NoDirExists(t, filepath.Join(root, ".testoptimization"))

	// Human guidance asks too, instead of picking one.
	out.Reset()
	require.ErrorAs(t, Run(&out), &choice)
	require.Empty(t, out.String())
}

func TestSelectedProviderDrivesDiscovery(t *testing.T) {
	registerTestCI(t)
	root := newJestRepository(t, "jobs:\n  unit:\n    steps:\n      - run: npx jest --ci\n")
	writeFile(t, root, testCIFile, "jobs:\n  test:\n    env: {SHARD: '1'}\n    steps:\n      - run: npx jest\n")

	useCIProvider(t, "testci")
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	require.Len(t, workflows, 1)
	require.Equal(t, testCIFile, workflows[0].Path)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, testCIFile, scope.Commands[0].locations[0].Workflow)
	// The unselected GitHub workflow stays visible, as outside this validation.
	require.Contains(t, scope.Review, ".github/workflows/test.yml: not a Test CI workflow; ddtest does not analyze it, so jest tests it runs are outside this validation and need their own Datadog setup.")
	require.False(t, slices.ContainsFunc(scope.Review, func(review string) bool { return strings.HasPrefix(review, testCIFile) }), scope.Review)
	require.Equal(t, map[string]string{"CI": "true", "TESTCI": "true", "X": "1"}, WithCIProviderDefaults(map[string]string{"X": "1"}))
	require.Equal(t, "Test CI", CIProviderDisplayName())

	useCIProvider(t, "github")
	scope, err = DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, ".github/workflows/test.yml", scope.Commands[0].locations[0].Workflow)
	require.Contains(t, scope.Review, ".testci.yml: not a GitHub Actions workflow; ddtest does not analyze it, so jest tests it runs are outside this validation and need their own Datadog setup.")
	require.Equal(t, map[string]string{"CI": "true", "GITHUB_ACTIONS": "true", "X": "1"}, WithCIProviderDefaults(map[string]string{"X": "1"}))
}

func TestGitHubActionsIsTheDefaultProvider(t *testing.T) {
	UseCIProvider("")
	require.Equal(t, []string{"github"}, CIProviderNames())
	require.Equal(t, "GitHub Actions", CIProviderDisplayName())
	require.Equal(t, map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}, WithCIProviderDefaults(nil))
}

func TestUnsupportedProviderGetsNoGitHubSetup(t *testing.T) {
	registerTestCI(t)
	for _, tc := range []struct {
		name, framework string
		write           func(root string)
	}{
		{"jest", "jest", func(root string) {
			writeFile(t, root, "package.json", `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.0.0"}}`)
			writeFile(t, root, testCIFile, jestWorkflow)
		}},
		{"rspec", "rspec", func(root string) {
			writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\n")
			writeFile(t, root, testCIFile, "jobs:\n  test:\n    steps:\n      - run: bundle exec rspec\n")
		}},
		{"pytest", "pytest", func(root string) {
			writeFile(t, root, "pyproject.toml", "[tool.pytest.ini_options]\n")
			writeFile(t, root, testCIFile, "jobs:\n  test:\n    steps:\n      - run: pytest\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.write(root)
			t.Chdir(root)

			var out bytes.Buffer
			err := StartWithOptions(&out, StartOptions{Format: "json"})
			var unsupported *CIProviderUnsupportedError
			require.ErrorAs(t, err, &unsupported)
			var result AgentResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
			require.Equal(t, "unsupported", result.Status)
			require.Equal(t, "ci_provider_unsupported", result.Code)
			require.Equal(t, "report_result", result.NextAction)
			require.Empty(t, result.NextCommand)
			require.Empty(t, result.ResumeCommand)
			require.Empty(t, result.Instruction)
			require.Contains(t, result.Reason, tc.framework+" CI setup")
			require.Contains(t, result.Reason, "on Test CI")

			out.Reset()
			err = Run(&out)
			require.ErrorAs(t, err, &unsupported)
			require.Empty(t, out.String(), "no setup instructions for an unsupported provider")
			require.NotContains(t, err.Error(), "GitHub")
			require.NotContains(t, err.Error(), "action")

			require.ErrorAs(t, CheckCIProviderSupport(root, unsupported.Language, tc.framework), &unsupported)
			out.Reset()
			require.ErrorAs(t, StartWithOptions(&out, StartOptions{Human: true}), &unsupported)
			require.Empty(t, out.String())
		})
	}
}

func TestEveryLanguageSupportsGitHubActions(t *testing.T) {
	for _, language := range discoveryLanguages {
		require.True(t, languageSupportsCIProvider(language.name(), gitHubActions{}), language.name())
		require.False(t, languageSupportsCIProvider(language.name(), testCI{}), language.name())
	}
	require.False(t, languageSupportsCIProvider("unregistered", gitHubActions{}))
}

func TestUnparseableOtherCIFilesNeverAbortOnboarding(t *testing.T) {
	registerTestCI(t)
	for _, tc := range []struct{ name, path, contents string }{
		// A Groovy slashy string that a Jenkinsfile reader could not tokenize.
		{"Jenkinsfile", "Jenkinsfile", "def re = ~/it's/\npipeline { stages { stage('test') { steps { sh 'npx jest' } } } }\n"},
		{"Buildkite list form", ".buildkite/pipeline.yml", "- command: npx jest\n"},
		{"registered unselected provider", testCIFile, "jobs: [npx jest\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, jestWorkflow)
			writeFile(t, root, tc.path, tc.contents)
			t.Chdir(root)
			selected, err := SelectCIProvider(root, "")
			require.NoError(t, err)
			require.Equal(t, "github", selected)
			var out bytes.Buffer
			require.NoError(t, StartWithOptions(&out, StartOptions{Format: "json"}))
			var result AgentResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
			require.Equal(t, "ready", result.Status)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 1)
			require.Contains(t, scope.Review, tc.path+": not a GitHub Actions workflow; ddtest does not analyze it, so jest tests it runs are outside this validation and need their own Datadog setup.")
		})
	}
}

func TestOtherProviderTestJobsStayOutsideValidation(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npx jest\n")
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  script:\n    - npx jest\n")
	for _, requested := range []string{"", "github"} {
		useCIProvider(t, requested)
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1)
		require.Contains(t, scope.Review, ".gitlab-ci.yml: not a GitHub Actions workflow; ddtest does not analyze it, so jest tests it runs are outside this validation and need their own Datadog setup.")
	}
}

func TestFollowUpCommandsKeepTheSelectedProvider(t *testing.T) {
	useCIProvider(t, "")
	require.NotContains(t, DDTestCommand("testdrive", "--report"), "--ci-provider")
	useCIProvider(t, "github")
	for _, command := range [][]string{OnboardingCommand("jest"), DDTestCommand("testdrive", "--report"), DDTestCommand("onboard", "--framework", "jest", "--instructions")} {
		require.Equal(t, []string{"--ci-provider", "github"}, command[len(command)-2:], command)
	}
	require.Equal(t, 1, strings.Count(strings.Join(DDTestCommand("testdrive", "--ci-provider", "github"), " "), "--ci-provider"))
	require.NotContains(t, DDTestCommand("instrument"), "--ci-provider")
}

func TestUnknownActiveProviderFails(t *testing.T) {
	useCIProvider(t, "gitlab")
	_, err := readWorkflows(t.TempDir())
	require.EqualError(t, err, `unsupported --ci-provider "gitlab"; supported CI providers: github`)
}

// Provider files parse CI configuration into neutral facts; language decisions
// stay behind discoveryLanguage.
func TestCIProviderFilesNameNoLanguage(t *testing.T) {
	languageguard.CheckNeutral(t, ".", "ci_provider*.go", "ci_providers.go")
}

// A run without the selected provider's configuration consults no CI, so the
// provider's language support doesn't apply.
func TestProviderSupportOnlyAppliesWithItsConfiguration(t *testing.T) {
	registerTestCI(t)
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\n")
	useCIProvider(t, "testci")
	require.NoError(t, CheckCIProviderSupport(root, "ruby", "rspec"))
	writeFile(t, root, testCIFile, "jobs:\n  test:\n    steps:\n      - run: bundle exec rspec\n")
	var unsupported *CIProviderUnsupportedError
	require.ErrorAs(t, CheckCIProviderSupport(root, "ruby", "rspec"), &unsupported)
}

// Human follow-ups keep the selected provider when several are configured,
// so rerunning them doesn't ask again; with one provider they stay unchanged.
func TestHumanFollowUpsKeepTheSelectedProvider(t *testing.T) {
	registerTestCI(t)
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: js\n      - run: npm test\n")
	t.Chdir(root)
	var output bytes.Buffer
	require.NoError(t, Run(&output))
	require.Contains(t, output.String(), "Run `ddtest testdrive --framework jest` to check the setup locally.")

	writeFile(t, root, testCIFile, jestWorkflow)
	useCIProvider(t, "github")
	output.Reset()
	require.NoError(t, Run(&output))
	require.Contains(t, output.String(), "Run `ddtest testdrive --framework jest --ci-provider github` to check the setup locally.")
}

// Checkout-level checks list the selected provider's configuration only.
func TestConfigurationFilesFollowTheSelectedProvider(t *testing.T) {
	registerTestCI(t)
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, testCIFile, jestWorkflow)
	useCIProvider(t, "testci")
	files, err := CIProviderConfigurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{testCIFile}, files)
	useCIProvider(t, "github")
	files, err = CIProviderConfigurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{".github/workflows/test.yml"}, files)
}
