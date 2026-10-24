package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
)

// Apply owns only recognized source edits, then starts a fresh plain testdrive.
// Agent checkpoints, setup facts and the structured coordinator arrive separately.
type ApplyOptions struct {
	Site           string
	ExplicitTracer bool
	Preview        bool
	AllowDirty     bool
}

func (t *Testdrive) Apply(ctx context.Context, output io.Writer, options ApplyOptions) (runErr error) {
	if t.language != "javascript" || !onboard.SupportsAutomaticSetup(t.framework.Name()) {
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
	lock, err := acquireWorkflowLock(t.repositoryRoot)
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
	if err != nil {
		return err
	}
	if len(scope.Commands) == 0 || len(scope.Unresolved) > 0 {
		return fmt.Errorf("CI discovery needs review before automatic setup: %v", scope.Unresolved)
	}
	for _, entry := range scope.Commands {
		if len(entry.UnvalidatedSetup) > 0 {
			return fmt.Errorf("%s requires setup review: %s", entry.Command, strings.Join(entry.UnvalidatedSetup, "; "))
		}
	}
	selection, err := t.selectSetupTracer(ctx, scope, options.ExplicitTracer)
	if err != nil {
		return err
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
		_, err = fmt.Fprintln(output, "Preview only; no source files changed and no tests executed. Review this diff, then pass --apply --yes to apply and validate it.")
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

func (t *Testdrive) selectSetupTracer(ctx context.Context, scope onboard.ValidationScope, explicit bool) (platform.JSSelection, error) {
	ci := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, t.framework.Name())
	if len(ci.Jobs) == 0 {
		return platform.JSSelection{}, fmt.Errorf("CI test runtime is unresolved: %s", ci.Reason)
	}
	pin := ""
	for _, job := range ci.Jobs {
		if strings.Contains(job.Action, "@v2") {
			return platform.JSSelection{}, fmt.Errorf("existing %s; upgrade to v3 or review manually", job.Action)
		}
		if job.Tracer != "" {
			version := strings.TrimPrefix(job.Tracer, "dd-trace@")
			if pin != "" && pin != version {
				return platform.JSSelection{}, fmt.Errorf("CI selects multiple tracer versions; align them explicitly")
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
		words, err := shellquote.Split(entry.Command)
		if entry.Cypress != nil {
			words, err = []string{"node"}, nil
		}
		if err != nil || len(words) == 0 {
			return platform.JSSelection{}, fmt.Errorf("unresolved command: %s", entry.Command)
		}
		probe := *t
		probe.cypress, probe.cypressCommand = entry.Cypress, entry.Command
		directory, directoryErr := localSetupDirectory(t.repositoryRoot, entry.Directory)
		if directoryErr != nil {
			return platform.JSSelection{}, directoryErr
		}
		probe.configurationRoot = t.checkoutRoot()
		probe.repositoryRoot = directory
		probe.command, probe.args = probe.configurationExecutable(words[0]), words[1:]
		probe.tracerVersion = version
		probe.executor = configurationExecutor{t.executor, entry.Environment}
		probe.configurationEnvironment = entry.Environment
		// Preflight loads the configuration; never execute one outside the checkout.
		policy := frameworkFeaturePolicies[probe.framework.Name()]
		if err := validateConfigurationFiles(probe.checkoutRoot(), probe.repositoryRoot, probe.args, policy.ConfigurationFiles, policy.configurationOptions()); err != nil {
			return platform.JSSelection{}, err
		}
		probe.bindPreflight()
		check := validationResult{CIRuntime: &ci}
		err = probe.preflightInDirectory(ctx, directory, &check)
		if check.Selection == nil || check.Selection.Version == "" || check.Preflight == nil {
			if err == nil {
				err = fmt.Errorf("preflight did not resolve tracer metadata")
			}
			return platform.JSSelection{}, err
		}
		selection := *check.Selection
		if pin == "" && t.projectTracer == "" && selection.Source != "project" && !explicit && selected.Version == "" {
			if rec := check.Preflight.Recommendation; rec != nil && len(rec.Unresolved) == 0 && len(rec.Compatible) > 0 {
				selection = rec.Selection
			}
		}
		support := map[string]func(jestPreflight, platform.JSSelection) verdict{"jest": checkJestSupport, "vitest": checkVitestSupport, "mocha": checkMochaSupport, "playwright": checkPlaywrightSupport, "cypress": checkCypressSupport, "cucumber": checkCucumberSupport, "webdriverio": checkWebdriverIOSupport}[t.framework.Name()]
		if verdict := support(*check.Preflight, selection); verdict.Status != "compatible" {
			return platform.JSSelection{}, fmt.Errorf("%s: %s", entry.Command, verdict.Reason)
		}
		for _, job := range ci.Jobs {
			node := job.Node
			if job.NodeResolution != nil {
				node = job.NodeResolution.Version
			}
			status, reason := onboard.CompareNodeRequirement(node, selection.Node)
			if job.Code == "excluded_runtime" {
				continue
			}
			if status == "incompatible" && job.Code == "missing_instrumentation" {
				continue
			}
			if status != "compatible" || !repairableSetupFinding(t.framework.Name(), job) && job.Status != "compatible" {
				return platform.JSSelection{}, fmt.Errorf("%s / %s / step %d: %s %s", job.Workflow, job.Job, job.Step, job.Reason, reason)
			}
		}
		if selected.Version != "" && selected.Version != selection.Version {
			return platform.JSSelection{}, fmt.Errorf("tracer selection changed across configurations")
		}
		selected = selection
		version = selected.Version
	}
	return selected, nil
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

// Bind method values after copying a drive so preflight uses its command and workspace.
func (t *Testdrive) bindPreflight() {
	switch t.framework.Name() {
	case "vitest":
		t.preflight = t.checkVitestPreflight
	case "playwright":
		t.preflight = t.checkPlaywrightPreflight
	case "mocha":
		t.preflight = t.checkMochaPreflight
	case "cypress":
		t.preflight = t.checkCypressPreflight
	case "cucumber":
		t.preflight = t.checkCucumberPreflight
	case "webdriverio":
		t.preflight = t.checkWebdriverIOPreflight
	default:
		t.preflight = t.checkJestPreflight
	}
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
