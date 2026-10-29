package testdrive

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// validationLanguage is everything testdrive needs from one language. Shared
// orchestration (Prepare, Run, Workflow, reports) calls only these hooks; each
// language keeps its specifics in its own files and registers in languages.go.
type validationLanguage interface {
	name() string
	// frameworks lists the test frameworks this language validates.
	frameworks() []string
	// displayNames labels the language and its frameworks in output.
	displayNames() map[string]string
	// tracerPackage names the tracer distribution in labels ("<package>@<version>").
	tracerPackage() string
	// installedTracerVersion extracts the version from the platform's detection output.
	installedTracerVersion(detected string) string

	// prepareStorage runs before discovery and fails early on unusable storage.
	prepareStorage() error
	// selectCommand resolves the command a run without --all validates.
	// handled is false when the framework's default command applies.
	selectCommand(request commandRequest) (command string, args []string, handled bool, err error)
	// detectsTracerInPreparation reports whether Prepare looks for a project
	// tracer in the original checkout. Languages whose detection loads project
	// code detect it later, inside the disposable copy.
	detectsTracerInPreparation() bool
	// refreshProjectTracer detects the project tracer again inside each
	// configuration's copy. Missing project dependencies select the normal
	// temporary fallback; a tracer resolved elsewhere is never retained.
	refreshProjectTracer(t *Testdrive, ctx context.Context)
	// usesPackageBinaries reports whether a bare configuration command
	// resolves to the package's locally installed binaries.
	usesPackageBinaries() bool
	// inspectEnvironment resolves the runtime that runs the selected command.
	// It may replace the command and returns a finding when tests cannot run.
	inspectEnvironment(ctx context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard)
	// parsesCommandAssignments reports whether leading VAR=value words of the
	// selected command are its environment.
	parsesCommandAssignments() bool
	// tracerCommand selects the executable the tracer installer inspects.
	tracerCommand(info environmentInfo, command string, args []string) (string, []string)

	// installPreview describes a temporary tracer installation. Empty prints
	// the install command instead.
	installPreview(label string) string
	// previewNotes adds the language's own steps to the run preview.
	previewNotes() []string
	// installTracer installs a temporary tracer. handled is false when the
	// platform installer applies.
	installTracer(t *Testdrive, ctx context.Context, options platform.TracerOptions) (installation platform.TracerInstallation, handled bool, err error)
	// dependencyChanges lists the root manifests a temporary installation may
	// change. A non-empty note replaces that listing.
	dependencyChanges(projectTracer string) (patterns []string, note string)
	// installDirectory returns the folder a temporary tracer is installed into.
	installDirectory(sessionDirectory, repositoryRoot string) (string, func() error, error)
	// projectTracerPath is the preload path of a project-installed tracer, if any.
	projectTracerPath(projectTracer string) string
	// temporaryTracerSource describes a tracer ddtest installed for the run.
	temporaryTracerSource() string
	// runEnvironment adds the variables that load the tracer into a run.
	runEnvironment(t *Testdrive, path string) map[string]string
	// noResultsFinding refines the finding for a run without test results.
	noResultsFinding(runtime reportRuntime, noEvents, commandFailed bool, card *reportCard, advice *intake.Advice)

	// pairedFrameworks returns the paired-validation adapters of the
	// language's frameworks, keyed by framework name.
	pairedFrameworks() map[string]pairedFramework
	// isolatesConfigurations reports whether every configuration runs in a
	// disposable copy, even configuration-only checks.
	isolatesConfigurations() bool
	// prepareConfiguration adjusts a configuration's environment before it
	// runs, for example to point the runtime at manifests in the copy.
	prepareConfiguration(t *Testdrive, item *configurationResult, original, copyRoot string) error

	// coordinated reports whether the agent coordinator and automatic CI
	// setup support the language.
	coordinated() bool
	// defaultsToCITracer reports whether a tracer every CI job pins becomes
	// the default release.
	defaultsToCITracer() bool
	// ciRuntimes inspects the runtimes and tracer selections of CI jobs for
	// configuration-only checks.
	ciRuntimes(ctx context.Context, root, framework string) onboard.RuntimeCheck
	// ciRuntimeName names the runtime CI jobs select, as in "Node 20".
	ciRuntimeName() string
	// configurationsCIRuntime is the CI runtime inventory --all starts from,
	// before any configuration runs; nil starts from none.
	configurationsCIRuntime(t *Testdrive, scope onboard.ValidationScope) *onboard.RuntimeCheck
	// mergeCIRuntime folds one configuration's CI runtime check into the
	// aggregate. Languages whose every check covers all jobs keep the latest.
	mergeCIRuntime(current, next *onboard.RuntimeCheck) *onboard.RuntimeCheck
	// labelsTracerWithoutSelection reports whether a configuration's tracer
	// label identifies its release even without a registry selection.
	labelsTracerWithoutSelection() bool

	// workflowRuntime is the language runtime, besides Node, that a
	// coordinated report is bound to; empty binds none. An error means the
	// runtime running the selected commands cannot be inspected.
	workflowRuntime(t *Testdrive) (string, error)
	// reportRuntimeCurrent reports whether the runtime a saved coordinated
	// report is bound to still runs on this host.
	reportRuntimeCurrent(root string, evidence WorkflowEvidence) bool
	// packageManager names the package manager in a coordinated plan.
	packageManager(root string) string
	// ciPinnedTracer is the tracer release one CI job pins, if any.
	ciPinnedTracer(job onboard.RuntimeFinding) string
	// dependencyReadiness returns the commands that set up the missing
	// dependencies of the selected configurations (every one in scope with
	// --all), checked before anything runs. None lets validation start.
	dependencyReadiness(t *Testdrive, ctx context.Context, scope onboard.ValidationScope) ([][]string, error)
	// extraInputFingerprint is the language's inputs, beyond the shared source
	// snapshot, that a coordinated report is bound to; empty adds none.
	extraInputFingerprint(ctx context.Context, root string) (string, error)
	// packageExclusions checks that the language's packages leave out the
	// reports. handled is false when the shared package check applies.
	packageExclusions(t *Testdrive, ctx context.Context) (check onboard.WorkflowCheck, handled bool)
	// initialization describes framework's CI tracer initialization; empty
	// uses the shared description.
	initialization(framework string) string
}

// sharedWorkflowHooks is the coordinated-validation behavior every language
// gets unless it binds its own runtime, inputs or packaging checks.
type sharedWorkflowHooks struct{}

func (sharedWorkflowHooks) workflowRuntime(*Testdrive) (string, error) { return "", nil }
func (sharedWorkflowHooks) reportRuntimeCurrent(_ string, evidence WorkflowEvidence) bool {
	return evidence.Node == currentNodeVersion()
}
func (sharedWorkflowHooks) packageManager(root string) string { return workflowPackageManager(root) }
func (sharedWorkflowHooks) ciPinnedTracer(job onboard.RuntimeFinding) string {
	return strings.TrimPrefix(job.Tracer, "dd-trace@")
}
func (sharedWorkflowHooks) dependencyReadiness(*Testdrive, context.Context, onboard.ValidationScope) ([][]string, error) {
	return nil, nil
}
func (sharedWorkflowHooks) extraInputFingerprint(context.Context, string) (string, error) {
	return "", nil
}
func (sharedWorkflowHooks) packageExclusions(*Testdrive, context.Context) (onboard.WorkflowCheck, bool) {
	return onboard.WorkflowCheck{}, false
}
func (sharedWorkflowHooks) initialization(string) string { return "" }

// commandRequest is what selectCommand can use to choose a command.
type commandRequest struct {
	root     string
	runner   framework.Framework
	override string
	all      bool
}

// environmentRequest is what inspectEnvironment can use to resolve a runtime.
type environmentRequest struct {
	root           string
	command        string
	args           []string
	explicit       bool
	checkOnly      bool
	discoveryError error
}

// pairedFramework is one framework's part of paired validation.
type pairedFramework struct {
	// run executes one baseline, reporting-only or probe variant.
	run func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, preload string, spec runSpec) (validationRun, error)
	// features runs the feature probes after a compatible pair.
	features func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, preload string, baseline validationRun, result *validationResult) error
	// preflight checks the framework's own prerequisites; nil has none.
	preflight func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error
	// telemetry matches native results to reported telemetry; nil uses
	// defaultTelemetry.
	telemetry func(validationRun) verdict
	// probeTelemetry matches a probe control's native result to its
	// telemetry; nil uses the shared file-based check.
	probeTelemetry func(validationRun) verdict
	// preflightAfterInstall runs preflight again once the tracer is
	// installed, to inspect the dependencies that load it.
	preflightAfterInstall bool
	// verifyInstallation confirms that the selected runtime loads the selected tracer.
	verifyInstallation func(t *Testdrive, ctx context.Context, preload string) error
	// missingCoverage is the blocking check when real-suite coverage is
	// missing; empty uses the default.
	missingCoverage string
	// outcomeTotals adds the reporting-only native outcomes summed across
	// configurations to the handoff.
	outcomeTotals bool
}

// Neutral names for the paired-validation building blocks every language
// shares; the originals are named after the framework that introduced them.
type (
	nativeTest     = jestTest
	preflightCheck = jestPreflight
)

// defaultTelemetry matches native results to telemetry by file, name and status.
func defaultTelemetry(run validationRun) verdict { return verifyJestTelemetry(run) }

// repositoryCoverageFile returns the checkout-relative path of a covered
// file, and whether it is an existing file inside the checkout.
func repositoryCoverageFile(root, file string) (string, bool) {
	return cucumberCoverageFile(root, file)
}

// applicationSourceCoverage summarizes the coverage of existing application
// files, excluding the native test files themselves.
func applicationSourceCoverage(root string, facts intake.Facts, native []nativeTest) *coverageSummary {
	return cypressSourceCoverage(root, cypressTestCode{}, facts, native)
}

// languageNamed returns a registered language, or nil.
func languageNamed(name string) validationLanguage {
	for _, language := range validationLanguages {
		if language.name() == name {
			return language
		}
	}
	return nil
}

// languageForFramework returns the language that validates framework, or nil.
func languageForFramework(framework string) validationLanguage {
	for _, language := range validationLanguages {
		for _, name := range language.frameworks() {
			if name == framework {
				return language
			}
		}
	}
	return nil
}

// pairedFrameworkNamed returns the paired-validation adapter of framework.
func pairedFrameworkNamed(framework string) (pairedFramework, bool) {
	language := languageForFramework(framework)
	if language == nil {
		return pairedFramework{}, false
	}
	adapter, ok := language.pairedFrameworks()[framework]
	return adapter, ok
}

// lang is the selected language; a run without a registered one gets
// unsupportedLanguage, which adds nothing.
func (t *Testdrive) lang() validationLanguage {
	if language := languageNamed(t.language); language != nil {
		return language
	}
	return unsupportedLanguage{}
}

// unsupportedLanguage is selected when no registered language matches. It adds
// nothing to a run and supports no coordination.
type unsupportedLanguage struct{ sharedWorkflowHooks }

func (unsupportedLanguage) name() string                         { return "" }
func (unsupportedLanguage) frameworks() []string                 { return nil }
func (unsupportedLanguage) displayNames() map[string]string      { return nil }
func (unsupportedLanguage) tracerPackage() string                { return "" }
func (unsupportedLanguage) installedTracerVersion(string) string { return "" }
func (unsupportedLanguage) prepareStorage() error                { return nil }
func (unsupportedLanguage) selectCommand(commandRequest) (string, []string, bool, error) {
	return "", nil, false, nil
}
func (unsupportedLanguage) detectsTracerInPreparation() bool                 { return true }
func (unsupportedLanguage) refreshProjectTracer(*Testdrive, context.Context) {}
func (unsupportedLanguage) usesPackageBinaries() bool                        { return true }
func (unsupportedLanguage) previewNotes() []string                           { return nil }
func (unsupportedLanguage) installTracer(*Testdrive, context.Context, platform.TracerOptions) (platform.TracerInstallation, bool, error) {
	return platform.TracerInstallation{}, false, nil
}
func (unsupportedLanguage) inspectEnvironment(_ context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard) {
	return request.command, request.args, environmentInfo{}, nil
}
func (unsupportedLanguage) parsesCommandAssignments() bool { return false }
func (unsupportedLanguage) tracerCommand(_ environmentInfo, command string, args []string) (string, []string) {
	return command, args
}
func (unsupportedLanguage) installPreview(string) string                { return "" }
func (unsupportedLanguage) dependencyChanges(string) ([]string, string) { return nil, "" }
func (unsupportedLanguage) installDirectory(sessionDirectory, _ string) (string, func() error, error) {
	return sessionDirectory, nil, nil
}
func (unsupportedLanguage) projectTracerPath(string) string                                         { return "" }
func (unsupportedLanguage) temporaryTracerSource() string                                           { return "temporary installation" }
func (unsupportedLanguage) runEnvironment(*Testdrive, string) map[string]string                     { return nil }
func (unsupportedLanguage) noResultsFinding(reportRuntime, bool, bool, *reportCard, *intake.Advice) {}
func (unsupportedLanguage) pairedFrameworks() map[string]pairedFramework                            { return nil }
func (unsupportedLanguage) isolatesConfigurations() bool                                            { return false }
func (unsupportedLanguage) prepareConfiguration(*Testdrive, *configurationResult, string, string) error {
	return nil
}
func (unsupportedLanguage) coordinated() bool        { return false }
func (unsupportedLanguage) defaultsToCITracer() bool { return false }
func (unsupportedLanguage) ciRuntimes(ctx context.Context, root, framework string) onboard.RuntimeCheck {
	return onboard.CheckCIRuntimes(ctx, root, framework)
}
func (unsupportedLanguage) ciRuntimeName() string { return "Node" }
func (unsupportedLanguage) configurationsCIRuntime(*Testdrive, onboard.ValidationScope) *onboard.RuntimeCheck {
	return nil
}
func (unsupportedLanguage) mergeCIRuntime(_, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	return next
}
func (unsupportedLanguage) labelsTracerWithoutSelection() bool { return false }

// frameworkCIRuntimeName names the runtime CI jobs select for framework.
func frameworkCIRuntimeName(framework string) string {
	if language := languageForFramework(framework); language != nil {
		return language.ciRuntimeName()
	}
	return unsupportedLanguage{}.ciRuntimeName()
}

// frameworkCIRuntimes inspects CI runtimes with the framework's language.
func frameworkCIRuntimes(ctx context.Context, root, framework string) onboard.RuntimeCheck {
	if language := languageForFramework(framework); language != nil {
		return language.ciRuntimes(ctx, root, framework)
	}
	return onboard.CheckCIRuntimes(ctx, root, framework)
}

// languageOrUnsupported returns framework's language; unsupportedLanguage when
// none validates it.
func languageOrUnsupported(framework string) validationLanguage {
	if language := languageForFramework(framework); language != nil {
		return language
	}
	return unsupportedLanguage{}
}

// workflowFingerprint is the source snapshot a coordinated report for
// framework is bound to: the shared inputs plus the language's own.
func workflowFingerprint(ctx context.Context, root, framework string) (string, error) {
	fingerprint, err := InputFingerprint(ctx, root)
	if err != nil {
		return "", err
	}
	extra, err := languageOrUnsupported(framework).extraInputFingerprint(ctx, root)
	if err != nil || extra == "" {
		return fingerprint, err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint+"\x00"+extra))), nil
}

// Coordinated reports whether the agent coordinator validates language.
func Coordinated(language string) bool {
	registered := languageNamed(language)
	return registered != nil && registered.coordinated()
}
