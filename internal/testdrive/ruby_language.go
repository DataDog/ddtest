package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

// rubyLanguage observes RSpec and Minitest suites with datadog-ci.
type rubyLanguage struct{}

func (rubyLanguage) name() string         { return "ruby" }
func (rubyLanguage) frameworks() []string { return []string{"rspec", "minitest"} }
func (rubyLanguage) displayNames() map[string]string {
	return map[string]string{"ruby": "Ruby", "rspec": "RSpec", "minitest": "Minitest"}
}
func (rubyLanguage) tracerPackage() string { return "datadog-ci" }

// Bundler lists the gem as "* datadog-ci (<version>)".
func (rubyLanguage) installedTracerVersion(detected string) string {
	if info, ok := strings.CutPrefix(strings.TrimSpace(detected), "* datadog-ci ("); ok {
		version, _, _ := strings.Cut(info, ")")
		return version
	}
	return ""
}

func (rubyLanguage) prepareStorage() error { return nil }

// A selected CI or user command runs as given. Without one, Minitest's default
// probes Rails through Bundler, which may load the Gemfile; that probe runs
// only in an owned copy.
func (rubyLanguage) selectCommand(request commandRequest) (string, []string, bool, error) {
	var command string
	var args []string
	handled := false
	if request.runner.Name() == "minitest" {
		command, args, handled = "bundle", []string{"exec", "rake", "test"}, true
	}
	if request.all {
		return command, args, handled, nil
	}
	selected, err := onboard.RubyValidationCommand(request.root, request.runner.Name(), request.override)
	if err != nil {
		return command, args, handled, err
	}
	if selected == "" {
		if request.runner.Name() != "minitest" {
			return command, args, handled, nil
		}
		isolator := Testdrive{repositoryRoot: request.root}
		err = isolator.inRubyWorkflowCopy(context.Background(), func(root string) (probeErr error) {
			if err := os.Chdir(root); err != nil {
				return err
			}
			defer func() { probeErr = errors.Join(probeErr, os.Chdir(request.root)) }()
			command, args = request.runner.Command()
			return nil
		})
		return command, args, true, err
	}
	words, err := shellquote.Split(selected)
	if err != nil || len(words) == 0 {
		return command, args, handled, fmt.Errorf("invalid Ruby command: %s", selected)
	}
	return words[0], words[1:], true, nil
}

// Bundler resolves the project tracer, which may evaluate the Gemfile; the
// preflight does it inside the copy instead.
func (rubyLanguage) detectsTracerInPreparation() bool { return false }

// Each configuration may select its own Gemfile and bundle.
func (rubyLanguage) refreshProjectTracer(t *Testdrive, ctx context.Context) {
	if installer, ok := t.platform.(*platform.Ruby); ok {
		// Bundler may evaluate a blocking Gemfile; keep the subprocess bound.
		if t.commandTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, t.commandTimeout)
			defer cancel()
		}
		t.projectTracer, _ = installer.DetectTracer(ctx, platform.TracerOptions{Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
	}
}

func (rubyLanguage) usesPackageBinaries() bool { return false }

func (rubyLanguage) inspectEnvironment(ctx context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard) {
	return request.command, request.args, rubyEnvironmentInfo(ctx), nil
}

func (rubyLanguage) parsesCommandAssignments() bool { return false }

func (rubyLanguage) tracerCommand(_ environmentInfo, command string, args []string) (string, []string) {
	return command, args
}

func (rubyLanguage) installPreview(string) string { return "" }

func (rubyLanguage) previewNotes() []string {
	return []string{
		"compare native baseline/reporting-only results and feature controls in a disposable copy; preserve the original Gemfile and lockfile",
		"check numeric CI Ruby selectors against the resolved tracer; other engines/floating selectors require separate validation or explicit instrumentation exclusions",
	}
}

func (rubyLanguage) installTracer(t *Testdrive, ctx context.Context, options platform.TracerOptions) (platform.TracerInstallation, bool, error) {
	installation, err := t.installRubyTracer(ctx, options)
	return installation, true, err
}

// A temporary installation goes through Bundler, which edits the Gemfile.
func (rubyLanguage) dependencyChanges(projectTracer string) ([]string, string) {
	if projectTracer == "" {
		return nil, "Bundler updates only the disposable copy of Gemfile and Gemfile.lock; the original bundle is preserved."
	}
	return []string{"Gemfile", "Gemfile.lock", "gems.rb", "gems.locked"}, ""
}

func (rubyLanguage) installDirectory(sessionDirectory, _ string) (string, func() error, error) {
	return sessionDirectory, nil, nil
}

func (rubyLanguage) projectTracerPath(string) string { return "" }
func (rubyLanguage) temporaryTracerSource() string   { return "project bundle installation" }

func (rubyLanguage) runEnvironment(t *Testdrive, path string) map[string]string {
	env := rubyEnvironment(path)
	maps.Copy(env, t.tracerEnvironment)
	return env
}

func (rubyLanguage) noResultsFinding(reportRuntime, bool, bool, *reportCard, *intake.Advice) {}

func (rubyLanguage) pairedFrameworks() map[string]pairedFramework {
	adapter := pairedFramework{
		run: func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, preload string, spec runSpec) (validationRun, error) {
			value, err := t.runRuby(ctx, output, session, preload, spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode)
			value.Role = spec.Role
			return value, err
		},
		features: func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, _ string, baseline validationRun, result *validationResult) error {
			return t.runRubyFeatures(ctx, output, session, baseline, result)
		},
		preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
			return t.checkRubyPreflight
		},
		telemetry:             verifyRubyTelemetry,
		preflightAfterInstall: true,
		missingCoverage:       "Ruby application coverage was not observed. Inspect the selected Gemfile/runtime and native suite output; setup failures before tests start cannot validate compatibility or features. Check datadog-ci coverage settings after the native suite runs.",
		outcomeTotals:         true,
	}
	return map[string]pairedFramework{"rspec": adapter, "minitest": adapter}
}

// rubyLocalCommand reports whether a configuration is a local command, which
// alone inherits the shell's BUNDLE_GEMFILE: it has no CI locations and is not
// the sole CI command a plain run selects.
func rubyLocalCommand(root, framework string, locations []string) bool {
	if len(locations) > 0 {
		return false
	}
	if strings.TrimSpace(settings.GetCommand()) == "" {
		if selected, err := onboard.RubyValidationCommand(root, framework, ""); err == nil && selected != "" {
			return false
		}
	}
	return true
}

// Bundler can execute Gemfiles and gemspecs even with a frozen lockfile, so
// every configuration, including configuration-only checks, runs in a copy.
func (rubyLanguage) isolatesConfigurations() bool { return true }

// Point Bundler at the configuration's Gemfile inside the copy. Only a local
// command inherits BUNDLE_GEMFILE from the shell; CI never sees it, including
// the sole CI command a plain run selects.
func (rubyLanguage) prepareConfiguration(t *Testdrive, item *configurationResult, original, workspace string) error {
	gemfile, err := rubyConfigurationGemfile(original, item.Directory, t.configurationEnvironment, rubyLocalCommand(original, t.framework.Name(), item.Locations))
	if err != nil {
		return err
	}
	t.configurationEnvironment["BUNDLE_GEMFILE"] = filepath.Join(workspace, gemfile)
	return nil
}

// The agent coordinator validates manually set up Ruby CI; only automatic
// CI edits remain JavaScript-only.
func (rubyLanguage) coordinated() bool        { return true }
func (rubyLanguage) defaultsToCITracer() bool { return false }

// RubyGems requirements are checked against the installed bundle, so CI
// inspection only inventories the static Ruby and tracer selections.
func (rubyLanguage) ciRuntimes(_ context.Context, root, framework string) onboard.RuntimeCheck {
	return onboard.RubyCIConfiguration(root, framework)
}

func (rubyLanguage) ciRuntimeName() string { return "Ruby" }

func (rubyLanguage) configurationsCIRuntime(t *Testdrive, scope onboard.ValidationScope) *onboard.RuntimeCheck {
	ci := onboard.RubyCIConfiguration(t.checkoutRoot(), t.framework.Name())
	instrumented := false
	for _, entry := range scope.Commands {
		instrumented = instrumented || !ciInstrumentationDisabled(entry)
	}
	if !instrumented {
		ci.Status, ci.Reason = "inconclusive", "Every CI entry excludes instrumentation; at least one compatible instrumented CI consumer is required."
	}
	return &ci
}

func (rubyLanguage) mergeCIRuntime(current, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	return mergeRubyRuntimeChecks(current, next)
}

// The preflight labels the bundled datadog-ci release; there is no registry
// selection.
func (rubyLanguage) labelsTracerWithoutSelection() bool { return true }

// Reports are bound to the Ruby that runs the selected commands, not to Node.
func (rubyLanguage) workflowRuntime(t *Testdrive) (string, error) {
	version := t.workflowRubyVersion()
	if version == "" {
		return "", errors.New("the Ruby runtime on PATH could not be inspected; restore the selected Ruby executable before validation")
	}
	return version, nil
}

func (rubyLanguage) reportRuntimeCurrent(root string, evidence WorkflowEvidence) bool {
	return evidence.Runtime != "" && evidence.Runtime == currentRubyVersion(root)
}

func (rubyLanguage) packageManager(string) string { return "bundler" }

// Ruby CI pins the release in the action's ruby-tracer-version; an excluded
// row installs nothing.
func (rubyLanguage) ciPinnedTracer(job onboard.RuntimeFinding) string {
	if job.Status == "excluded" {
		return ""
	}
	return job.TracerRequested
}

// Without --all, only the selected command's bundle is checked, with the
// same Gemfile selection its run uses.
func (rubyLanguage) dependencyReadiness(t *Testdrive, ctx context.Context, scope onboard.ValidationScope) ([][]string, error) {
	if !t.allConfigurations {
		entry := onboard.TestCommand{Directory: ".", Command: t.command, Environment: t.configurationEnvironment, Locations: t.configurationLocations}
		if len(entry.Locations) == 0 && !rubyLocalCommand(t.repositoryRoot, t.framework.Name(), nil) {
			entry.Locations = []string{"the sole CI command"}
		}
		scope.Commands = []onboard.TestCommand{entry}
	}
	return t.rubyBundleReadiness(ctx, scope)
}

func (rubyLanguage) extraInputFingerprint(ctx context.Context, root string) (string, error) {
	return fingerprintRubyBundles(ctx, root)
}

func (rubyLanguage) packageExclusions(t *Testdrive, ctx context.Context) (onboard.WorkflowCheck, bool) {
	return t.rubyPackageExclusions(ctx), true
}

func (rubyLanguage) initialization(string) string {
	return "Install the pinned datadog-ci using the Ruby CI action after bundle setup; preserve RUBYOPT and the original test command. Keep both reports excluded from Git and gem packaging."
}

func init() {
	features := []featurePolicy{
		{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "required"}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
	}
	for _, name := range (rubyLanguage{}).frameworks() {
		frameworkFeaturePolicies[name] = frameworkPolicy{SourceCoverage: true, ProbeBodyCounts: true, Paired: true, CheckOnly: true, Preflight: true, Configurations: true, Features: slices.Clone(features)}
	}
}
