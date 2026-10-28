package testdrive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// pythonLanguage observes pytest suites with ddtrace.
type pythonLanguage struct{ sharedWorkflowHooks }

func (pythonLanguage) name() string         { return "python" }
func (pythonLanguage) frameworks() []string { return []string{"pytest"} }
func (pythonLanguage) displayNames() map[string]string {
	return map[string]string{"python": "Python", "pytest": "pytest"}
}
func (pythonLanguage) tracerPackage() string                         { return "ddtrace" }
func (pythonLanguage) installedTracerVersion(detected string) string { return detected }
func (pythonLanguage) prepareStorage() error                         { return nil }
func (pythonLanguage) selectCommand(commandRequest) (string, []string, bool, error) {
	return "", nil, false, nil
}
func (pythonLanguage) detectsTracerInPreparation() bool                 { return true }
func (pythonLanguage) refreshProjectTracer(*Testdrive, context.Context) {}
func (pythonLanguage) usesPackageBinaries() bool                        { return true }
func (pythonLanguage) previewNotes() []string                           { return nil }
func (pythonLanguage) installTracer(*Testdrive, context.Context, platform.TracerOptions) (platform.TracerInstallation, bool, error) {
	return platform.TracerInstallation{}, false, nil
}

func (pythonLanguage) inspectEnvironment(ctx context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard) {
	command, args, source := pythonTestdriveCommand(request.root, request.command, request.args, request.explicit)
	info, finding := pythonEnvironmentInfo(ctx, command, args, source)
	return command, args, info, finding
}

func (pythonLanguage) parsesCommandAssignments() bool { return false }

// The tracer installer inspects the selected interpreter, not a wrapper.
func (pythonLanguage) tracerCommand(info environmentInfo, command string, args []string) (string, []string) {
	if info.Executable != "" {
		return info.Executable, nil
	}
	return command, args
}

func (pythonLanguage) installPreview(label string) string {
	return fmt.Sprintf("install %s in a temporary Python environment; remove it after the run", label)
}

func (pythonLanguage) dependencyChanges(string) ([]string, string) {
	return []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements*.txt", "requirements*.in", "Pipfile", "Pipfile.lock", "poetry.lock", "uv.lock", "pdm.lock", "pylock.toml"}, ""
}

// The owner marker lets startup recovery remove this folder after a hard kill;
// the environment itself stays in an empty subfolder.
func (pythonLanguage) installDirectory(_, repositoryRoot string) (string, func() error, error) {
	owned, err := os.MkdirTemp("", "ddtest-python-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return workspace.RemoveMarkedStorage(owned) }
	if err := workspace.MarkStorage(owned, repositoryRoot); err != nil {
		return "", cleanup, err
	}
	directory := filepath.Join(owned, "environment")
	if err := os.Mkdir(directory, 0700); err != nil {
		return "", cleanup, err
	}
	return directory, cleanup, nil
}

func (pythonLanguage) projectTracerPath(string) string { return "" }
func (pythonLanguage) temporaryTracerSource() string   { return "temporary installation" }

func (pythonLanguage) runEnvironment(_ *Testdrive, path string) map[string]string {
	return pythonEnvironment(path)
}

// pytest rejects --ddtrace when the ddtrace plugin did not load.
func (pythonLanguage) noResultsFinding(runtime reportRuntime, noEvents, commandFailed bool, card *reportCard, advice *intake.Advice) {
	if !noEvents || !commandFailed || !strings.EqualFold(runtime.Framework, "pytest") {
		return
	}
	for _, line := range strings.Split(runtime.Output, "\n") {
		_, arguments, found := strings.Cut(line, "error: unrecognized arguments:")
		if !found || !slices.Contains(strings.Fields(arguments), "--ddtrace") {
			continue
		}
		card.Kind, card.Title = "pytest-plugin-not-loaded", "pytest did not load the ddtrace plugin"
		card.Context = "pytest rejected --ddtrace because the ddtrace plugin was not loaded. Automatic instrumentation could not start with this project's pytest configuration."
		*advice = intake.Advice{
			Finding: card.Kind, Title: "Load the ddtrace pytest plugin in CI",
			Text: "Check PYTEST_DISABLE_PLUGIN_AUTOLOAD, --disable-plugin-autoload, and -p no:ddtrace in the CI environment and pytest options. If autoloading is intentionally disabled, explicitly load ddtrace once, for example: python -m pytest -p ddtrace --ddtrace. Use the same plugin-loading options for testdrive and CI, then rerun testdrive.",
			URL:  "https://docs.pytest.org/en/stable/how-to/plugins.html#disabling-plugins-from-autoloading",
		}
		return
	}
}

func (pythonLanguage) pairedFrameworks() map[string]pairedFramework { return nil }
func (pythonLanguage) isolatesConfigurations() bool                 { return false }

func (pythonLanguage) prepareConfiguration(*Testdrive, *configurationResult, string, string) error {
	return nil
}
func (pythonLanguage) coordinated() bool        { return false }
func (pythonLanguage) defaultsToCITracer() bool { return false }
func (pythonLanguage) ciRuntimes(ctx context.Context, root, framework string) onboard.RuntimeCheck {
	return onboard.CheckCIRuntimes(ctx, root, framework)
}

func (pythonLanguage) ciRuntimeName() string { return "Python" }

func (pythonLanguage) configurationsCIRuntime(*Testdrive, onboard.ValidationScope) *onboard.RuntimeCheck {
	return nil
}

func (pythonLanguage) mergeCIRuntime(_, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	return next
}

func (pythonLanguage) labelsTracerWithoutSelection() bool { return false }
