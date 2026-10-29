package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
)

// Ruby tasks can execute arbitrary code. Replay only a single explicit entry
// point; retain ambiguous shell setup for review instead of guessing its scope.
func resolveRubyStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) commandResolution {
	if step.ResolutionError != "" {
		return unresolvedCommand(step.ResolutionError)
	}
	if strings.TrimSpace(step.Run) == "" {
		return commandResolution{}
	}
	directory := filepath.Clean(stepDirectory(workflow, job, step))
	if !filepath.IsLocal(directory) || strings.ContainsAny(directory, "$`~") {
		return unresolvedCommand("Ruby working-directory needs explicit review: " + directory)
	}
	if rubyMaintenanceStep(step.Run) {
		return commandResolution{Review: true, Reason: "Documentation, lint or release tooling is maintenance, outside " + framework + " test scope."}
	}
	parts, parseErr := staticCommands(step.Run)
	matched := environment.LooksLikeTestCommand(filepath.Join(root, directory), step.Run, "ruby", framework)
	if parseErr == nil && len(parts) == 1 {
		words := parts[0]
		if len(words) > 2 && filepath.Base(words[0]) == "bundle" && words[1] == "exec" {
			words = words[2:]
		}
		if len(words) > 0 {
			name := filepath.Base(words[0])
			if name == "rspec" && framework != "rspec" || name == "rails" && framework != "minitest" {
				return commandResolution{Review: true, Reason: "Explicit command belongs to the other Ruby framework; validate it separately."}
			}
			if name == "ruby" && framework == "minitest" {
				for _, word := range words[1:] {
					matched = matched || strings.Contains(word, "_test.rb") || strings.Contains(word, "minitest")
				}
			}
		}
		if len(words) > 0 && filepath.Base(words[0]) == "rake" {
			if !environment.LooksLikeTestCommand(filepath.Join(root, directory), step.Run, "ruby", framework) {
				return commandResolution{Review: true, Reason: "Ruby maintenance task is outside test validation."}
			}
			if resolution, done := resolveRubyRakeTargets(root, directory, step.Run, parts[0], words, framework); done {
				return resolution
			}
		}
	}
	if !matched {
		if strings.Contains(step.Run, "test") || strings.Contains(step.Run, "spec") {
			if parseErr == nil && rubyOutsideUninstrumentedJob(root, workflow, job, parts, framework) {
				return commandResolution{Review: true, Code: CodeUninstrumentedJob, Reason: "Uninstrumented CI job with no recognized " + framework + " step: this command is outside validation and its tests, if any, are not reported to Datadog. If it runs " + framework + " tests, add the Datadog action to this job and rerun validation: " + step.Run}
			}
			return unresolvedCommand("Ruby test wrapper was not resolved; ddtest cannot tell which tests it runs: " + step.Run)
		}
		// An expression such as bundle exec ${{ matrix.command }} may select the
		// tests, so it stays in the required scope.
		if workflowExpression.MatchString(step.Run) {
			return unresolvedCommand("Ruby command expression was not resolved; ddtest cannot tell which tests it runs: " + step.Run)
		}
		// Dynamic shell syntax hides what a repository script runs or which
		// bundle and Ruby options later steps inherit, so keep it visible.
		if parseErr != nil && (strings.Contains(step.Run, "./") || strings.HasPrefix(strings.TrimSpace(step.Run), "bin/") || strings.Contains(step.Run, "BUNDLE_GEMFILE") || strings.Contains(step.Run, "RUBYOPT") || strings.Contains(step.Run, "bundle exec")) {
			return unresolvedCommand("Repository script or Ruby setup could not be resolved statically; ddtest cannot tell which tests it runs: " + step.Run)
		}
		if parseErr == nil && slices.ContainsFunc(parts, rubyRepositoryScript) {
			return unresolvedCommand("Repository script may run " + framework + " tests; ddtest cannot tell which ones: " + step.Run)
		}
		if strings.Contains(step.Run, "bundle exec") || strings.Contains(step.Run, "bin/") {
			return commandResolution{Review: true, Reason: "Ruby task or wrapper was not identified as " + framework + "; inspect it separately if it runs tests."}
		}
		return commandResolution{}
	}
	parts, err := staticCommands(step.Run)
	if err != nil || len(parts) != 1 || len(parts[0]) == 0 || strings.Contains(parts[0][0], "=") {
		return unresolvedCommand("Ruby test entry point requires a single explicit command; ddtest cannot replay its shell setup: " + step.Run)
	}
	return commandResolution{Matched: true, Tests: []TestCommand{{Directory: directory, Command: shellquote.Join(parts[0]...)}}}
}

// rubyMaintenanceTools generate documentation, lint or release a gem; they
// never run tests.
var rubyMaintenanceTools = []string{"rdoc", "ri", "yard", "yardoc", "rubocop", "standardrb", "rufo", "reek", "brakeman", "bundle-audit", "bundler-audit", "erb_lint", "erblint"}

// rubyReleaseSubcommands build or publish a gem.
var rubyReleaseSubcommands = map[string][]string{"gem": {"build", "push", "yank", "owner", "signin", "cert"}}

// rubyDependencySubcommands only install or inspect gems around maintenance.
// Alone they are ordinary setup, not maintenance.
var rubyDependencySubcommands = map[string][]string{
	"gem":    {"install", "unpack", "fetch", "uninstall", "update", "list", "specification"},
	"bundle": {"install", "config", "update", "lock", "package", "cache", "check", "binstubs", "clean", "outdated"},
}

// rubyShellBuiltins only move, print or inspect files around maintenance.
var rubyShellBuiltins = []string{"cd", "cp", "mv", "mkdir", "rm", "ln", "echo", "printf", "cat", "ls", "touch", "tar", "pwd", "true"}

// rubyMaintenanceStep reports whether every command of a step is
// documentation, lint or release tooling, or file handling around it, with at
// least one such tool. It never runs Rake, Ruby, a test runner or a
// repository script, so it is maintenance, not test scope.
func rubyMaintenanceStep(run string) bool {
	file, err := syntax.NewParser().Parse(strings.NewReader(run), "")
	if err != nil {
		return false
	}
	tools := 0
	// Only plain commands joined by newlines, ";" or "&&" are classified.
	// Background jobs, pipes, "||", subshells, groups, redirections and
	// substitutions can hide or start another command, as in
	// "rubocop & rspec spec/integration".
	var visit func(stmt *syntax.Stmt) bool
	visit = func(stmt *syntax.Stmt) bool {
		if stmt.Background || stmt.Coprocess || stmt.Negated || len(stmt.Redirs) > 0 {
			return false
		}
		switch command := stmt.Cmd.(type) {
		case *syntax.BinaryCmd:
			return command.Op == syntax.AndStmt && visit(command.X) && visit(command.Y)
		case *syntax.CallExpr:
			if len(command.Assigns) > 0 || len(command.Args) == 0 {
				return false
			}
			var words []string
			for _, word := range command.Args {
				text, static := rubyStaticWord(word)
				if !static {
					return false
				}
				words = append(words, text)
			}
			if len(words) > 2 && words[0] == "bundle" && words[1] == "exec" {
				words = words[2:]
			}
			if strings.ContainsAny(words[0], "$=/") {
				return false
			}
			switch {
			case slices.Contains(rubyMaintenanceTools, words[0]):
				tools++
			case len(words) > 1 && slices.Contains(rubyReleaseSubcommands[words[0]], words[1]):
				tools++
			case len(words) > 1 && slices.Contains(rubyDependencySubcommands[words[0]], words[1]):
			case slices.Contains(rubyShellBuiltins, words[0]):
			default:
				return false
			}
			return true
		}
		return false
	}
	for _, stmt := range file.Stmts {
		if !visit(stmt) {
			return false
		}
	}
	return tools > 0
}

// rubyStaticWord returns a word made only of literal and quoted literal text.
func rubyStaticWord(word *syntax.Word) (string, bool) {
	var text strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			text.WriteString(part.Value)
		case *syntax.SglQuoted:
			text.WriteString(part.Value)
		case *syntax.DblQuoted:
			for _, inner := range part.Parts {
				literal, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				text.WriteString(literal.Value)
			}
		default:
			return "", false
		}
	}
	return text.String(), true
}

// rubyOutsideUninstrumentedJob reports whether a command that only mentions
// tests, with no framework, Rake, Ruby or repository entry point, runs in a
// CI job that has no Datadog action and no step recognized as framework's.
// Such a job was not set up for the framework, so its command is reported as
// outside validation instead of blocking it. Repository scripts, Rake and
// expressions stay in scope: they may well run the framework.
func rubyOutsideUninstrumentedJob(root string, workflow ciWorkflow, job runtimeJob, parts [][]string, framework string) bool {
	if len(job.Steps) == 0 || slices.ContainsFunc(parts, rubyRepositoryScript) {
		return false
	}
	for _, words := range parts {
		if len(words) > 2 && filepath.Base(words[0]) == "bundle" && words[1] == "exec" {
			words = words[2:]
		}
		if len(words) == 0 || slices.Contains([]string{"rake", "ruby", "rspec", "rails", "minitest", "m", "testrb"}, filepath.Base(words[0])) || strings.ContainsAny(words[0], "$/") {
			return false
		}
	}
	// Siblings resolve without job context, so this check cannot recurse.
	sibling := job
	sibling.Steps = nil
	for _, step := range job.Steps {
		if _, ok := datadogSetup(step); ok {
			return false
		}
		if step.Run != "" && resolveRubyStep(root, workflow, sibling, step, framework).Matched {
			return false
		}
	}
	return true
}

// rubyMaintenanceBinstubs are repository binstubs that never run tests.
var rubyMaintenanceBinstubs = []string{"rubocop", "standardrb", "brakeman", "bundle-audit", "bundler-audit", "erb_lint", "erblint", "setup", "dev", "yarn", "webpack", "importmap"}

// A repository script such as ./ci/run, bin/ci or ruby ci/run.rb may wrap the
// tests, so it stays in the required scope instead of disappearing from it.
func rubyRepositoryScript(words []string) bool {
	if len(words) > 2 && filepath.Base(words[0]) == "bundle" && words[1] == "exec" {
		words = words[2:]
	}
	// An interpreter's script argument is a repository file even without a
	// directory, as in bash ci.sh.
	interpreter := len(words) > 1 && slices.Contains([]string{"sh", "bash", "ruby"}, words[0]) && !strings.HasPrefix(words[1], "-")
	if interpreter {
		words = words[1:]
	}
	if len(words) == 0 || !interpreter && !strings.Contains(words[0], "/") || filepath.IsAbs(words[0]) || strings.HasPrefix(words[0], "~") {
		return false
	}
	return !slices.Contains(rubyMaintenanceBinstubs, strings.TrimSuffix(filepath.Base(words[0]), ".rb"))
}

// resolveRubyRakeTargets resolves the Rake targets one step runs: its explicit
// targets, or for a bare rake the Rakefile's literal default task. done is
// false when the step is an ordinary test command for the caller to resolve.
//
// When the targets include the framework's own task (spec for RSpec, test for
// Minitest) next to other targets, local validation runs only the framework's
// task, bound to this CI step, and the other targets are reported: they run in
// the same instrumented step without being validated.
func resolveRubyRakeTargets(root, directory, run string, command, words []string, framework string) (commandResolution, bool) {
	task, other := "spec", "test"
	if framework == "minitest" {
		task, other = "test", "spec"
	}
	targets, flags, rakefile := rubyRakeArguments(words[1:])
	explicit := len(targets) > 0
	if !explicit && (rakefile == "" || rakefile == "Rakefile") {
		targets = rubyDefaultTargets(root, directory)
	}
	ownTask := func(word string) bool { return word == task || strings.HasPrefix(word, task+":") }
	otherTask := func(word string) bool { return word == other || strings.HasPrefix(word, other+":") }
	own := slices.DeleteFunc(slices.Clone(targets), func(word string) bool { return !ownTask(word) })
	if len(own) == 0 {
		if slices.ContainsFunc(targets, otherTask) {
			return commandResolution{Review: true, Reason: "Rake " + other + " task belongs to the other Ruby framework; validate it separately."}, true
		}
		if !explicit {
			return unresolvedCommand("Rake default task could not be read statically or does not name the " + task + " task; ddtest cannot tell which " + framework + " tests this step runs."), true
		}
		return unresolvedCommand("Custom Rake task does not name the " + task + " task; ddtest cannot tell which " + framework + " tests this step runs."), true
	}
	others := slices.DeleteFunc(slices.Clone(targets), ownTask)
	if len(others) == 0 {
		// The step runs only the framework's tasks; execute it unchanged.
		return commandResolution{}, false
	}
	// Keep options unresolved instead of guessing how they affect a narrower
	// invocation.
	if flags {
		return unresolvedCommand("Rake invocation combines the " + task + " task and other targets with options ddtest cannot separate: " + shellquote.Join(words...)), true
	}
	selected := append(slices.Clone(command[:len(command)-len(words)+1]), own...)
	otherFramework := ""
	if slices.ContainsFunc(others, otherTask) {
		otherFramework = " (including the other Ruby framework's " + other + " task)"
	}
	note := "This CI step also runs Rake task(s) " + strings.Join(others, ", ") + otherFramework + " in the same process. Local validation covers only " + shellquote.Join(selected...) + "; instrumenting this step also reports those tasks' tests to Datadog, and they are not validated. Report this to the user; running " + framework + " in its own step keeps the other tasks uninstrumented."
	return commandResolution{Matched: true, ReviewReason: note, Tests: []TestCommand{{Directory: directory, Command: shellquote.Join(selected...), StepScopeNotes: []string{note}}}}, true
}

// rubyRakeOperandOptions take the next word as their value, which is never a
// task: rake -f Rakefile spec runs only spec.
var rubyRakeOperandOptions = []string{"-f", "--rakefile", "-I", "--libdir", "-r", "--require", "-R", "--rakelibdir", "-e", "--execute", "-p", "--execute-print", "-E", "--execute-continue"}

// rubyRakeArguments separates Rake's task targets from its options, their
// values and VAR=value arguments, which apply to the whole invocation. It
// also returns the selected Rakefile, when one is named.
func rubyRakeArguments(arguments []string) (targets []string, flags bool, rakefile string) {
	for i := 0; i < len(arguments); i++ {
		word := arguments[i]
		switch {
		case slices.Contains(rubyRakeOperandOptions, word):
			flags = true
			if i+1 < len(arguments) {
				i++
				if word == "-f" || word == "--rakefile" {
					rakefile = arguments[i]
				}
			}
		case strings.HasPrefix(word, "--rakefile="):
			flags, rakefile = true, strings.TrimPrefix(word, "--rakefile=")
		case (word == "-j" || word == "--jobs") && i+1 < len(arguments) && strings.Trim(arguments[i+1], "0123456789") == "":
			flags = true
			i++
		case strings.HasPrefix(word, "-") || strings.Contains(word, "="):
			flags = true
		default:
			targets = append(targets, word)
		}
	}
	return targets, flags, rakefile
}

// rubyDefaultTarget is one literal default task declaration, such as
// task default: :spec, task :default => [:spec, :features] or
// task default: %w[spec features].
var rubyDefaultTarget = regexp.MustCompile(`^task\s+(?::default\s*=>|default:)\s*(.+?)\s*(?:#.*)?$`)

// rubyDefaultTargetList is a literal list of symbols or strings.
var rubyDefaultTargetName = regexp.MustCompile(`^(?::|"|')?([A-Za-z_][A-Za-z0-9_:]*)(?:"|')?$`)

// rubyDefaultTargets reads the Rakefile's literal default task without loading
// Ruby code. Several or computed declarations, or enhancements, are unknown
// (nil). Execution of a single-framework default keeps the bare rake command.
func rubyDefaultTargets(root, directory string) []string {
	path := filepath.Join(root, directory, "Rakefile")
	info, valid := repositoryFile(root, filepath.Dir(path), "Rakefile")
	if !valid || info.Size() > 256*1024 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var targets []string
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "default") {
			continue
		}
		match := rubyDefaultTarget.FindStringSubmatch(line)
		if found || match == nil {
			return nil
		}
		found = true
		value := match[1]
		switch {
		case strings.HasPrefix(value, "%w[") || strings.HasPrefix(value, "%i["):
			if !strings.HasSuffix(value, "]") {
				return nil
			}
			value = strings.Join(strings.Fields(value[3:len(value)-1]), ",")
		case strings.HasPrefix(value, "["):
			if !strings.HasSuffix(value, "]") {
				return nil
			}
			value = value[1 : len(value)-1]
		}
		for _, item := range strings.Split(value, ",") {
			name := rubyDefaultTargetName.FindStringSubmatch(strings.TrimSpace(item))
			if name == nil {
				return nil
			}
			targets = append(targets, name[1])
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

// RubyValidationCommand retains the CI entry point when there is one choice.
func RubyValidationCommand(root, framework, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil
	}
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return "", err
	}
	if len(scope.Commands) > 1 {
		return "", fmt.Errorf("multiple %s CI configurations found; inspect --all --check-only or select one with --command", framework)
	}
	if len(scope.Commands) == 1 {
		entry := scope.Commands[0]
		if entry.Directory != "." || len(entry.Environment) > 0 || len(entry.UnvalidatedSetup) > 0 {
			return "", fmt.Errorf("ruby CI command requires its working-directory/environment/setup; use --all or validate an explicit --command from that prepared environment")
		}
		return entry.Command, nil
	}
	return "", nil
}

// RubyCIConfiguration inventories static Ruby selections. The caller checks
// requirements with RubyGems itself, against the actual installed bundle.
func RubyCIConfiguration(root, framework string) RuntimeCheck {
	return RubyCIConfigurationForSite(root, framework, "")
}

// RubyCIConfigurationForSite is RubyCIConfiguration that also requires each
// instrumented entry's Datadog action to report to the selected site (empty:
// any site). An incomplete action keeps its entry inconclusive.
func RubyCIConfigurationForSite(root, framework, site string) RuntimeCheck {
	result := RuntimeCheck{Status: "not applicable", Reason: "No candidate Ruby CI commands found."}
	provider, err := currentCIProvider(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive", Reason: err.Error()})
				continue
			}
			for _, row := range rows {
				active, conditionErr := possibleJobCondition(job.If, row)
				if conditionErr == nil && !active {
					continue
				}
				ruby, engine := rubyImageRuntime(job.Image)
				imageRuby := ruby != ""
				tracer, action, rubygems, gemsAtSetup := "", "", "", ""
				var setup *ciSetup
				var inputs map[string]string
				skippedAction := false
				var actionErr error
				for i, step := range job.Steps {
					uses, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
					current, isSetup := datadogSetup(step)
					active, stepErr := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
					if stepErr == nil && !active {
						if selected, err := setupSelectsLanguage(current, row, "ruby"); isSetup && current.Pinned && err == nil && selected {
							skippedAction = true
						}
						continue
					}
					if uses == "ruby/setup-ruby" {
						ruby, engine, tracer, action, actionErr, inputs, rubygems, setup = "", "", "", "", nil, nil, "", nil
						imageRuby = false
						skippedAction = false
					}
					if isSetup {
						tracer, action, actionErr, inputs, setup = "", "", nil, nil, nil
					}
					if version := rubyGemsUpdate(step.Run); version != "" {
						rubygems = version
					}
					if isSetup {
						// The installer checks RubyGems when the setup runs; a later
						// update comes too late.
						gemsAtSetup = rubygems
					}
					if conditionErr == nil && stepErr == nil {
						if uses == "ruby/setup-ruby" && hasVersion {
							ruby, _ = runtimeValue(step.With["ruby-version"], row)
							rubygems = step.With["rubygems"]
							// A missing version may come from .ruby-version, but
							// the runner's implicit defaults are not certified here.
						}
						if isSetup && current.Pinned {
							selected, err := setupSelectsLanguage(current, row, "ruby")
							actionErr = err
							if err == nil && selected {
								action = current.Reference
								setup = &current
								tracer, _ = runtimeValue(current.Inputs["ruby-tracer-version"], row)
								inputs = map[string]string{}
								for key, value := range current.Inputs {
									inputs[key], _ = runtimeValue(value, row)
									if inputs[key] == "" && strings.Contains(value, "${{") {
										// A secret or other expression is set even
										// though its value is unknown statically.
										inputs[key] = value
									}
								}
							}
						}
					}
					resolved, _, resolveErr := resolveVariantStep(workflow, job, step, row, framework)
					resolution := resolveRubyStep(root, workflow, job, resolved, framework)
					if resolution.Review {
						result.Review = append(result.Review, RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Status: "not checked", Reason: resolution.Reason, Code: resolution.Code})
						continue
					}
					if !resolution.Matched && resolution.Reason == "" {
						continue
					}
					finding := RuntimeFinding{Location: (commandLocation{Workflow: workflow.Path, Job: name, Matrix: row, Step: stepNumber(step, i), Command: step.Run}).String(), Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Runtime: ruby, Action: action, TracerRequested: tracer, setupInputs: inputs, setupProvider: provider, Status: "inconclusive", Reason: rubyMissingSetupReason(provider)}
					if setup != nil {
						finding.setupSupported, finding.setupReadProblem = setup.Supported, setup.Problem
					}
					if conditionErr != nil || stepErr != nil || resolveErr != nil || actionErr != nil || resolution.Reason != "" {
						reasons := []string{resolution.Reason}
						for _, err := range []error{conditionErr, stepErr, resolveErr, actionErr} {
							if err != nil {
								reasons = append(reasons, err.Error())
							}
						}
						finding.Reason = "Ruby CI scope needs review: " + strings.TrimSpace(strings.Join(reasons, "; "))
					} else if ruby != "" && tracer != "" && action != "" {
						finding.Status, finding.Reason = "not checked", "Check the selected Ruby with the bundled tracer's RubyGems requirements and compare tracer releases."
						if reason := rubyActionProblem(finding, site); reason != "" {
							finding.Status, finding.Code, finding.Reason = "inconclusive", CodeIncompleteCISetup, reason
						} else if reason := rubyGemsResolverProblem(ruby, rubygems, row); !imageRuby && reason != "" {
							finding.Status, finding.Reason = "inconclusive", reason
						} else if reason := rubyImageGemsProblem(ruby, gemsAtSetup, provider); imageRuby && reason != "" {
							finding.Status, finding.Reason = "inconclusive", reason
						}
					} else if action == "" && skippedAction {
						finding.Status, finding.Code = "excluded", "excluded_runtime"
						finding.Reason = "The Datadog action is explicitly skipped for this matrix entry. Its tests still run without instrumentation; this runtime is not validated with the tracer."
					} else if action == "" && engine != "" {
						finding.Status, finding.Code = "excluded", "excluded_runtime"
						finding.Reason = "This job runs " + engine + " (image " + job.Image + "), which the tracer cannot instrument, without the " + provider.setupName() + ". Its tests still run without instrumentation; this runtime is not validated with the tracer."
					}
					// An explicitly uninstrumented row has no preload to lose.
					if reason := rubyBootstrapProblem(workflow, job, step, row, framework); finding.Status != "excluded" && reason != "" {
						finding.Status, finding.Reason = "inconclusive", reason
					}
					result.Jobs = append(result.Jobs, finding)
				}
			}
		}
	}
	if len(result.Jobs) > 0 {
		result.Status, result.Reason = "inconclusive", "Ruby CI runtime and tracer selections need validation against the local bundle."
	}
	return result
}

// rubyGemsResolverPin is the ruby/setup-ruby input that gives every Ruby 2.7
// row a RubyGems able to resolve the tracer's portable gems.
const rubyGemsResolverPin = "rubygems: ${{ startsWith(matrix.ruby, '2.7') && '3.4.22' || 'default' }}"

// rubyGemsResolverProblem explains why an instrumented Ruby 2.7.x row would
// install the tracer with Ruby 2.7's own RubyGems 3.1, which can select a
// native archive requiring Ruby 3.0. Empty when the row's rubygems input
// statically selects RubyGems 3.2 or later, or the row is not Ruby 2.7.
func rubyGemsResolverProblem(ruby, rubygems string, row map[string]any) string {
	if ruby != "2.7" && !strings.HasPrefix(ruby, "2.7.") {
		return ""
	}
	selected := strings.TrimSpace(rubygems)
	if strings.Contains(selected, "${{") {
		parser := conditionParser{rest: strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(selected, "${{"), "}}")), row: row}
		value, err := parser.expression(0)
		text, known := conditionString(value)
		if err != nil || !known || strings.TrimSpace(parser.rest) != "" {
			return "The rubygems input of ruby/setup-ruby could not be resolved for this Ruby " + ruby + " entry; use " + rubyGemsResolverPin + " (with the actual axis name)."
		}
		selected = text
	}
	parts := strings.SplitN(selected, ".", 3)
	if len(parts) >= 2 {
		major, majorErr := strconv.Atoi(parts[0])
		minor, minorErr := strconv.Atoi(parts[1])
		if majorErr == nil && minorErr == nil && (major > 3 || major == 3 && minor >= 2) {
			return ""
		}
	}
	return "Ruby " + ruby + " installs the tracer with RubyGems 3.1, which can select a native gem requiring Ruby 3.0. Set " + rubyGemsResolverPin + " on ruby/setup-ruby (with the actual axis name) so every 2.7.x entry is covered."
}

func rubyBootstrapProblem(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, frameworks ...string) string {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	// The installer replaces RUBYOPT in the job's shell: options the job
	// set before it must be restored after it.
	if options := step.Env["RUBYOPT"]; strings.Contains(options, rubyInstallerPreload) {
		for _, layer := range []map[string]string{workflow.Env, job.Env} {
			for _, option := range strings.Fields(layer["RUBYOPT"]) {
				if !slices.Contains(strings.Fields(options), option) {
					provider := activeOrDefaultCIProvider()
					if provider.sharedSyntax() {
						return "The Datadog setup script replaces RUBYOPT, dropping the job's " + option + "; restore it after the setup entry with export RUBYOPT=\"$RUBYOPT " + layer["RUBYOPT"] + "\"."
					}
					terms := provider.setupTerms()
					return "The " + provider.setupName() + " replaces RUBYOPT, dropping the job's " + option + "; restore it after " + terms.SetupEntry + " with " + terms.Persist(`export RUBYOPT="$RUBYOPT `+layer["RUBYOPT"]+`"`) + "."
				}
			}
		}
	}
	if options, overridden := env["RUBYOPT"]; overridden {
		if !strings.Contains(options, "datadog/ci/auto_instrument") && !strings.Contains(options, "${{ env.RUBYOPT }}") && !strings.Contains(options, "${{env.RUBYOPT}}") {
			if provider := activeOrDefaultCIProvider(); !provider.sharedSyntax() {
				return "The Ruby test environment replaces the RUBYOPT the " + provider.setupName() + " exports, without its Datadog preload. Extend it instead (" + provider.setupTerms().Persist(`export RUBYOPT="$RUBYOPT ..."`) + "), preserving the customer's Ruby options."
			}
			return "The Ruby test environment overrides the action's RUBYOPT without its Datadog preload. Preserve the instrumentation and the customer's Ruby options."
		}
	}
	keys := []string{"DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"}
	if len(frameworks) > 0 {
		keys = append(keys, "DD_TRACE_"+strings.ToUpper(frameworks[0])+"_ENABLED")
	}
	for _, key := range keys {
		raw, set := env[key]
		if !set {
			continue
		}
		// A matrix entry can disable instrumentation for its own row only.
		value, err := runtimeValue(raw, row)
		if err != nil {
			return "Ruby CI instrumentation setting " + key + " needs review: " + err.Error()
		}
		if value = strings.ToLower(strings.TrimSpace(value)); value == "false" || value == "0" {
			return "Ruby CI instrumentation is explicitly disabled by " + key
		}
	}
	return ""
}

// rubyMissingSetupReason asks for a static Ruby and an exact tracer release
// in the provider's setup before a test step.
func rubyMissingSetupReason(provider ciProvider) string {
	if provider.sharedSyntax() {
		return "Select a static Ruby version and exact ruby-tracer-version in a Datadog action before this test step; validate other installs separately."
	}
	return "Run the " + provider.setupName() + " with an exact " + provider.setupInput("ruby-tracer-version") + " before this test command, in a job whose image selects a static Ruby version (such as ruby:3.3); validate other installs separately."
}

// rubyImage matches official Ruby images (ruby:3.3, ruby:3.3.6-slim,
// docker.io/library/ruby:3.2-alpine), CircleCI's (cimg/ruby:3.3.6,
// circleci/ruby:2.7-node) and the alternative engines' images.
var rubyImage = regexp.MustCompile(`^(?:cimg/|circleci/|(?:docker\.io/)?(?:library/)?)(ruby|jruby|truffleruby|ghcr\.io/graalvm/truffleruby[a-z-]*|flavorjones/truffleruby):([^@]+)(?:@.*)?$`)

// rubyImageRuntime is the Ruby version a CI job's image selects, or the
// engine the tracer cannot instrument (JRuby, TruffleRuby, Ruby head). Both
// are empty when the image selects no static Ruby: no image, another image,
// a floating tag such as latest, or an unresolved variable.
func rubyImageRuntime(image string) (version, engine string) {
	match := rubyImage.FindStringSubmatch(strings.TrimSpace(image))
	if match == nil {
		return "", ""
	}
	name, tag := match[1], match[2]
	if name == "jruby" {
		return "", "JRuby"
	}
	if name != "ruby" {
		return "", "TruffleRuby"
	}
	if tag == "head" || strings.HasPrefix(tag, "head-") || tag == "master" {
		return "", "Ruby head"
	}
	version, _, _ = strings.Cut(tag, "-")
	if !rubyImageVersion.MatchString(version) {
		return "", ""
	}
	return version, ""
}

var rubyImageVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)

// rubyGemsUpdate is the RubyGems release a gem update --system step
// installs, or "".
func rubyGemsUpdate(run string) string {
	words, err := shellquote.Split(strings.TrimSpace(run))
	if err != nil || len(words) != 4 || words[0] != "gem" || words[1] != "update" || words[2] != "--system" {
		return ""
	}
	return words[3]
}

// rubyImageGemsProblem explains why a Ruby 2.7 or 3.0 image cannot run the
// installer: they ship RubyGems older than the 3.3.22 it requires, unless an
// earlier step updates RubyGems.
func rubyImageGemsProblem(ruby, rubygems string, provider ciProvider) string {
	if ruby != "2.7" && !strings.HasPrefix(ruby, "2.7.") && ruby != "3.0" && !strings.HasPrefix(ruby, "3.0.") {
		return ""
	}
	if parts := strings.SplitN(rubygems, ".", 3); len(parts) == 3 {
		major, majorErr := strconv.Atoi(parts[0])
		minor, minorErr := strconv.Atoi(parts[1])
		patch, patchErr := strconv.Atoi(strings.SplitN(parts[2], ".", 2)[0])
		if majorErr == nil && minorErr == nil && patchErr == nil && (major > 3 || major == 3 && (minor > 3 || minor == 3 && patch >= 22)) {
			return ""
		}
	}
	return "The Ruby " + ruby + " image ships RubyGems older than 3.3.22, which the " + provider.setupName() + "'s installer requires. Run gem update --system 3.4.22 before it in this job."
}
