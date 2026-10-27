package testdrive

import (
	"context"
	"io"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// javascriptLanguage validates Node.js test frameworks with dd-trace.
type javascriptLanguage struct{}

func (javascriptLanguage) name() string { return "javascript" }

func (javascriptLanguage) frameworks() []string {
	return []string{"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio"}
}

func (javascriptLanguage) displayNames() map[string]string {
	return map[string]string{"javascript": "JavaScript", "webdriverio": "WebdriverIO", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest"}
}

func (javascriptLanguage) tracerPackage() string { return "dd-trace" }

func (javascriptLanguage) installedTracerVersion(detected string) string {
	return javascriptTracerVersion(detected)
}

// Jest checks keep npm metadata, preflight probes and reports in temporary
// storage; an unusable folder would otherwise surface later as registry failures.
func (javascriptLanguage) prepareStorage() error { return checkTemporaryStorage() }

// Jest and the other runners select their commands in Prepare itself.
func (javascriptLanguage) selectCommand(commandRequest) (string, []string, bool, error) {
	return "", nil, false, nil
}

func (javascriptLanguage) detectsTracerInPreparation() bool { return true }

func (javascriptLanguage) refreshProjectTracer(t *Testdrive, ctx context.Context) {
	if installer, ok := t.platform.(*platform.JavaScript); ok {
		t.projectTracer, _ = installer.DetectTracer(ctx, platform.TracerOptions{Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
	}
}

func (javascriptLanguage) usesPackageBinaries() bool { return true }

func (javascriptLanguage) inspectEnvironment(ctx context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard) {
	var info environmentInfo
	if !request.checkOnly && request.discoveryError == nil {
		info = nodeEnvironmentInfo(ctx, request.command)
	}
	return request.command, request.args, info, nil
}

func (javascriptLanguage) parsesCommandAssignments() bool { return true }

func (javascriptLanguage) tracerCommand(_ environmentInfo, command string, args []string) (string, []string) {
	return command, args
}

func (javascriptLanguage) installPreview(string) string { return "" }

func (javascriptLanguage) previewNotes() []string { return nil }

func (javascriptLanguage) installTracer(*Testdrive, context.Context, platform.TracerOptions) (platform.TracerInstallation, bool, error) {
	return platform.TracerInstallation{}, false, nil
}

func (javascriptLanguage) dependencyChanges(string) ([]string, string) {
	return []string{"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"}, ""
}

func (javascriptLanguage) installDirectory(sessionDirectory, _ string) (string, func() error, error) {
	return sessionDirectory, nil, nil
}

func (javascriptLanguage) projectTracerPath(projectTracer string) string { return projectTracer }

func (javascriptLanguage) temporaryTracerSource() string { return "temporary installation" }

func (javascriptLanguage) runEnvironment(t *Testdrive, path string) map[string]string {
	return t.javascriptEnvironment(path)
}

func (javascriptLanguage) noResultsFinding(reportRuntime, bool, bool, *reportCard, *intake.Advice) {}

func (javascriptLanguage) pairedFrameworks() map[string]pairedFramework {
	return map[string]pairedFramework{
		"jest": {
			run: (*Testdrive).runJest,
			features: func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, preload string, _ validationRun, result *validationResult) error {
				return t.runJestFeatures(ctx, output, session, preload, result)
			},
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkJestPreflight
			},
		},
		"vitest": {
			run:      (*Testdrive).runVitest,
			features: (*Testdrive).runVitestFeatures,
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkVitestPreflight
			},
		},
		"mocha": {
			run:      (*Testdrive).runMocha,
			features: (*Testdrive).runMochaFeatures,
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkMochaPreflight
			},
		},
		"playwright": {
			run:      (*Testdrive).runPlaywright,
			features: (*Testdrive).runPlaywrightFeatures,
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkPlaywrightPreflight
			},
		},
		"cypress": {
			run:      (*Testdrive).runCypress,
			features: (*Testdrive).runCypressFeatures,
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkCypressPreflight
			},
		},
		"cucumber": {
			run:      (*Testdrive).runCucumber,
			features: (*Testdrive).runCucumberFeatures,
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkCucumberPreflight
			},
		},
		"webdriverio": {
			run:      (*Testdrive).runWebdriverIO,
			features: (*Testdrive).runWebdriverIOFeatures,
			preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
				return t.checkWebdriverIOPreflight
			},
		},
	}
}

func (javascriptLanguage) isolatesConfigurations() bool { return false }

func (javascriptLanguage) prepareConfiguration(*Testdrive, *configurationResult, string, string) error {
	return nil
}

func (javascriptLanguage) coordinated() bool { return true }

func (javascriptLanguage) defaultsToCITracer() bool { return true }

func (javascriptLanguage) ciRuntimes(ctx context.Context, root, framework string) onboard.RuntimeCheck {
	return onboard.CheckCIRuntimes(ctx, root, framework)
}

func (javascriptLanguage) ciRuntimeName() string { return "Node" }

func (javascriptLanguage) configurationsCIRuntime(*Testdrive, onboard.ValidationScope) *onboard.RuntimeCheck {
	return nil
}

func (javascriptLanguage) mergeCIRuntime(_, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	return next
}

func (javascriptLanguage) labelsTracerWithoutSelection() bool { return false }
