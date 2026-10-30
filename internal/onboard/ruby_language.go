// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	_ "embed"
	"fmt"
	"maps"
	"os"
	"path"
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

// Ruby setup uses the Datadog GitHub Actions setup action, the Datadog
// GitLab setup script, the Datadog CircleCI orb, on Buildkite the Datadog
// installer, or the Datadog Jenkins plugin.
func (rubyDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider || provider == gitLabCIProvider || provider == circleCIProvider || provider == buildkiteProvider || provider == jenkinsProvider
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

func (rubyDiscovery) keepsCISetting(string) bool { return false }

func (rubyDiscovery) jobConsumer(string, ciWorkflow, runtimeJob, string) (bool, bool) {
	return false, false
}

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."

func (rubyDiscovery) ciSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	if terms.Plugin {
		return rubyPluginSetupRequest(root, framework, site, terms)
	}
	request := ciSetupRequest{Language: "ruby", Site: site, TracerInput: "ruby-tracer-version", TracerVersion: "<exact datadog-ci release>"}
	request.Notes = []string{
		"Replace <exact datadog-ci release> with an exact datadog-ci release compatible with every instrumented Ruby, such as 1.42.0; when the bundle already locks datadog-ci, use that release. Keep the existing Gemfile, lockfile, Ruby images, bundle exec/binstub/Rake/Rails entry points and test arguments: the " + terms.Installer + " adds datadog-ci to the CI bundle itself, so do not edit the Gemfile. In a mixed RSpec/Minitest repository, scope this change to " + framework + ".",
		"The " + terms.Installer + " replaces RUBYOPT with -rbundler/setup -rdatadog/ci/auto_instrument. When the job sets RUBYOPT, re-add those options right after " + terms.Entry + " with " + terms.Persist(`export RUBYOPT="$RUBYOPT <existing options>"`) + "; never replace RUBYOPT after it. Set BUNDLE_GEMFILE in " + terms.Variables + " (one per matrix entry where the matrix selects it), so datadog-ci joins the bundle the tests use.",
		rubyGemsNote(root, framework, terms),
		rubyRakeOptionsNote(root),
		setupImageNote(root, "ruby", framework),
		"If a job or matrix entry runs a Ruby the tracer cannot instrument (head, JRuby, TruffleRuby), keep its tests running uninstrumented in a job without " + terms.Entry + "; ddtest reports such jobs as excluded. To split a shared matrix, exclude those entries from the instrumented job " + terms.MatrixExclusion + " and run them in that separate job. Report each exclusion and its reason; do not drop entries, guard the tests or change Ruby versions to obtain a pass. At least one compatible instrumented job is required.",
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore)" + rubyGemPackaging(root),
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	if framework == "minitest" {
		request.Notes = append(request.Notes, rubyShellForkSetup(terms))
	}
	return request
}

// rubyPluginSetupRequest is the Ruby part of a CI server plugin's setup: the
// plugin uses the datadog-ci the root bundle pins, so the bundle pins it.
func rubyPluginSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "ruby", Site: site}
	pin := "Pin datadog-ci in the bundle: add gem \"datadog-ci\", \"<exact datadog-ci release>\", group: :test to the Gemfile at the checkout root (in a group the tests load; keep every other line) and run bundle lock, so Gemfile.lock pins it, then commit both. Use an exact release compatible with every instrumented Ruby, such as 1.42.0; when Gemfile.lock already locks datadog-ci, keep that release. The " + terms.Installer + " uses a datadog-ci the bundle already has; without one it runs bundle config set frozen false and bundle add datadog-ci on the agent, installing the latest release, which ddtest cannot validate."
	if locked := rubyLockedTracer(root); locked != "" {
		pin = "The Gemfile.lock at the checkout root already pins datadog-ci " + locked + ", which the " + terms.Installer + " uses as is and ddtest validates; keep it pinned, and keep the Gemfile and Gemfile.lock unchanged."
	}
	request.Notes = []string{
		pin,
		"The " + terms.Installer + " configures datadog-ci from the checkout root with the build agent's default ruby and bundle, so keep the Gemfile at the checkout root and run the " + framework + " tests from there: not inside dir(…), and without BUNDLE_GEMFILE. Keep the existing bundle exec/binstub/Rake/Rails entry points and test arguments.",
		"The " + terms.Installer + " replaces RUBYOPT with -rbundler/setup -rdatadog/ci/auto_instrument for " + terms.Later + ". When a stage sets RUBYOPT, re-add its options with " + terms.Persist(`export RUBYOPT="$RUBYOPT <existing options>"`) + "; never replace RUBYOPT there.",
		"The " + terms.Installer + " refuses Ruby older than 2.7 and RubyGems older than 3.3.22: on agents with Ruby 2.7 or 3.0, run gem update --system 3.4.22 on the agent (or in an earlier step) before the tests.",
		rubyRakeOptionsNote(root),
		setupUnsupportedNote(root, "ruby", framework),
		"If a stage runs a Ruby the tracer cannot instrument (head, JRuby, TruffleRuby), tell the user: the " + terms.Installer + " instruments every stage of the pipeline, so it cannot leave that stage out. Report it; do not drop stages or change Ruby versions to obtain a pass.",
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore)" + rubyGemPackaging(root),
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	if framework == "minitest" {
		request.Notes = append(request.Notes, rubyShellForkSetup(terms))
	}
	return request
}

// Agents apply Ruby setup by hand on every provider.
func (rubyDiscovery) automaticProviderSetup(string) bool { return false }

// rubyShellForkSetup is the Minitest fork setup for a setup that runs in the
// job's shell: the initializer extends the exported RUBYOPT after it.
func rubyShellForkSetup(terms ciSetupTerms) string {
	text := strings.ReplaceAll(rubyMinitestForkInstructions, "__FORK_SETUP__", strings.TrimSpace(RubyMinitestForkSetup))
	return strings.NewReplacer(
		"On the existing test step, after the Datadog action, append the initializer to the action's exported options:", "Right after "+datadogSetupEntry(terms)+", append the initializer to the exported options:",
		"env:\n  RUBYOPT: \"${{ env.RUBYOPT }} -r./ci/datadog_test_optimization.rb\"", terms.PersistEntry(`export RUBYOPT="$RUBYOPT -r./ci/datadog_test_optimization.rb"`),
		"Adjust the relative path only if the test step has another working directory.", "Adjust the relative path only if the tests run from another directory.",
		"The step's `RUBYOPT` also loads it on matrix entries where the Datadog action is skipped; there it does nothing, because Datadog CI is not loaded.", "In a job without the setup it does nothing, because Datadog CI is not loaded.",
	).Replace(text)
}

// datadogSetupEntry names the setup's place in a job with Datadog's name,
// such as "the Datadog setup entry".
func datadogSetupEntry(terms ciSetupTerms) string {
	if strings.Contains(terms.SetupEntry, "Datadog") {
		return terms.SetupEntry
	}
	return "the Datadog " + strings.TrimPrefix(terms.SetupEntry, "the ")
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
func rubyGemsNote(root, framework string, terms ciSetupTerms) string {
	generic := "Ruby 2.7 and 3.0 images ship RubyGems older than 3.3.22, which the installer rejects: in those jobs run gem update --system 3.4.22 before " + terms.Entry + ". Keep their test and instrumentation entries."
	provider, err := currentCIProvider(root)
	if err != nil {
		return generic
	}
	workflows, err := readProviderWorkflows(provider, root)
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
			if guard := terms.ImageGuard(job.ImageTemplate, job.Image, "gem update --system 3.4.22"); guard != "" {
				guards = append(guards, guard)
			}
		}
	}
	if len(images) == 0 {
		return generic
	}
	note := "These CI jobs run " + strings.Join(images, ", ") + ", whose RubyGems is older than the 3.3.22 the installer requires: run gem update --system 3.4.22 before " + terms.SetupEntry + " in them, and keep their test and instrumentation entries."
	if len(guards) > 0 {
		note += " Where " + terms.SharedMatrix + ", add this entry before " + terms.SetupEntry + " instead of splitting the job; ddtest follows the guard per matrix entry:\n" + strings.Join(guards, "\n")
	}
	return note
}

// rubyRakeOptionsNote names the CI test commands that pass RUBYOPT as a rake
// argument (rake RUBYOPT='-W0'), which replaces the preload for the tests rake
// runs, and prescribes the inline form that keeps it; empty when none does.
func rubyRakeOptionsNote(root string) string {
	provider, err := currentCIProvider(root)
	if err != nil {
		return ""
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return ""
	}
	var locations, forms []string
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			for _, step := range workflow.Jobs[name].Steps {
				options, found := rubyRakeOptions(step.Run)
				if !found {
					continue
				}
				if location := workflow.Path + " / " + name; !slices.Contains(locations, location) {
					locations = append(locations, location)
				}
				if form := `RUBYOPT="$RUBYOPT ` + options + `"`; !slices.Contains(forms, form) {
					forms = append(forms, form)
				}
			}
		}
	}
	if len(locations) == 0 {
		return ""
	}
	return "These CI test commands pass RUBYOPT as a rake argument, which replaces the preload for the tests rake runs: " + strings.Join(locations, ", ") + ". Write that argument as RUBYOPT=\"$RUBYOPT <its options>\", here " + strings.Join(forms, "; ") + ": it keeps the preload, so that command needs no other RUBYOPT change."
}

// rubyGemPackaging completes the report exclusion for the repository's gem
// packaging. A gemspec listing its files with git ls-files already leaves out
// what .gitignore excludes, so it needs no edit; one listing globs needs the
// exclusion in its files.
func rubyGemPackaging(root string) string {
	specs, _ := filepath.Glob(filepath.Join(root, "*.gemspec"))
	if len(specs) == 0 {
		return " and from gem packaging."
	}
	allowlists := 0
	for _, spec := range specs {
		data, err := os.ReadFile(spec)
		if err == nil && rubyGemspecAllowlist(string(data)) {
			allowlists++
			continue
		}
		if err != nil || !strings.Contains(string(data), "git ls-files") {
			return " and from gem packaging: the gemspec lists its files with globs, so exclude .testoptimization/ from its files."
		}
	}
	if allowlists == len(specs) {
		return ". The gemspec lists its files from specific paths that cannot include .testoptimization/, so the reports stay out of the gem: do not edit the gemspec."
	}
	return ". The gemspec lists its files with git ls-files, so that .gitignore entry also keeps the reports out of the gem: do not edit the gemspec."
}

var (
	// rubyGemspecFiles is a one-line files assignment.
	rubyGemspecFiles = regexp.MustCompile(`(?m)^\s*\w+\.files\s*=\s*(.+?)\s*$`)
	// rubyGemspecAllowlistTerms are the terms of an allowlist: Dir globs,
	// %w lists and strings, joined by +.
	rubyGemspecAllowlistTerms = regexp.MustCompile(`^(?:Dir(?:\.glob)?\s*[\[(]\s*(?:"[^"]*"|'[^']*')(?:\s*,\s*(?:"[^"]*"|'[^']*'))*\s*[\])]|%w[\[(][^\])]*[\])]|"[^"]*"|'[^']*')(?:\s*\+\s*(?:Dir(?:\.glob)?\s*[\[(]\s*(?:"[^"]*"|'[^']*')(?:\s*,\s*(?:"[^"]*"|'[^']*'))*\s*[\])]|%w[\[(][^\])]*[\])]|"[^"]*"|'[^']*'))*$`)
	rubyGemspecPattern        = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
)

// rubyGemspecAllowlist reports a gemspec whose files are only globs and names
// under specific top-level paths, as in Dir.glob("{bin,lib,exe}/**/*") +
// %w[README.md Gemfile]: none can match .testoptimization/.
func rubyGemspecAllowlist(gemspec string) bool {
	matches := rubyGemspecFiles.FindAllStringSubmatch(gemspec, -1)
	if len(matches) != 1 || !rubyGemspecAllowlistTerms.MatchString(matches[0][1]) {
		return false
	}
	expression := matches[0][1]
	var paths []string
	for _, quoted := range rubyGemspecPattern.FindAllStringSubmatch(expression, -1) {
		paths = append(paths, quoted[1]+quoted[2])
	}
	for _, list := range regexp.MustCompile(`%w[\[(]([^\])]*)[\])]`).FindAllStringSubmatch(expression, -1) {
		paths = append(paths, strings.Fields(list[1])...)
	}
	for _, pattern := range paths {
		first, _, _ := strings.Cut(strings.TrimPrefix(pattern, "./"), "/")
		names := []string{first}
		if strings.HasPrefix(first, "{") && strings.HasSuffix(first, "}") {
			names = strings.Split(strings.Trim(first, "{}"), ",")
		}
		for _, name := range names {
			if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "*?[]{}") {
				return false
			}
		}
	}
	return len(paths) > 0
}

func (rubyDiscovery) runtimeVariable(name string) bool {
	return slices.Contains([]string{"RUBYOPT", "RUBYLIB", "GEM_HOME", "GEM_PATH"}, name) || strings.HasPrefix(name, "BUNDLE_")
}

func (rubyDiscovery) cacheVariable(name string) bool {
	return slices.Contains([]string{"BUNDLE_PATH", "GEM_HOME"}, name)
}

// A stated Ruby is compared like a ruby:<version> image tag's, so it needs a
// minor version.
var rubyStatedRuntime = &statedRuntime{
	Names:   []string{"ruby"},
	Display: "Ruby",
	Version: rubyImageVersion,
	Example: "3.3 or 3.3.6",
	base:    rubyImageSelection,
	configured: func(root string, workflow ciWorkflow, job runtimeJob) (string, string) {
		rows, err := runtimeMatrix(job.Strategy.Matrix)
		return installedRuntime(rows, err, func(row map[string]any) (string, string, bool) {
			if active, err := rubyJobActive(job, row); err == nil && !active {
				return "", "", false
			}
			// The tests run on the last selection before them.
			version, source := "", ""
			for _, step := range stepsThroughFirstTest(job.Steps, func(step runtimeStep) bool { return rubyTestStep(root, workflow, job, step, row) }) {
				// A step that may not run selects nothing for certain.
				if active, err := rubyStepActive(workflow, job, step, row); err != nil || !active {
					continue
				}
				if selected := step.Runtime["ruby"]; selected != "" {
					version, source = selected, step.Source
				} else if uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@"); uses == "ruby/setup-ruby" {
					if selected, err := runtimeValue(step.With["ruby-version"], row); err == nil && selected != "" {
						version, source = selected, "ruby/setup-ruby"
					}
				}
			}
			return version, source, true
		})
	},
	check: func(_ context.Context, root, framework string) RuntimeCheck {
		return RubyCIConfiguration(root, framework)
	},
	runtime: func(finding RuntimeFinding) string { return finding.Runtime },
}

func (rubyDiscovery) statedRuntime() *statedRuntime { return rubyStatedRuntime }

func (rubyDiscovery) ciPluginLanguage() string { return "RUBY" }

// The Datadog Jenkins plugin replaces RUBYOPT with the datadog-ci preload,
// as the installer does.
// The Datadog Jenkins plugin replaces RUBYOPT.
func (rubyDiscovery) ciPluginComposes(string) string { return "" }

// Ruby version managers: rbenv, rvm, chruby, and sourcing the scripts that
// load RVM or chruby.
func (rubyDiscovery) selectsShellRuntime(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch name := path.Base(words[0]); name {
	case "rbenv", "rvm", "chruby":
		return true
	case "source", ".":
		return len(words) > 1 && slices.Contains([]string{"rvm", "rvm.sh", "chruby.sh", "auto.sh"}, path.Base(words[1]))
	}
	return false
}

func (rubyDiscovery) ciPluginExports(map[string]string) (map[string]string, map[string]string) {
	return map[string]string{"RUBYOPT": rubyInstallerPreload}, nil
}

// rubyTestStep reports whether step is a test step of any Ruby framework on a
// matrix row, as RubyCIConfiguration identifies them: a statement is checked
// before the framework is known. A step a condition skips on the row is not
// one.
func rubyTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) bool {
	if active, err := rubyStepActive(workflow, job, step, row); err == nil && !active {
		return false
	}
	for _, framework := range (rubyDiscovery{}).frameworks() {
		resolved, _, _ := resolveVariantStep(workflow, job, step, row, framework)
		resolution := resolveRubyStep(root, workflow, job, resolved, framework)
		if !resolution.Review && (resolution.Matched || resolution.Reason != "") {
			return true
		}
	}
	return false
}

// rubyImageSelection is the Ruby version or engine a job's image selects, or
// a Ruby image's floating tag (latest, current, or none): that image's runtime
// is not the user's to state.
func rubyImageSelection(job runtimeJob) string {
	if version, engine := rubyImageRuntime(job.Image); version+engine != "" {
		return version + engine
	}
	if match := rubyImage.FindStringSubmatch(withImplicitTag(job.Image)); match != nil {
		return match[2]
	}
	return ""
}
