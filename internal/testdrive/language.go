package testdrive

import (
	"context"
	"io"

	"github.com/DataDog/ddtest/internal/onboard"
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
	// telemetry matches native results to reported telemetry; nil uses the
	// default file/name/status identities.
	telemetry func(validationRun) verdict
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
type unsupportedLanguage struct{}

func (unsupportedLanguage) name() string                         { return "" }
func (unsupportedLanguage) frameworks() []string                 { return nil }
func (unsupportedLanguage) displayNames() map[string]string      { return nil }
func (unsupportedLanguage) tracerPackage() string                { return "" }
func (unsupportedLanguage) installedTracerVersion(string) string { return "" }
func (unsupportedLanguage) prepareStorage() error                { return nil }
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

// frameworkCIRuntimes inspects CI runtimes with the framework's language.
func frameworkCIRuntimes(ctx context.Context, root, framework string) onboard.RuntimeCheck {
	if language := languageForFramework(framework); language != nil {
		return language.ciRuntimes(ctx, root, framework)
	}
	return onboard.CheckCIRuntimes(ctx, root, framework)
}
