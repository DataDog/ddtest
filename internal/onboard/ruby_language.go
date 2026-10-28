// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

//go:embed instructions/ruby.md
var rubyInstructions string

// RubyMinitestForkSetup configures the same synchronous transport in CI and
// local validation for test runners that terminate forked children with exit!.
//
//go:embed instructions/ruby_minitest_fork.rb
var RubyMinitestForkSetup string

//go:embed instructions/ruby_minitest_fork.md
var rubyMinitestForkInstructions string

// rubyAgentInstructions are the CI setup steps for a coding agent; ddtest
// hands off the validation command separately.
//
//go:embed instructions/ruby_agent.md
var rubyAgentInstructions string

// rubyDiscovery onboards RSpec and Minitest with datadog-ci.
type rubyDiscovery struct{}

func (rubyDiscovery) name() string { return "ruby" }

func (rubyDiscovery) actionLanguage() string { return "ruby" }

func (rubyDiscovery) bootstrap(string) string { return rubyBootstrap }

func (rubyDiscovery) resolvesTestSteps(string) bool { return true }

// Agents apply Ruby CI setup by hand; validation then runs through the
// coordinator.
func (rubyDiscovery) coordinated() bool { return true }

func (rubyDiscovery) automaticCISetup() bool { return false }

// Ruby setup uses the Datadog GitHub Actions setup action or the Datadog
// GitLab setup script.
func (rubyDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider || provider == gitLabCIProvider
}

// The installer adds datadog-ci to the bundle and replaces RUBYOPT with its
// preload; ${{ env.RUBYOPT }} is that exported value, as on GitHub Actions.
func (rubyDiscovery) setupShellExports(map[string]string) (map[string]string, map[string]string) {
	return map[string]string{"RUBYOPT": rubyInstallerPreload}, nil
}

// rubyInstallerPreload is the RUBYOPT the Datadog installer exports in the
// job's shell, replacing any earlier value.
const rubyInstallerPreload = "-rbundler/setup -rdatadog/ci/auto_instrument"

// Every discovered test step runs after a complete Datadog action or
// explicitly skips it; at least one stays instrumented. Coordinated validation
// enforces the same rule through RubyCIConfigurationForSite.
func (rubyDiscovery) ciSetupConfigured(root, framework, site string) bool {
	instrumented := false
	for _, job := range RubyCIConfigurationForSite(root, framework, site).Jobs {
		if job.Status == "excluded" {
			continue
		}
		if job.Action == "" || rubyActionProblem(job, site) != "" {
			return false
		}
		instrumented = true
	}
	return instrumented
}

// exactRubyRelease is a single RubyGems release, never a requirement or "latest".
var exactRubyRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9A-Za-z]+)*$`)

// rubyActionProblem explains why the Datadog setup before an instrumented
// entry is not a complete setup, in its provider's terms: a supported
// release, an API key, an exact tracer release and the selected site (empty:
// not selected). Empty when it is.
func rubyActionProblem(job RuntimeFinding, site string) string {
	provider := job.setupProvider
	if provider == nil {
		provider = activeOrDefaultCIProvider()
	}
	inputs := maps.Clone(job.setupInputs)
	if inputs == nil {
		inputs = map[string]string{}
	}
	inputs["ruby-tracer-version"] = job.TracerRequested
	supported := job.setupSupported || provider.sharedSyntax() && setupActionSupported(job.Action)
	return provider.setupProblem(ciSetup{Reference: job.Action, Pinned: true, Supported: supported, Problem: job.setupReadProblem, Inputs: inputs}, "ruby-tracer-version", exactRubyRelease.MatchString, "1.42.0", site)
}

func (rubyDiscovery) agentInstructions(framework, site string) string {
	siteInput := ""
	if site != "" && site != defaultDatadogSite {
		siteInput = ", `site: " + site + "` (the selected Datadog site)"
	}
	text := strings.NewReplacer("__FRAMEWORK__", framework, "__SITE_INPUT__", siteInput, " and from gem packaging.", rubyGemPackaging(".")).Replace(rubyAgentInstructions)
	return withMinitestForkSetup(text, framework)
}

func (rubyDiscovery) frameworks() []string { return []string{"rspec", "minitest"} }

func (rubyDiscovery) resolveStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) (commandResolution, bool) {
	return resolveRubyStep(root, workflow, job, step, framework), true
}

func (rubyDiscovery) localCommands(root, command, framework string) ([]TestCommand, bool) {
	return resolveRubyStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, framework).Tests, true
}

func (rubyDiscovery) variantValue(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, key, value string) (string, bool, error) {
	switch key {
	case "BUNDLE_GEMFILE":
		// Bundler resolves ordinary paths from the test working directory;
		// github.workspace paths instead start at the checkout root.
		relative := strings.NewReplacer("${{ github.workspace }}/", "", "${{github.workspace}}/", "").Replace(value)
		workspacePath := relative != value
		value, err := interpolateMatrix(relative, row)
		if err != nil {
			return "", false, err
		}
		directory := filepath.Clean(stepDirectory(workflow, job, step))
		target := filepath.Join(directory, value)
		if workspacePath {
			target = value
		}
		if !filepath.IsLocal(target) || filepath.IsAbs(value) || strings.ContainsAny(value, "$`~\\\x00") {
			return "", false, fmt.Errorf("BUNDLE_GEMFILE must resolve to a repository-local path: %s", value)
		}
		if workspacePath {
			value, err = filepath.Rel(directory, target)
			if err != nil {
				return "", false, err
			}
			value = filepath.ToSlash(value)
		}
		return value, true, nil
	case "RUBYOPT":
		// Local validation owns the tracer preload; preserve companion Ruby
		// options. The installer's exported preload replaced the job's options,
		// so options restored after it are the step's own.
		if installed := strings.Replace(value, rubyInstallerPreload, "", 1); installed != value {
			return strings.TrimSpace(installed), true, nil
		}
		inherited := strings.NewReplacer("${{ env.RUBYOPT }}", "", "${{env.RUBYOPT}}", "").Replace(value)
		if inherited != value && (job.Env["RUBYOPT"] != "" || workflow.Env["RUBYOPT"] != "") {
			return "", false, fmt.Errorf("RUBYOPT extends workflow or job Ruby options; validate with those options explicitly")
		}
		value = inherited
		return strings.TrimSpace(value), true, nil
	}
	return value, false, nil
}

func (rubyDiscovery) skipsUnsupportedNode() bool { return false }

func (rubyDiscovery) instructions(framework string) string {
	return withMinitestForkSetup(strings.ReplaceAll(rubyInstructions, "__FRAMEWORK__", framework), framework)
}

// withMinitestForkSetup inserts the forked-test transport setup after the
// heading of Minitest instructions.
func withMinitestForkSetup(text, framework string) string {
	if framework != "minitest" {
		return text
	}
	heading, rest, _ := strings.Cut(text, "\n\n")
	forkSetup := strings.ReplaceAll(rubyMinitestForkInstructions, "__FORK_SETUP__", strings.TrimSpace(RubyMinitestForkSetup))
	return heading + "\n\n" + forkSetup + "\n\n" + rest
}

func (rubyDiscovery) onboardingScope(string) (discover, instructionsWhenConfigured bool) {
	return true, true
}

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."

func (rubyDiscovery) ciSetupRequest(root, framework, site string) ciSetupRequest {
	request := ciSetupRequest{Language: "ruby", Site: site, TracerInput: "ruby-tracer-version", TracerVersion: "<exact datadog-ci release>"}
	request.Notes = []string{
		"Replace <exact datadog-ci release> with an exact datadog-ci release compatible with every instrumented Ruby, such as 1.42.0; when the bundle already locks datadog-ci, use that release. Keep the existing Gemfile, lockfile, Ruby images, bundle exec/binstub/Rake/Rails entry points and test arguments: the script adds datadog-ci to the CI bundle itself, so do not edit the Gemfile. In a mixed RSpec/Minitest repository, scope this change to " + framework + ".",
		"The script replaces RUBYOPT with -rbundler/setup -rdatadog/ci/auto_instrument. When the job sets RUBYOPT, re-add those options right after the entry with export RUBYOPT=\"$RUBYOPT <existing options>\"; never replace RUBYOPT after it. Set BUNDLE_GEMFILE in the job's variables (one per matrix entry where the matrix selects it), so datadog-ci joins the bundle the tests use.",
		rubyGemsNote(root, framework),
		"If a job or matrix entry runs a Ruby the tracer cannot instrument (head, JRuby, TruffleRuby), keep its tests running uninstrumented in a job without the entry; ddtest reports such jobs as excluded. To split a shared matrix, exclude those entries from the instrumented job with rules (when: never on their matrix variables) and run them in that separate job. Report each exclusion and its reason; do not drop entries, guard the tests or change Ruby versions to obtain a pass. At least one compatible instrumented job is required.",
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore)" + rubyGemPackaging(root),
	}
	if framework == "minitest" {
		request.Notes = append(request.Notes, rubyShellForkSetup())
	}
	return request
}

// Agents apply Ruby setup by hand on every provider.
func (rubyDiscovery) automaticProviderSetup(string) bool { return false }

// rubyShellForkSetup is the Minitest fork setup for a setup that runs in the
// job's shell: the initializer extends the exported RUBYOPT after it.
func rubyShellForkSetup() string {
	text := strings.ReplaceAll(rubyMinitestForkInstructions, "__FORK_SETUP__", strings.TrimSpace(RubyMinitestForkSetup))
	return strings.NewReplacer(
		"On the existing test step, after the Datadog action, append the initializer to the action's exported options:", "Right after the Datadog setup entry, append the initializer to the exported options:",
		"env:\n  RUBYOPT: \"${{ env.RUBYOPT }} -r./ci/datadog_test_optimization.rb\"", "- export RUBYOPT=\"$RUBYOPT -r./ci/datadog_test_optimization.rb\"",
		"Adjust the relative path only if the test step has another working directory.", "Adjust the relative path only if the tests run from another directory.",
		"The step's `RUBYOPT` also loads it on matrix entries where the Datadog action is skipped; there it does nothing, because Datadog CI is not loaded.", "In a job without the setup it does nothing, because Datadog CI is not loaded.",
	).Replace(text)
}

// JRuby, TruffleRuby and Ruby head images run an engine datadog-ci does not
// instrument.
func (rubyDiscovery) unsupportedImage(image string) string {
	_, engine := rubyImageRuntime(image)
	return engine
}

// Its own step rules decide which uninstrumented commands are outside scope.
func (rubyDiscovery) outsideFrameworkJob(string, ciWorkflow, runtimeJob, map[string]any, string) bool {
	return false
}

// rubyGemsNote names the CI Ruby images whose RubyGems is older than the
// 3.3.22 the installer requires (Ruby 2.7 and 3.0) and the update they need
// before the setup entry; in a shared matrix script, a guard on the variable
// that selects the image updates only those entries.
func rubyGemsNote(root, framework string) string {
	const generic = "Ruby 2.7 and 3.0 images ship RubyGems older than 3.3.22, which the installer rejects: in those jobs run gem update --system 3.4.22 before the entry. Keep their test and instrumentation entries."
	provider, err := currentCIProvider(root)
	if err != nil {
		return generic
	}
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return generic
	}
	images, guards := []string{}, []string{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			version, _ := rubyImageRuntime(job.Image)
			if rubyImageGemsProblem(version, "", provider) == "" || disabledCondition(job.If) {
				continue
			}
			runs := false
			for _, step := range job.Steps {
				runs = runs || resolveRubyStep(root, workflow, job, step, framework).Matched
			}
			if !runs || slices.Contains(images, job.Image) {
				continue
			}
			images = append(images, job.Image)
			// Compare the image as written with the entry's image, which also
			// works when the variable is only the tag (ruby:${RUBY_VERSION}).
			if rubyImageVariable.MatchString(job.ImageTemplate) {
				guards = append(guards, `- if [ "`+job.ImageTemplate+`" = "`+job.Image+`" ]; then gem update --system 3.4.22; fi`)
			}
		}
	}
	if len(images) == 0 {
		return generic
	}
	note := "These CI jobs run " + strings.Join(images, ", ") + ", whose RubyGems is older than the 3.3.22 the installer requires: run gem update --system 3.4.22 before the setup entry in them, and keep their test and instrumentation entries."
	if len(guards) > 0 {
		note += " Where a parallel:matrix shares the script, add this entry before the setup entry instead of splitting the job; ddtest follows the guard per matrix entry:\n" + strings.Join(guards, "\n")
	}
	return note
}

// rubyImageVariable is the variable an image is selected by, as in
// image: $RUBY_IMAGE or ruby:${RUBY_VERSION}.
var rubyImageVariable = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// rubyGemPackaging completes the report exclusion for the repository's gem
// packaging. A gemspec listing its files with git ls-files already leaves out
// what .gitignore excludes, so it needs no edit; one listing globs needs the
// exclusion in its files.
func rubyGemPackaging(root string) string {
	specs, _ := filepath.Glob(filepath.Join(root, "*.gemspec"))
	if len(specs) == 0 {
		return " and from gem packaging."
	}
	for _, spec := range specs {
		data, err := os.ReadFile(spec)
		if err != nil || !strings.Contains(string(data), "git ls-files") {
			return " and from gem packaging: the gemspec lists its files with globs, so exclude .testoptimization/ from its files."
		}
	}
	return ". The gemspec lists its files with git ls-files, so that .gitignore entry also keeps the reports out of the gem: do not edit the gemspec."
}

func (rubyDiscovery) runtimeVariable(name string) bool {
	return slices.Contains([]string{"RUBYOPT", "RUBYLIB", "GEM_HOME", "GEM_PATH"}, name) || strings.HasPrefix(name, "BUNDLE_")
}

func (rubyDiscovery) cacheVariable(name string) bool {
	return slices.Contains([]string{"BUNDLE_PATH", "GEM_HOME"}, name)
}
