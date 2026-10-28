package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

// TestCommand identifies an explicit framework invocation, not the surrounding
// build, publication or shell script. Locations retain every CI consumer.
type packageScript struct {
	Directory, Name string
	Leaf            bool
}

type TestCommand struct {
	CIInstrumentationExcluded bool `json:"ci_instrumentation_excluded,omitempty"`
	// UnsupportedNode marks an exclusion ddtest made itself: no dd-trace release
	// runs on the entry's CI Node, so it is not tried.
	UnsupportedNode bool `json:"unsupported_node,omitempty"`
	// WindowsRunner marks an exclusion on a Windows runner, whose Datadog
	// action is skipped: ddtest can't set up or validate Windows locally.
	WindowsRunner bool `json:"windows_runner,omitempty"`
	// RemoteGrid names the remote browser grid (such as Sauce Labs) whose CI
	// secrets the entry reads. It runs only in CI: CI setup instruments it, and
	// local validation reports it as a caveat instead of running it.
	RemoteGrid                      string            `json:"remote_grid,omitempty"`
	LocalOverrides                  []string          `json:"local_overrides,omitempty"`
	ID                              string            `json:"id,omitempty"`
	Services                        []ServiceCommand  `json:"services,omitempty"`
	PortOverride                    *PortOverrideFact `json:"port_override,omitempty"`
	webdriverIOScoped               bool
	packageScripts                  []packageScript
	cucumberProbeCommand            string
	cucumberProbeBefore             []LifecycleScript
	cucumberProbeAfter              []LifecycleScript
	CypressPackage                  string          `json:"cypress_package_directory,omitempty"`
	Cypress                         *CypressCommand `json:"cypress,omitempty"`
	Platform                        string          `json:"platform,omitempty"` // GOOS of the resolved hosted CI runner, when known.
	ProbeCoverageThresholdsDisabled bool            `json:"-"`
	mochaIstanbul                   bool
	LifecyclePrerequisites          []BuildCommand `json:"lifecycle_prerequisites,omitempty"`
	LifecycleHooks                  []string       `json:"lifecycle_hooks,omitempty"`
	mochaRunnerArgs                 []string
	mochaLookupDirectory            string
	mochaProbeDirectory             string
	mochaProbeEnvironment           map[string]string

	ScopeCommand     string `json:"-"` // Original invocation before adding a forwarding wrapper.
	ProbeLintSkipped bool   `json:"probe_lint_skipped,omitempty"`
	ReviewReason     string `json:"review_reason,omitempty"`
	// StepScopeNotes name other work the configuration's CI steps run in the
	// same instrumented process, outside this configuration's validation.
	StepScopeNotes          []string          `json:"step_scope_notes,omitempty"`
	ExitOnFailure           bool              `json:"exit_on_failure,omitempty"`
	Resolution              string            `json:"resolution,omitempty"`
	Directory               string            `json:"directory"`
	Command                 string            `json:"command"`
	Environment             map[string]string `json:"environment,omitempty"`
	locations               []commandLocation
	Locations               []string       `json:"locations,omitempty"`
	Prerequisites           []BuildCommand `json:"prerequisites,omitempty"`
	UnvalidatedSetup        []string       `json:"unvalidated_setup,omitempty"`
	CICoverageCommand       string         `json:"ci_coverage_command,omitempty"`
	mochaESM                bool
	mochaProbeCommand       string
	usesNYC                 bool
	cucumberCoverageWrapper string
	cucumberScoped          bool
	cucumberInlineIstanbul  bool
	cucumberLoaders         string // Source loaders the scoped script keeps after the inherited options.
	cucumberLoaderReason    string // Set when an in-script NODE_OPTIONS loader still replaces preloads.
}

// BuildCommand is a statically resolved build preceding a test in the same
// command chain. Discovery records preparation without executing it.
type PreparationKind string

const (
	PreparationPatch        PreparationKind = "patch"
	PreparationPackageBuild PreparationKind = "package-build"
	PreparationDependencies PreparationKind = "dependencies"
)

type BuildCommand struct {
	FactSource    string   `json:"fact_source,omitempty"`
	ExpectedFiles []string `json:"expected_files,omitempty"`
	Argv          []string `json:"argv,omitempty"`

	Environment map[string]string `json:"environment,omitempty"`
	Kind        PreparationKind   `json:"kind,omitempty"`
	Directory   string            `json:"directory"`
	Command     string            `json:"command"`
}

type ValidationScope struct {
	Commands   []TestCommand `json:"commands"`
	Unresolved []string      `json:"unresolved,omitempty"`
	Review     []string      `json:"review,omitempty"`
	// ComputedAxes can't be replayed locally value by value. Validation
	// decides whether each is a caveat or a blocker from its job's results.
	ComputedAxes []ComputedAxis `json:"computed_axes,omitempty"`
}

// ComputedAxis is a matrix axis computed at runtime (for example from
// fromJson) that only a dependency install reads; the job's tests run with the
// locally installed dependencies instead of each CI value.
type ComputedAxis struct {
	Workflow string `json:"workflow"`
	Job      string `json:"job"`
	Axis     string `json:"axis"`
}

// Location is the workflow/job prefix shared by the job's command locations.
func (axis ComputedAxis) Location() string {
	return axis.Workflow + " / " + axis.Job
}

func yarnImplicitInstall(words []string) bool {
	if len(words) == 0 || words[0] != "yarn" {
		return false
	}
	for _, word := range words[1:] {
		if !slices.Contains([]string{"--frozen-lockfile", "--non-interactive", "--ignore-scripts", "--ignore-optional", "--offline", "--immutable"}, word) {
			return false
		}
	}
	return true
}

// DiscoverValidationScope reads CI and package aliases without running them.
// Unknown fragments stay visible alongside known test invocations.
func DiscoverValidationScope(root, framework string) (ValidationScope, error) {
	return discoverValidationScope(root, framework, false)
}

func discoverValidationScope(root, framework string, allowSetupFacts bool) (ValidationScope, error) {
	var scope ValidationScope
	var allUnresolved, allReview []scopeDiagnostic
	language := discoveryLanguageName(framework)
	if !resolvesTestSteps(language, framework) {
		return scope, fmt.Errorf("CI command discovery is not available for %s", framework)
	}

	workflows, err := readWorkflows(root)
	if err != nil {
		return scope, err
	}
	scope.Review = append(scope.Review, otherCIReview(root, framework)...)
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			lastTest := -1
			{
				consumer, hidden := false, false
				var outside []string
				for i, step := range job.Steps {
					resolution := resolveTestStep(root, workflow, job, step, language, framework)
					switch {
					case resolution.Review:
					case resolution.Matched:
						consumer, lastTest = true, i
					case resolution.Reason != "" && stepMayStartTests(root, workflow, job, step, framework):
						hidden, lastTest = true, i
					case resolution.Reason != "":
						outside = append(outside, commandLocation{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: step.Run}.String()+": "+resolution.Reason)
					}
				}
				if !consumer && !hidden {
					if len(outside) == 0 {
						scope.Review = append(scope.Review, workflow.Path+" / "+name+": no "+framework+" invocation identified; action implementations and job conditions are outside this local validation scope.")
					}
					// No step here can start a selected test, so its unresolved
					// steps can't affect local validation; keep them visible only.
					for _, reason := range outside {
						scope.Review = append(scope.Review, "Not a "+framework+" test consumer: "+reason)
					}
					continue
				}
			}

			rows, computed, err := jobMatrix(job)
			if err != nil {
				scope.Unresolved = append(scope.Unresolved, workflow.Path+" / "+name+": "+err.Error())
				continue
			}
			for _, axis := range computed {
				scope.ComputedAxes = append(scope.ComputedAxes, ComputedAxis{Workflow: workflow.Path, Job: name, Axis: axis})
			}
			conditionNoted := false
			var notTried []string
			for _, row := range rows {
				job := job // Resolve this row without mutating the next row's runner.
				if framework == "cypress" {
					if label, ok := job.RunsOn.(string); ok {
						resolved, err := runtimeValue(label, row)
						if err == nil {
							job.RunsOn = resolved
						}
					}
				}
				active, assumed, err := possibleJobConditionPath(job.If, row)
				if err != nil {
					scope.Unresolved = append(scope.Unresolved, workflow.Path+" / "+name+": "+err.Error())
					continue
				}
				if !active {
					continue
				}
				if assumed && !conditionNoted {
					conditionNoted = true
					scope.Review = append(scope.Review, workflow.Path+" / "+name+": job condition "+strconv.Quote(strings.TrimSpace(job.If))+" depends on runtime context; validated as a possible CI path, the condition itself is not verified statically.")
				}
				// No dd-trace release runs on the Node a test step runs on, so its
				// tests are excluded like an explicit opt-out. ddtest decides this
				// without asking. The Node is the one active at that step: a job
				// may switch runtimes between test steps.
				skipsNode := discoveryLanguageNamed(language).skipsUnsupportedNode()
				unsupportedAt := func(step int) bool {
					if !skipsNode {
						return false
					}
					node, err := rowSetupNodeBefore(root, workflow, job, row, step)
					if err != nil || !UnsupportedNode(node) {
						return false
					}
					if !slices.Contains(notTried, node) {
						notTried = append(notTried, node)
					}
					return true
				}
				finalNode, finalErr := rowSetupNode(root, workflow, job, row)
				unsupportedRow := skipsNode && finalErr == nil && UnsupportedNode(finalNode)
				installerActive, installerSkipped := false, false
				var pendingSetup []string
				var preparation []BuildCommand
				var unresolved, setupUnresolved, review []scopeDiagnostic
				var commands []TestCommand
				for i, step := range job.Steps {
					if framework == "cypress" && strings.Split(strings.ToLower(step.Uses), "@")[0] == githubAction && step.Run == "" && step.ResolutionError == "" {
						step.If = ""
					}
					active, conditionErr := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
					if strings.EqualFold(strings.Split(step.Uses, "@")[0], githubAction) && conditionErr == nil {
						if selected, err := actionSelectsLanguage(step, row, setupActionLanguage(language)); err == nil && selected {
							if active {
								installerActive = true
							} else {
								installerSkipped = true
							}
						}
					}
					if conditionErr == nil && !active {
						continue
					}
					location := commandLocation{Workflow: workflow.Path, Job: name, Matrix: maps.Clone(row), Step: stepNumber(step, i), Command: step.Run}
					if (framework == "playwright" || framework == "cypress") && strings.Split(step.Uses, "@")[0] == "actions/upload-artifact" && step.Run == "" && step.ResolutionError == "" {
						review = append(review, scopeDiagnostic{location, "artifact upload is outside local " + map[string]string{"playwright": "Playwright", "cypress": "Cypress"}[framework] + " execution"})
						continue
					}
					resolved, env, envErr := resolveVariantStep(workflow, job, step, row, framework)
					resolution := resolveTestStep(root, workflow, job, resolved, language, framework)
					if readsComputedAxis(step.Run, computed, row) && !resolution.Matched && !stepMayStartTests(root, workflow, job, step, framework) {
						// A dependency install of a computed variant is the axis
						// itself; the tests run with the installed dependencies.
						// Any other reader could change the tests unseen.
						if !dependencyInstallOnly(step.Run) {
							unresolved = append(unresolved, scopeDiagnostic{location, "Setup other than a dependency install reads a matrix axis computed at runtime; it can't be replayed locally"})
							continue
						}
						review = append(review, scopeDiagnostic{location, "Dependency install reads a matrix axis computed at runtime; not replayed locally"})
						continue
					}
					if allowSetupFacts && conditionErr == nil && envErr == nil {
						directory, dirErr := repositoryDirectory(root, root, stepDirectory(workflow, job, resolved))
						if dirErr == nil {
							resolution = classifyFactPrerequisites(root, directory, resolved.Run, framework, resolution)
						}
					}
					// Known non-test output mutations after the last selected test cannot
					// prepare that test. Unknown commands still remain unresolved.
					laterTest := false
					if len(resolution.Setup)+len(resolution.Builds) > 0 && len(commands) > 0 && !resolution.Matched && (resolution.Reason == "" || resolution.Review) && !slices.ContainsFunc(resolution.Builds, func(build BuildCommand) bool { return build.Kind == PreparationNeedsFacts }) {
						for _, later := range job.Steps[i+1:] {
							if active, err := stepCondition(later, row, conditionEnvironment(workflow, job, runtimeStep{}, row)); err == nil && !active {
								continue
							}
							// Resolve like the main scan so matrix-selected tests are recognized.
							resolvedLater, _, _ := resolveVariantStep(workflow, job, later, row, framework)
							laterTest = laterTest || resolveTestStep(root, workflow, job, resolvedLater, language, framework).Matched
						}
						if !laterTest {
							for _, setup := range resolution.Setup {
								review = append(review, scopeDiagnostic{location, "Post-test output/setup requires separate review: " + setup})
							}
							for _, build := range resolution.Builds {
								review = append(review, scopeDiagnostic{location, "Post-test preparation requires separate review: " + build.Command})
							}
							resolution.Builds = nil
							resolution.Setup = nil
						}
					}
					for _, setup := range resolution.Setup {
						setupUnresolved = append(setupUnresolved, scopeDiagnostic{location, setup})
					}
					if conditionErr != nil && resolved.Run != "" && resolution.Reason == "" && !resolution.Matched && len(resolution.Builds) == 0 {
						resolution.Review, resolution.Reason = true, "Condition affects only a recognized non-test command; review separately: "+conditionErr.Error()
					}
					if len(resolution.Builds) > 0 && (conditionErr != nil || envErr != nil) {
						resolution.Review = false
					}
					if conditionErr != nil && !resolution.Review {
						pendingSetup = append(pendingSetup, conditionErr.Error())
						resolution.Reason = joinDiagnostic(resolution.Reason, conditionErr.Error())
					}
					if envErr != nil && !resolution.Matched && len(resolution.Builds) == 0 && resolution.Reason == "" {
						review = append(review, scopeDiagnostic{location, "Non-test setup environment requires separate review: " + envErr.Error()})
						envErr = nil
					}
					if envErr != nil && !resolution.Review {
						pendingSetup = append(pendingSetup, envErr.Error())
						resolution.Reason = joinDiagnostic(resolution.Reason, envErr.Error())
					}
					if resolution.Review {
						for j := range resolution.Builds {
							resolution.Builds[j].Environment = mergeEnvironment(env, resolution.Builds[j].Environment)
						}
						preparation = append(preparation, resolution.Builds...)
						pendingSetup = append(pendingSetup, resolution.Setup...)
						review = append(review, scopeDiagnostic{location, resolution.Reason})
						continue
					}
					if resolution.Reason != "" && !resolution.Matched && !stepMayStartTests(root, workflow, job, step, framework) {
						// Nothing after the job's last possible test can change that test.
						if i > lastTest {
							review = append(review, scopeDiagnostic{location, "After the job's last " + framework + " test step; outside the test path: " + resolution.Reason})
							continue
						}
						// A step that only reads or prints can't prepare a later test.
						if checkOnlyStep(resolved.Run) {
							review = append(review, scopeDiagnostic{location, "Check-only step before the job's " + framework + " test; it neither starts nor prepares a test: " + resolution.Reason})
							continue
						}
					}
					if resolution.Reason != "" {
						unresolved = append(unresolved, scopeDiagnostic{location, resolution.Reason})
					}
					if resolution.ReviewReason != "" {
						review = append(review, scopeDiagnostic{location, resolution.ReviewReason})
					}
					for j := range resolution.Builds {
						resolution.Builds[j].Environment = mergeEnvironment(env, resolution.Builds[j].Environment)
					}
					for _, entry := range resolution.Tests {
						entry.CIInstrumentationExcluded = ((installerSkipped && !installerActive) || mochaInstrumentationDisabled(workflow, job, resolved, row)) && checkExcludedJestBootstrap(workflow, job, resolved, row) == ""
						if unsupportedAt(i) {
							entry.CIInstrumentationExcluded, entry.UnsupportedNode = true, true
						}
						entry.WindowsRunner = entry.CIInstrumentationExcluded && installerSkipped && windowsRow(job, row)
						entry.RemoteGrid = resolved.RemoteGrid
						if framework == "mocha" && !entry.usesNYC && !entry.mochaIstanbul {
							parts, err := staticCommands(entry.Command)
							if err == nil && len(parts) == 1 && len(parts[0]) > 0 && !strings.Contains(parts[0][0], "=") {
								entry.CICoverageCommand = "npx --no-install nyc -- " + entry.Command
							}
						}

						for j := range entry.Prerequisites {
							entry.Prerequisites[j].Environment = mergeEnvironment(env, entry.Prerequisites[j].Environment)
						}
						entry.Prerequisites = append(consumedPreparation(root, preparation, entry, location, &review), entry.Prerequisites...)
						entry.UnvalidatedSetup = append(slices.Clone(pendingSetup), entry.UnvalidatedSetup...)
						entry.Environment = mergeEnvironment(env, entry.Environment)
						entry.Directory = filepath.ToSlash(filepath.Clean(entry.Directory))
						entry.locations = []commandLocation{location}
						if framework == "cypress" {
							entry.Platform = cypressRunnerPlatform(job.RunsOn)
						}
						commands = append(commands, entry)
					}
					if resolution.Reason != "" {
						pendingSetup = append(pendingSetup, location.String()+": "+resolution.Reason)
					}
					preparation = append(preparation, resolution.Builds...)
					pendingSetup = append(pendingSetup, resolution.Setup...)
				}
				if allowSetupFacts {
					for _, build := range preparation {
						if build.Kind != PreparationNeedsFacts {
							continue
						}
						attached := false
						for _, entry := range commands {
							for _, candidate := range entry.Prerequisites {
								attached = attached || (candidate.Command == build.Command && candidate.Directory == build.Directory)
							}
						}
						if !attached {
							scope.Unresolved = append(scope.Unresolved, workflow.Path+" / "+name+": local Node command has no following test configuration; review explicitly: "+build.Command)
						}
					}
				}

				// An explicit instrumentation opt-out never claims local validation
				// of its setup. Keep that setup visible without blocking other rows.
				// Unknown commands remain in unresolved, even on excluded rows.
				onlyExcluded := len(commands) > 0 && !slices.ContainsFunc(commands, func(command TestCommand) bool { return !command.CIInstrumentationExcluded })
				for _, diagnostic := range setupUnresolved {
					if onlyExcluded {
						diagnostic.Reason = "Unvalidated setup for instrumentation-excluded CI tests: " + diagnostic.Reason
						review = append(review, diagnostic)
					} else {
						unresolved = append(unresolved, diagnostic)
					}
				}

				// Unresolved steps don't block a row only no supported test runs on.
				if unsupportedRow && !slices.ContainsFunc(commands, func(command TestCommand) bool { return !command.UnsupportedNode }) {
					if !slices.Contains(notTried, finalNode) {
						notTried = append(notTried, finalNode)
					}
					for _, diagnostic := range unresolved {
						diagnostic.Reason = "Not tried (below Node 18, the oldest dd-trace major ddtest supports): " + diagnostic.Reason
						review = append(review, diagnostic)
					}
					unresolved = nil
				}
				allUnresolved = append(allUnresolved, unresolved...)
				allReview = append(allReview, review...)
				for _, entry := range commands {
					index := slices.IndexFunc(scope.Commands, func(previous TestCommand) bool {
						return previous.CIInstrumentationExcluded == entry.CIInstrumentationExcluded && reflect.DeepEqual(previous.Cypress, entry.Cypress) && previous.Platform == entry.Platform && previous.Directory == entry.Directory && previous.Command == entry.Command && previous.ExitOnFailure == entry.ExitOnFailure && slices.EqualFunc(previous.Prerequisites, entry.Prerequisites, EqualPreparation) && slices.Equal(previous.UnvalidatedSetup, entry.UnvalidatedSetup) && maps.Equal(previous.Environment, entry.Environment)
					})
					if index < 0 {
						scope.Commands = append(scope.Commands, entry)
					} else {
						scope.Commands[index].locations = append(scope.Commands[index].locations, entry.locations...)
						// Steps merged into one configuration keep every shared-step note.
						for _, note := range entry.StepScopeNotes {
							if !slices.Contains(scope.Commands[index].StepScopeNotes, note) {
								scope.Commands[index].StepScopeNotes = append(scope.Commands[index].StepScopeNotes, note)
							}
						}
						scope.Commands[index].packageScripts = append(scope.Commands[index].packageScripts, entry.packageScripts...)
					}
				}
			}
			if len(notTried) > 0 {
				scope.Review = append(scope.Review, NotTriedNote(workflow.Path+" / "+name, notTried))
			}
		}
	}
	scope.Unresolved = append(scope.Unresolved, groupMatrixDiagnostics(allUnresolved)...)
	scope.Review = append(scope.Review, groupMatrixDiagnostics(allReview)...)
	for i := range scope.Commands {
		for _, location := range scope.Commands[i].locations {
			scope.Commands[i].Locations = append(scope.Commands[i].Locations, location.String())
		}
	}
	return scope, nil
}

// ValidationCommands also resolves a selected local alias, for scope accounting.
func ValidationCommands(root, command, framework string) []TestCommand {
	if language := discoveryLanguageFor(framework); language != nil {
		if commands, ok := language.localCommands(root, command, framework); ok {
			return commands
		}
	}
	remaining := packageScriptExpansionLimit
	return resolveJestSequence(root, root, command, nil, &remaining, framework).Tests
}

// File rewrites in earlier CI steps change what the same test command executes.
func mutatesCIFiles(command string) bool {
	commands, err := staticCommands(command)
	if err != nil {
		return false
	} // Already retained as unresolved setup.
	for _, words := range commands {
		words, _, err = commandEnvironment(words)
		if err != nil {
			continue
		}
		// xargs runs its command once per input; classify that command.
		if len(words) > 0 && words[0] == "xargs" {
			if command := xargsCommand(words[1:]); len(command) > 0 && mutatesCIFiles(shellquote.Join(command...)) {
				return true
			}
			continue
		}
		if len(words) > 0 && slices.Contains([]string{"sed", "cp", "mv", "rm", "patch", "rimraf", "rsync"}, words[0]) {
			return true
		}
		if changesDependencies(words) {
			return true
		}
	}
	return false
}

// Skip xargs's own options, including those that take a separate value.
func xargsCommand(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		option := args[0]
		args = args[1:]
		if option == "--" {
			break
		}
		if slices.Contains([]string{"-n", "-L", "-P", "-s", "-d", "-E", "-I", "-a"}, option) && len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

// Ordinary lockfile installs prepare declared dependencies. Explicit package
// additions/removals change what the later test command resolves and must stay
// visible as setup, even when an installer itself is a recognized CI step.
func changesDependencies(words []string) bool {
	if len(words) < 2 || !slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, words[0]) {
		return false
	}
	args := words[1:]
	for len(args) > 0 && (args[0] == "-g" || args[0] == "--global") {
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	if slices.Contains([]string{"rm", "remove", "uninstall", "add"}, args[0]) {
		return true
	}
	if args[0] != "install" && args[0] != "i" {
		return false
	}
	// A global install of only the package manager itself (npm install -g
	// npm@latest) upgrades the CI toolchain; it changes no project dependency.
	global := len(args) < len(words)-1 || slices.Contains(args, "-g") || slices.Contains(args, "--global")
	toolchain := global
	installs := false
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "-") {
			installs = true
			name, _, _ := strings.Cut(arg, "@")
			toolchain = toolchain && slices.Contains(packageManagerPackages, name)
		}
	}
	return installs && !toolchain
}

// windowsRow reports whether a matrix row runs on a Windows runner.
func windowsRow(job runtimeJob, row map[string]any) bool {
	for _, label := range runnerLabels(job.RunsOn) {
		if runner, err := runtimeValue(label, row); err == nil && strings.HasPrefix(strings.ToLower(runner), "windows") {
			return true
		}
	}
	return false
}

// dependencyInstallOnly reports whether every command in a step installs
// project dependencies (npm install pkg@${{ matrix.version }}), with workflow
// expressions standing in for literal values.
func dependencyInstallOnly(command string) bool {
	commands, err := staticCommands(workflowExpression.ReplaceAllString(command, "value"))
	if err != nil || len(commands) == 0 {
		return false
	}
	for _, words := range commands {
		words, _, err = commandEnvironment(words)
		if err != nil || !changesDependencies(words) {
			return false
		}
	}
	return true
}

func readsComputedAxis(command string, axes []string, row map[string]any) bool {
	return slices.ContainsFunc(axes, func(axis string) bool {
		return row[axis] == computedAxisValue(axis) && regexp.MustCompile(`\bmatrix\.`+regexp.QuoteMeta(axis)+`\b`).MatchString(command)
	})
}

// CI configuration of providers other than GitHub Actions, by conventional path.
var otherCIFiles = []string{".circleci/config.yml", "circle.yml", ".gitlab-ci.yml", "azure-pipelines.yml", ".travis.yml", "Jenkinsfile", "bitbucket-pipelines.yml", ".buildkite/pipeline.yml", "appveyor.yml", ".drone.yml"}

// otherCIReview names other providers' CI files that mention the framework.
// They are not parsed, so their tests stay visible instead of silently outside.
func otherCIReview(root, framework string) []string {
	var review []string
	for _, path := range otherCIFiles {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			continue
		}
		text := strings.ToLower(string(data))
		if strings.Contains(text, framework) || namesFramework(text, framework, true) {
			review = append(review, path+": not a GitHub Actions workflow; ddtest does not analyze it, so "+framework+" tests it runs are outside this validation and need their own Datadog setup.")
		}
	}
	return review
}
