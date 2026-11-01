package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

// Apply owns only recognized source edits, then starts a fresh plain testdrive.
// Agent checkpoints, setup facts and the structured coordinator arrive separately.
type ApplyOptions struct {
	Site           string
	ExplicitTracer bool
	Preview        bool
	AllowDirty     bool
	// FactsFile supplies the same setup facts the coordinator would accept.
	FactsFile string
}

func (t *Testdrive) Apply(ctx context.Context, output io.Writer, options ApplyOptions) (runErr error) {
	if !t.lang().coordinated() || !onboard.SupportsAutomaticSetup(t.framework.Name()) {
		return fmt.Errorf("automatic setup is unavailable for %s; preserve its hooks and use onboard instructions followed by testdrive", t.framework.Name())
	}
	// A preview or a refusal must not leave an empty report folder behind.
	reports := filepath.Join(t.repositoryRoot, ".testoptimization")
	_, statErr := os.Lstat(reports)
	validating := false
	defer func() {
		if os.IsNotExist(statErr) && !validating {
			removeEmptyDirectories(reports)
		}
	}()
	lock, err := workspace.AcquireLock(t.repositoryRoot)
	if err != nil {
		return err
	}
	defer func() {
		if lock != nil {
			_ = lock.Close()
		}
	}()
	if err := recoverProbes(t.repositoryRoot); err != nil {
		return err
	}
	scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
	if options.FactsFile != "" {
		scope, ctx, err = t.factsSetupScope(ctx, options.FactsFile)
	}
	if err != nil {
		return err
	}
	partial := len(scope.Unresolved) > 0 && slices.ContainsFunc(scope.Commands, t.readyConfiguration)
	if len(scope.Commands) == 0 || len(scope.Unresolved) > 0 && !partial {
		return fmt.Errorf("CI discovery needs review before automatic setup: %v", scope.Unresolved)
	}
	if partial {
		_, _ = fmt.Fprintf(output, "Unresolved CI is not validated locally; it is reported as a blocker, and setup edits a CI step only where it recognizes the test command: %s\n", strings.Join(scope.Unresolved, "; "))
	}
	for _, entry := range scope.Commands {
		if t.setupStaysReported(entry, partial) {
			continue
		}
		if len(entry.UnvalidatedSetup) > 0 || missingPrerequisiteFacts(entry) {
			return fmt.Errorf("%s requires setup review: %s", entry.Command, strings.Join(entry.UnvalidatedSetup, "; "))
		}
	}
	selection, reportOnly, err := t.selectSetupTracerReporting(ctx, scope, options.ExplicitTracer)
	if err == nil && t.setupTracerNote != "" {
		_, _ = fmt.Fprintln(output, t.setupTracerNote)
	}
	if err == nil && selection.Version == "" {
		err = noRunnableConfiguration(reportOnly)
	}
	if err != nil {
		return err
	}
	for _, entry := range reportOnly {
		_, _ = fmt.Fprintf(output, "Not set up or validated locally; reported as a blocker: %s\n", entry)
	}
	plan, err := onboard.PlanJavaScriptSetup(t.repositoryRoot, t.framework.Name(), selection.Version, options.Site, selection.Node)
	if err != nil {
		return err
	}
	if plan.Configured() {
		if _, err := fmt.Fprintf(output, "%s CI setup is already configured; no edits.\n", displayName(t.framework.Name())); err != nil || options.Preview {
			return err
		}
	}
	diff, err := plan.Diff()
	if err != nil {
		return err
	}
	if _, err = io.WriteString(output, diff); err != nil {
		return err
	}
	if impact := plan.LocalImpact(); len(impact) > 0 {
		_, _ = fmt.Fprintln(output, "Local developer impact:")
		for _, note := range impact {
			_, _ = fmt.Fprintf(output, "- %s\n", note)
		}
	}
	if options.Preview {
		next := "pass --apply --yes to apply and validate it."
		if dirty, err := plan.DirtyFiles(); err == nil && len(dirty) > 0 {
			next = "commit or stash " + strings.Join(dirty, ", ") + " and pass --apply --yes, or pass --apply --yes --allow-dirty to extend those uncommitted edits."
		}
		_, err = fmt.Fprintln(output, "Preview only; no source files changed and no tests executed. Review this diff, then "+next)
		return err
	}
	changes, err := plan.Apply(options.AllowDirty)
	if err != nil {
		return err
	}
	for _, change := range changes {
		_, _ = fmt.Fprintf(output, "Updated %s (before %s; after %s).\n", change.Path, change.Before, change.After)
	}
	if len(changes) > 0 {
		defer func() {
			if runErr != nil {
				names := make([]string, 0, len(changes))
				for _, change := range changes {
					names = append(names, change.Path)
				}
				_, _ = fmt.Fprintf(output, "Validation failed after setup. Source edits remain applied: %s.\nTo undo only the setup edits, preserving earlier changes, run from the repository root:\ngit apply -R --whitespace=nowarn <<'DDTEST_SETUP_REVERT'\n%sDDTEST_SETUP_REVERT\n", strings.Join(names, ", "), diff)
			}
		}()
	}
	if err := lock.Close(); err != nil {
		return err
	}
	lock = nil
	validating = true
	fresh, err := Prepare(PrepareOptions{TracerVersion: selection.Version, All: true})
	if err != nil {
		return err
	}
	return fresh.Run(ctx, output)
}

// factsSetupScope discovers the scope as the coordinator does and applies the
// facts bound to the current input fingerprint, so a preview shows what
// --apply --yes with the same facts would edit.
func (t *Testdrive) factsSetupScope(ctx context.Context, factsFile string) (onboard.ValidationScope, context.Context, error) {
	fingerprint, err := InputFingerprint(ctx, t.repositoryRoot)
	if err != nil {
		return onboard.ValidationScope{}, ctx, err
	}
	scope, err := onboard.DiscoverWorkflowScope(t.repositoryRoot, t.framework.Name())
	if err != nil {
		return onboard.ValidationScope{}, ctx, err
	}
	onboard.AssignConfigurationIDs(&scope)
	data, err := readSetupFacts(t.repositoryRoot, factsFile)
	if err != nil {
		return onboard.ValidationScope{}, ctx, err
	}
	if _, err := applySetupFactsData(t.repositoryRoot, data, fingerprint, &scope); err != nil {
		return onboard.ValidationScope{}, ctx, fmt.Errorf("setup facts %s: %w", factsFile, err)
	}
	return scope, onboard.WithWorkflowSetupScope(ctx, t.repositoryRoot, scope), nil
}

func (t *Testdrive) selectSetupTracer(ctx context.Context, scope onboard.ValidationScope, explicit bool) (platform.JSSelection, error) {
	selected, reportOnly, err := t.selectSetupTracerReporting(ctx, scope, explicit)
	if err == nil && selected.Version == "" {
		err = noRunnableConfiguration(reportOnly)
	}
	return selected, err
}

// noRunnableConfiguration explains an empty selection: every configuration
// was excluded or can only be reported.
func noRunnableConfiguration(reportOnly []string) error {
	if len(reportOnly) > 0 {
		return fmt.Errorf("no configuration can be set up and validated locally; reported: %s", strings.Join(reportOnly, "; "))
	}
	return fmt.Errorf("no configurations enabled for instrumentation")
}

// localOnlyUnsupported names configurations that can never run locally, such
// as WebdriverIO cloud grids and multiremote. They don't constrain the tracer:
// setup leaves them unedited and validation reports them.
func localOnlyUnsupported(framework string, check jestPreflight) string {
	if framework != "webdriverio" || check.WebdriverIO == nil {
		return ""
	}
	if verdict := checkWebdriverIOSupport(check, platform.JSSelection{}); verdict.Code == "cloud_grid_unsupported" || verdict.Code == "multiremote_unsupported" {
		return verdict.Reason
	}
	return ""
}

// selectSetupTracerReporting selects one tracer for the configurations setup
// edits. Configurations that can only be reported are returned instead of
// failing selection; an empty selection means none can be set up.
func (t *Testdrive) selectSetupTracerReporting(ctx context.Context, scope onboard.ValidationScope, explicit bool) (platform.JSSelection, []string, error) {
	var reportOnly []string
	ci := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, t.framework.Name())
	if len(ci.Jobs) == 0 {
		return platform.JSSelection{}, nil, fmt.Errorf("CI test runtime is unresolved: %s", ci.Reason)
	}
	pin := ""
	for _, job := range ci.Jobs {
		if strings.Contains(job.Action, "@v2") {
			return platform.JSSelection{}, nil, fmt.Errorf("existing %s; upgrade to v3 or review manually", job.Action)
		}
		if job.Tracer != "" {
			version := strings.TrimPrefix(job.Tracer, "dd-trace@")
			if pin != "" && pin != version {
				return platform.JSSelection{}, nil, fmt.Errorf("CI selects multiple tracer versions; align them explicitly")
			}
			pin = version
		}
	}
	version := t.tracerVersion
	if pin != "" && !explicit && t.projectTracer == "" {
		version = pin
	}
	var selected platform.JSSelection
	for _, entry := range scope.Commands {
		// Entries setup leaves unedited never constrain the tracer it selects.
		if t.setupStaysReported(entry, true) {
			continue
		}
		words, err := shellquote.Split(entry.Command)
		if entry.Cypress != nil {
			words, err = []string{"node"}, nil
		}
		if err != nil || len(words) == 0 {
			return platform.JSSelection{}, nil, fmt.Errorf("unresolved command: %s", entry.Command)
		}
		probe := *t
		probe.cypress, probe.cypressCommand = entry.Cypress, entry.Command
		directory, directoryErr := localSetupDirectory(t.repositoryRoot, entry.Directory)
		if directoryErr != nil {
			return platform.JSSelection{}, nil, directoryErr
		}
		probe.configurationRoot = t.checkoutRoot()
		probe.repositoryRoot = directory
		probe.command, probe.args = probe.configurationExecutable(words[0]), words[1:]
		probe.tracerVersion = version
		probe.executor = configurationExecutor{commandExecutor: t.executor, environment: entry.Environment, without: entry.WithoutCIValues}
		probe.configurationEnvironment = entry.Environment
		// Preflight loads the configuration; never execute one outside the checkout.
		policy := frameworkFeaturePolicies[probe.framework.Name()]
		if err := validateConfigurationFiles(probe.checkoutRoot(), probe.repositoryRoot, probe.args, policy.ConfigurationFiles, policy.configurationOptions()); err != nil {
			return platform.JSSelection{}, nil, err
		}
		probe.preflight = probe.frameworkPreflight()
		check := validationResult{CIRuntime: &ci}
		err = probe.preflightInDirectory(ctx, directory, &check)
		if check.Selection == nil || check.Selection.Version == "" || check.Preflight == nil {
			if err == nil {
				err = fmt.Errorf("preflight did not resolve tracer metadata")
			}
			return platform.JSSelection{}, nil, err
		}
		if reason := localOnlyUnsupported(t.framework.Name(), *check.Preflight); reason != "" {
			reportOnly = append(reportOnly, entry.Command+": "+reason)
			continue
		}
		selection := *check.Selection
		if pin == "" && t.projectTracer == "" && selection.Source != "project" && !explicit && selected.Version == "" {
			if rec := check.Preflight.Recommendation; rec != nil && len(rec.Unresolved) == 0 && len(rec.Compatible) > 0 {
				if rec.Selection.Version != selection.Version && len(rec.Unsupported) == 0 {
					t.setupTracerNote = coveringTracerNote(rec)
				}
				selection = rec.Selection
			}
			// Setup instruments every active CI row, so the default release must
			// run on each of their Node versions, not only on the local one.
			if len(unsupportedCINodes(ci, selection)) > 0 && t.resolveJSTracer != nil && t.resolveJSTags != nil {
				rec, _ := recommendJavaScriptTracer(ctx, *check.Preflight, &ci, t.resolveJSTracer, t.resolveJSTags, t.framework.Name())
				if rec == nil {
					// No release passes the framework's support check: select the
					// newest one every instrumented CI row runs, whose support
					// check then names what the project must change.
					rec = ciCoveringTracer(ctx, *check.Preflight, &ci, t.resolveJSTracer, t.resolveJSTags)
				}
				if rec == nil || len(rec.Unsupported) > 0 || len(rec.Unresolved) > 0 {
					return platform.JSSelection{}, nil, ciTracerMajorsError(t.framework.Name(), ci, selection, rec)
				}
				selection = rec.Selection
				t.setupTracerNote = coveringTracerNote(rec)
			}
		}
		support := map[string]func(jestPreflight, platform.JSSelection) verdict{"jest": checkJestSupport, "vitest": checkVitestSupport, "mocha": checkMochaSupport, "playwright": checkPlaywrightSupport, "cypress": checkCypressSupport, "cucumber": checkCucumberSupport, "webdriverio": checkWebdriverIOSupport}[t.framework.Name()]
		if verdict := support(*check.Preflight, selection); verdict.Status != "compatible" {
			if t.setupTracerNote != "" {
				return platform.JSSelection{}, nil, fmt.Errorf("%s %s: %s", t.setupTracerNote, entry.Command, verdict.Reason)
			}
			return platform.JSSelection{}, nil, fmt.Errorf("%s: %s", entry.Command, verdict.Reason)
		}
		for _, job := range ci.Jobs {
			node := job.Node
			if job.NodeResolution != nil {
				node = job.NodeResolution.Version
			}
			status, reason := onboard.CompareNodeRequirement(node, selection.Node)
			if !setupInstrumentsRow(job) {
				continue
			}
			if status == "incompatible" {
				var rec *tracerRecommendation
				if t.resolveJSTracer != nil && t.resolveJSTags != nil {
					rec, _ = recommendJavaScriptTracer(ctx, *check.Preflight, &ci, t.resolveJSTracer, t.resolveJSTags, t.framework.Name())
				}
				return platform.JSSelection{}, nil, ciTracerMajorsError(t.framework.Name(), ci, selection, rec)
			}
			if status != "compatible" || !repairableSetupFinding(t.framework.Name(), job) && job.Status != "compatible" {
				return platform.JSSelection{}, nil, fmt.Errorf("%s / %s / step %d: %s %s", job.Workflow, job.Job, job.Step, job.Reason, reason)
			}
		}
		if selected.Version != "" && selected.Version != selection.Version {
			return platform.JSSelection{}, nil, fmt.Errorf("tracer selection changed across configurations")
		}
		selected = selection
		version = selected.Version
	}
	return selected, reportOnly, nil
}

// readyConfiguration reports whether entry can be set up and validated now:
// instrumented, runnable on this host and without setup still to declare.
func (t *Testdrive) readyConfiguration(entry onboard.TestCommand) bool {
	return !t.setupStaysReported(entry, false) && len(entry.UnvalidatedSetup) == 0 && !missingPrerequisiteFacts(entry)
}

// setupStaysReported reports whether entry's pending setup never stops setup of
// the other configurations: excluded and Windows-only entries are left
// unedited, and in a partial run, setup ddtest couldn't parse stays a report
// blocker. Prerequisite outputs that facts can declare are still requested.
func (t *Testdrive) setupStaysReported(entry onboard.TestCommand, partial bool) bool {
	return ciInstrumentationDisabled(entry) || onboard.WindowsOnlyConfiguration(t.repositoryRoot, entry) ||
		partial && len(entry.UnvalidatedSetup) > 0 && !missingPrerequisiteFacts(entry)
}

// ciCoveringTracer is the newest release that runs on every instrumented CI
// row's Node, whatever the framework's support check says; nil when no
// release covers them all.
func ciCoveringTracer(ctx context.Context, check jestPreflight, ci *onboard.RuntimeCheck, resolve func(context.Context, string) (platform.JSSelection, error), tags func(context.Context) (map[string]string, error)) *tracerRecommendation {
	rec, _ := recommendJavaScriptTracer(ctx, check, ci, resolve, tags, "")
	if rec == nil || len(rec.Unsupported) > 0 || len(rec.Unresolved) > 0 || len(rec.Compatible) == 0 {
		return nil
	}
	return rec
}

// coveringTracerNote says which release setup selected for the CI rows.
func coveringTracerNote(rec *tracerRecommendation) string {
	return "Selected dd-trace@" + rec.Selection.Version + ", the newest release that runs on every instrumented CI row (Node " + scopeList(rec.Compatible) + ")."
}

// setupInstrumentsRow reports whether setup instruments this CI row. Excluded
// rows and rows on a Node no dd-trace release supports stay uninstrumented,
// and rows whose command didn't resolve stay unedited report blockers, so none
// of them constrains the tracer setup selects.
func setupInstrumentsRow(job onboard.RuntimeFinding) bool {
	return job.Code != onboard.CodeExcludedRuntime && job.Code != onboard.CodeUnsupportedNode && job.Code != onboard.CodeUnresolvedCommand
}

// unsupportedCINodes lists instrumented CI rows whose known Node cannot run
// the release.
func unsupportedCINodes(ci onboard.RuntimeCheck, selection platform.JSSelection) []onboard.RuntimeFinding {
	var rows []onboard.RuntimeFinding
	for _, job := range ci.Jobs {
		node := job.Node
		if job.NodeResolution != nil {
			node = job.NodeResolution.Version
		}
		if !setupInstrumentsRow(job) || node == "" {
			continue
		}
		if status, _ := onboard.CompareNodeRequirement(node, selection.Node); status == onboard.StatusIncompatible {
			rows = append(rows, job)
		}
	}
	return rows
}

// ciTracerMajors reports CI rows the selected release cannot run on. When
// opting those rows out is a complete fix, Exclusions holds the exact edits;
// excluding CI rows is a decision for the user, never applied automatically.
type ciTracerMajors struct {
	message    string
	Exclusions []string
	// Recommended is the verified release covering the most instrumented rows.
	Recommended string
}

func (e *ciTracerMajors) Error() string { return e.message }

// ciTracerMajorsError names the CI rows a release cannot run on and, when no
// single verified release covers every row, says so instead of guessing.
func ciTracerMajorsError(framework string, ci onboard.RuntimeCheck, selection platform.JSSelection, rec *tracerRecommendation) error {
	var rows []string
	for _, job := range unsupportedCINodes(ci, selection) {
		rows = appendUnique(rows, fmt.Sprintf("%s / %s / step %d (Node %s)", job.Workflow, job.Job, job.Step, nodeScopeLabel(job.Node)))
	}
	message := fmt.Sprintf("dd-trace@%s requires Node %s, which these CI rows do not provide: %s.", selection.Version, selection.Node, strings.Join(rows, "; "))
	majors := &ciTracerMajors{}
	switch {
	case rec != nil && len(rec.Unsupported) > 0:
		// Only Node versions no single release covers need different majors.
		majors.Recommended = rec.Selection.Version
		message += fmt.Sprintf(" The best verified release, dd-trace@%s, supports CI Node %s but not %s. CI rows need different dd-trace majors; select one --tracer-version every instrumented row supports", rec.Selection.Version, scopeList(rec.Compatible), scopeList(append(append([]string{}, rec.Unsupported...), rec.Unresolved...)))
	case rec != nil:
		majors.Recommended = rec.Selection.Version
		message += fmt.Sprintf(" dd-trace@%s supports every instrumented CI row whose Node is known (%s); rerun with --tracer-version %s", rec.Selection.Version, scopeList(rec.Compatible), rec.Selection.Version)
		if len(rec.Unresolved) > 0 {
			message += fmt.Sprintf(" and confirm CI Node %s", scopeList(rec.Unresolved))
		}
	default:
		message += " No verified dd-trace release was resolved for these rows; select one --tracer-version every instrumented row supports"
	}
	majors.Exclusions = ciRowExclusions(framework, ci, selection)
	if len(majors.Exclusions) > 0 {
		message += ", or, only with the user's approval, keep the older rows running uninstrumented: " + strings.Join(majors.Exclusions, "; ")
	}
	majors.message = message
	return majors
}

// ciRowExclusions returns the opt-out edit for each CI step whose every
// instrumented row needs another tracer major, or nil when that isn't a
// complete fix: a step with supported rows needs a per-row condition, and only
// Mocha discovery recognizes the opt-out.
func ciRowExclusions(framework string, ci onboard.RuntimeCheck, selection platform.JSSelection) []string {
	if framework != "mocha" {
		return nil
	}
	type step struct {
		workflow, job string
		number        int
	}
	active := map[step]int{}
	for _, job := range ci.Jobs {
		if setupInstrumentsRow(job) {
			active[step{job.Workflow, job.Job, job.Step}]++
		}
	}
	unsupported := map[step][]string{}
	var order []step
	for _, job := range unsupportedCINodes(ci, selection) {
		key := step{job.Workflow, job.Job, job.Step}
		if unsupported[key] == nil {
			order = append(order, key)
		}
		unsupported[key] = append(unsupported[key], nodeScopeLabel(job.Node))
	}
	var edits []string
	for _, key := range order {
		if len(unsupported[key]) != active[key] {
			return nil
		}
		edits = append(edits, fmt.Sprintf("%s / %s / step %d: add `DD_CIVISIBILITY_ENABLED: 'false'` to the step env (Node %s keep running without the tracer)", key.workflow, key.job, key.number, strings.Join(slices.Compact(slices.Sorted(slices.Values(unsupported[key]))), ", ")))
	}
	return edits
}

// repairableSetupFinding reports whether setup itself fixes the finding: it adds
// the action and its preload, and wraps uninstrumented Mocha commands with NYC.
// Only custom or conflicting initialization still needs review.
func repairableSetupFinding(framework string, job onboard.RuntimeFinding) bool {
	return job.Code == onboard.CodeMissingInstrumentation || job.Code == onboard.CodeMissingInitialization ||
		framework == "mocha" && job.Code == onboard.CodeUnverifiedInitialization && strings.HasPrefix(job.Reason, onboard.MochaCoverageGap)
}

func (t *Testdrive) preflightInDirectory(ctx context.Context, directory string, result *validationResult) (runErr error) {
	previous, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(directory); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(previous)) }()
	return t.preflight(ctx, io.Discard, result)
}

// removeEmptyDirectories removes directory if it holds only empty directories.
func removeEmptyDirectories(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return
		}
		removeEmptyDirectories(filepath.Join(directory, entry.Name()))
	}
	_ = os.Remove(directory)
}
