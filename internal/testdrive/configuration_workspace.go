package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

type configurationExecutor struct {
	commandExecutor
	environment map[string]string
}

func (e configurationExecutor) CombinedOutputInDirectory(ctx context.Context, directory, name string, args []string, env map[string]string) ([]byte, error) {
	merged := mergeConfigurationEnvironment(e.environment, env)
	executor, ok := e.commandExecutor.(directoryExecutor)
	if !ok {
		return nil, fmt.Errorf("command executor cannot set setup working directory")
	}
	return executor.CombinedOutputInDirectory(ctx, directory, name, args, merged)
}

func (e configurationExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	merged := mergeConfigurationEnvironment(e.environment, env)
	return e.commandExecutor.CombinedOutput(ctx, name, args, merged)
}

// Commands still use the existing process executor. The temporary copy gives
// every variant independent manifests, dependencies, test files and build output.
func (t *Testdrive) runPreparedConfiguration(ctx context.Context, output io.Writer, item *configurationResult) (result validationResult, runErr error) {
	runner := *t
	t = &runner
	t.configurationEnvironment = onboard.WithCIProviderDefaults(item.Environment)
	if !t.checkOnly && item.PortOverride == nil {
		if err := checkCypressStartupPorts(t.cypress, t.configurationEnvironment); err != nil {
			return result, err
		}
	}
	if item.Directory == "" {
		item.Directory = "."
	}
	t.configurationLocations = slices.Clone(item.Locations)
	if t.framework != nil && t.framework.Name() == "vitest" && t.usesVitestRunner() {
		t.vitestRunner = true
		var err error
		t.command, err = resolveVitestRunnerBinary(t.repositoryRoot, item.Directory, t.command)
		if err != nil {
			return result, err
		}
	}
	if len(item.UnvalidatedSetup) > 0 {
		return result, fmt.Errorf("configuration setup requires review before tests: %s; use the resolved --all plan or supply the requested setup facts", strings.Join(item.UnvalidatedSetup, "; "))
	}
	original, copyRoot := t.repositoryRoot, t.repositoryRoot
	checkout := t.checkoutRoot()
	sourceRoot := original
	if t.lang().isolatesConfigurations() {
		sourceRoot = checkout
	}
	if t.lang().isolatesConfigurations() || !t.checkOnly && (len(item.Prerequisites) > 0 || len(item.Services) > 0 || t.usesVitestRunner() || t.framework.Name() == "mocha" || t.framework.Name() == "playwright" || t.framework.Name() == "cypress" || t.framework.Name() == "cucumber" || t.framework.Name() == "webdriverio") {
		parent, err := os.MkdirTemp("", "ddtest-configuration-")
		if err != nil {
			return result, err
		}
		defer func() {
			err := workspace.RemoveMarkedStorage(parent)
			if err != nil {
				result.Cleanup = &verdict{Status: "failed", Reason: "Could not remove configuration workspace: " + parent}
			}
			runErr = errors.Join(runErr, err)
		}()
		if err := workspace.MarkStorage(parent, t.ownershipRoot()); err != nil {
			return result, err
		}
		parent, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return result, err
		}
		copyCheckout := filepath.Join(parent, "checkout")
		relative, err := filepath.Rel(sourceRoot, original)
		if err != nil || !filepath.IsLocal(relative) {
			return result, fmt.Errorf("configuration directory is outside the checkout: %s", original)
		}
		copyRoot = filepath.Join(copyCheckout, relative)
		// Without preparation to replay, nothing rebuilds ignored outputs such as
		// a locally compiled dist/, so the copy keeps the prepared state as is.
		skipIgnored := skipIgnoredOutput
		if len(item.Prerequisites) == 0 {
			skipIgnored = nil
		}
		size, err := configurationCopyEstimate(ctx, sourceRoot, skipIgnored)
		if err != nil {
			return result, err
		}
		if err := checkConfigurationSpace(parent, size); err != nil {
			return result, err
		}
		_, _ = fmt.Fprintf(output, "Isolating configuration: up to %d bytes; copy-on-write when available.\n", size)
		if err := workspace.Copy(ctx, sourceRoot, copyCheckout, skipIgnored); err != nil {
			return result, err
		}
		checkout = copyCheckout
	}
	cwd, err := os.Getwd()
	if err != nil {
		return result, err
	}
	if err := os.Chdir(copyRoot); err != nil {
		return result, err
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(cwd)) }()
	if filepath.IsAbs(t.command) && copyRoot != original && !t.vitestRunner {
		if relative, err := filepath.Rel(sourceRoot, t.command); err == nil && filepath.IsLocal(relative) {
			t.command = filepath.Join(checkout, relative)
		}
	}
	t.repositoryRoot, t.configurationRoot = copyRoot, checkout
	t.configurationSourceRoot = sourceRoot
	t.isolated = copyRoot != original
	executor := t.executor
	// Earlier setup must not inherit variables declared only on the test step.
	t.executor = configurationExecutor{executor, isolatedCIEnvironment(nil, sourceRoot, checkout)}
	if err := t.runBuildPrerequisites(ctx, output, item); err != nil {
		result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration preparation failed; tests were not started."}
		return result, err
	}

	if item.PortOverride != nil && !t.checkOnly {
		if err := configurePortFact(t.repositoryRoot, &item.TestCommand, *item.PortOverride, true); err != nil {
			return result, fmt.Errorf("application port override unavailable immediately before startup: %w", err)
		}
		t.cypress = item.Cypress
		if err := checkCypressStartupPorts(t.cypress, item.Environment); err != nil {
			return result, fmt.Errorf("application port was taken before startup; retry full validation without stopping the other listener: %w", err)
		}
	}
	ctx, stop, err := t.startSetupServices(ctx, output, item)
	defer func() {
		if err := stop(); err != nil {
			result.Cleanup = &verdict{Status: "failed", Reason: err.Error()}
			runErr = errors.Join(runErr, err)
		}
	}()
	if err != nil {
		return result, err
	}
	t.configurationEnvironment = isolatedCIEnvironment(onboard.WithCIProviderDefaults(item.Environment), sourceRoot, checkout)
	if err := t.lang().prepareConfiguration(t, item, original, copyRoot); err != nil {
		return result, err
	}
	t.executor = configurationExecutor{executor, t.configurationEnvironment}
	directory, err := localSetupDirectory(copyRoot, item.Directory)
	if err != nil {
		return result, err
	}
	if err := os.Chdir(directory); err != nil {
		return result, err
	}
	t.repositoryRoot = directory
	// Resolve bare framework tools from this package, but never replace an explicit runtime.
	t.command = t.configurationExecutable(t.command)
	if t.framework != nil {
		policy := frameworkFeaturePolicies[t.framework.Name()]
		if err := validateConfigurationFiles(t.checkoutRoot(), t.repositoryRoot, t.args, policy.ConfigurationFiles, policy.configurationOptions()); err != nil {
			return result, err
		}
	}
	t.lang().refreshProjectTracer(t, ctx)
	if t.preflight != nil {
		t.preflight = t.frameworkPreflight()
	}
	result, runErr = t.runPreparedChecksOrTests(ctx, output)
	if copyRoot != original {
		scrubCopyPaths(&result, checkout)
	}
	return result, runErr
}

// scrubCopyPaths reports commands relative to the checkout, never through
// the disposable copy's temporary path.
func scrubCopyPaths(result *validationResult, copyRoot string) {
	roots := []string{copyRoot + string(filepath.Separator)}
	if canonical, err := filepath.EvalSymlinks(copyRoot); err == nil && canonical != copyRoot {
		roots = append(roots, canonical+string(filepath.Separator))
	}
	scrub := func(text string) string {
		for _, root := range roots {
			text = strings.ReplaceAll(text, root, "")
		}
		return text
	}
	if result.Preflight != nil {
		result.Preflight.Command = scrub(result.Preflight.Command)
	}
	for i := range result.Runs {
		result.Runs[i].Command = scrub(result.Runs[i].Command)
		result.Runs[i].NativeCommand = scrub(result.Runs[i].NativeCommand)
	}
}

// Tracers resolve repository-relative source and skip paths against the CI
// checkout. A disposable copy must advertise its own root while retaining the
// provider, commit, job and other CI metadata.
func isolatedCIEnvironment(overrides map[string]string, original, copyRoot string) map[string]string {
	result := maps.Clone(overrides)
	if result == nil {
		result = map[string]string{}
	}
	for _, name := range environment.CIWorkspaceEnvironmentVariables() {
		value, configured := overrides[name]
		// Names such as WORKSPACE can also be customer settings on another CI.
		// Only an explicit reference to this checkout denotes a path to rebase.
		if configured && (!filepath.IsAbs(value) || workspace.CanonicalRoot(value) != workspace.CanonicalRoot(original)) {
			continue
		}
		if !configured {
			value = os.Getenv(name)
		}
		if value != "" {
			result[name] = copyRoot
		}
	}
	return result
}

// skipIgnoredOutput excludes Git-ignored output and cache trees from a copy.
// Installed dependency trees, Yarn PnP state, nested repositories and local
// environment files are inputs to a JavaScript run even when Git ignores them.
func skipIgnoredOutput(path string) bool {
	parts := strings.Split(path, string(filepath.Separator))
	return !slices.Contains(parts, "node_modules") && !slices.Contains(parts, ".git") && !slices.Contains(parts, ".yarn") && !strings.HasPrefix(filepath.Base(path), ".pnp.") && !strings.HasPrefix(filepath.Base(path), ".env")
}

func configurationCopyEstimate(ctx context.Context, source string, skipIgnored func(string) bool) (int64, error) {
	var size int64
	err := workspace.Walk(ctx, source, skipIgnored, func(_, _ string, entry fs.DirEntry) error {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
func checkConfigurationSpace(directory string, needed int64) error {
	free, err := workspace.FreeSpace(directory)
	if err != nil {
		return fmt.Errorf("check configuration disk space: %w", err)
	}
	if needed < 0 || uint64(needed) > free {
		return fmt.Errorf("insufficient disk space for configuration copy: need up to %d bytes, available %d bytes", needed, free)
	}
	return nil
}

// fileDescriptorsExhaustedCode marks a run stopped by the process or system
// open-file limit, typically while copying a large node_modules.
const fileDescriptorsExhaustedCode = "file_descriptors_exhausted"

// A package command executes in its package directory while telemetry source
// paths and cross-configuration native identities remain checkout-relative.
func (t *Testdrive) checkoutRoot() string {
	if t.configurationRoot != "" {
		return t.configurationRoot
	}
	for root := t.repositoryRoot; ; root = filepath.Dir(root) {
		if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
			return root
		}
		if root == filepath.Dir(root) {
			break
		}
	}
	return t.repositoryRoot
}

func (t *Testdrive) runPreparedChecksOrTests(ctx context.Context, output io.Writer) (result validationResult, runErr error) {
	if !t.checkOnly {
		return t.run(ctx, output)
	}
	result = validationResult{CheckOnly: true, Framework: t.framework.Name(), Tracer: t.tracerLabel, Compatibility: verdict{Status: onboard.StatusNotExercised, Reason: "Configuration checks only."}}
	root := t.repositoryRoot
	if t.workflowRoot != "" {
		root = t.workflowRoot
	}
	check := checkConfigurationCIRuntimes(ctx, root, t.framework.Name())
	if t.workflowRoot != "" {
		check = frameworkCIRuntimes(ctx, root, t.framework.Name())
	}
	result.CIRuntime = &check
	t.defaultToCITracer(check)
	result.Tracer = t.tracerLabel
	if t.preflight != nil {
		runErr = t.preflight(ctx, output, &result)
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	return result, runErr
}

func mergeConfigurationEnvironment(layers ...map[string]string) map[string]string {
	result := map[string]string{}
	for _, layer := range layers {
		maps.Copy(result, layer)
	}
	return result
}

// releaseCheckCopy removes the owned copy a coordinated validation shared
// across its checks. Startup recovery reclaims it if removal fails.
func (t *Testdrive) releaseCheckCopy() {
	if t.checkCopy != "" {
		_ = workspace.RemoveMarkedStorage(t.checkCopy)
		t.checkCopy = ""
	}
	t.reuseCheckCopy = false
}
