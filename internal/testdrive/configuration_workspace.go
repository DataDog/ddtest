package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
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
	t.configurationEnvironment = onboard.WithGitHubActionsDefaults(item.Environment)
	if !t.checkOnly && item.PortOverride == nil {
		if err := checkCypressStartupPorts(t.cypress, t.configurationEnvironment); err != nil {
			return result, err
		}
	}
	if item.Directory == "" {
		item.Directory = "."
	}
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
	original, workspace := t.repositoryRoot, t.repositoryRoot
	checkout := t.checkoutRoot()
	if t.language == "ruby" || !t.checkOnly && (len(item.Prerequisites) > 0 || len(item.Services) > 0 || t.usesVitestRunner() || t.framework.Name() == "mocha" || t.framework.Name() == "playwright" || t.framework.Name() == "cypress" || t.framework.Name() == "cucumber" || t.framework.Name() == "webdriverio") {
		parent, err := os.MkdirTemp("", "ddtest-configuration-")
		if err != nil {
			return result, err
		}
		defer func() {
			err := removeMarkedStorage(parent)
			if err != nil {
				result.Cleanup = &verdict{Status: "failed", Reason: "Could not remove configuration workspace: " + parent}
			}
			runErr = errors.Join(runErr, err)
		}()
		if err := markSessionStorage(parent, t.ownershipRoot()); err != nil {
			return result, err
		}
		parent, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return result, err
		}
		workspace = filepath.Join(parent, "checkout")
		// Without preparation to replay, nothing rebuilds ignored outputs such as
		// a locally compiled dist/, so the copy keeps the prepared state as is.
		keepIgnored := len(item.Prerequisites) == 0
		size, err := configurationCopyEstimate(ctx, original, keepIgnored)
		if err != nil {
			return result, err
		}
		if err := checkConfigurationSpace(parent, size); err != nil {
			return result, err
		}
		_, _ = fmt.Fprintf(output, "Isolating configuration: up to %d bytes; copy-on-write when available.\n", size)
		if err := copyConfiguration(ctx, original, workspace, keepIgnored); err != nil {
			return result, err
		}
		checkout = workspace
	}
	cwd, err := os.Getwd()
	if err != nil {
		return result, err
	}
	if err := os.Chdir(workspace); err != nil {
		return result, err
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(cwd)) }()
	if filepath.IsAbs(t.command) && workspace != original && !t.vitestRunner {
		if relative, err := filepath.Rel(original, t.command); err == nil && filepath.IsLocal(relative) {
			t.command = filepath.Join(workspace, relative)
		}
	}
	t.repositoryRoot, t.configurationRoot = workspace, checkout
	t.configurationSourceRoot = original
	t.isolated = workspace != original
	executor := t.executor
	// Earlier setup must not inherit variables declared only on the test step.
	t.executor = configurationExecutor{executor, isolatedCIEnvironment(nil, original, checkout)}
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
	t.configurationEnvironment = isolatedCIEnvironment(onboard.WithGitHubActionsDefaults(item.Environment), original, checkout)
	if t.language == "ruby" {
		gemfile := t.configurationEnvironment["BUNDLE_GEMFILE"]
		if gemfile == "" {
			gemfile = os.Getenv("BUNDLE_GEMFILE")
		}
		if filepath.IsAbs(gemfile) {
			canonical, err := filepath.EvalSymlinks(gemfile)
			if err != nil {
				return result, err
			}
			relative, err := filepath.Rel(canonicalWorkflowRoot(original), canonical)
			if err != nil || !filepath.IsLocal(relative) {
				return result, fmt.Errorf("BUNDLE_GEMFILE is outside the repository; use a repository-local Gemfile for isolated Ruby validation")
			}
			t.configurationEnvironment["BUNDLE_GEMFILE"] = filepath.Join(workspace, relative)
		}
	}
	t.executor = configurationExecutor{executor, t.configurationEnvironment}
	directory, err := localSetupDirectory(workspace, item.Directory)
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
	t.refreshConfigurationTracer(ctx)
	if t.preflight != nil {
		t.preflight = t.frameworkPreflight()
	}
	return t.runPreparedChecksOrTests(ctx, output)
}

// Tracers resolve repository-relative source and skip paths against the CI
// checkout. A disposable copy must advertise its own root while retaining the
// provider, commit, job and other CI metadata.
func isolatedCIEnvironment(overrides map[string]string, original, workspace string) map[string]string {
	result := maps.Clone(overrides)
	if result == nil {
		result = map[string]string{}
	}
	for _, name := range environment.CIWorkspaceEnvironmentVariables() {
		value, configured := overrides[name]
		// Names such as WORKSPACE can also be customer settings on another CI.
		// Only an explicit reference to this checkout denotes a path to rebase.
		if configured && (!filepath.IsAbs(value) || canonicalWorkflowRoot(value) != canonicalWorkflowRoot(original)) {
			continue
		}
		if !configured {
			value = os.Getenv(name)
		}
		if value != "" {
			result[name] = workspace
		}
	}
	return result
}

// Walk without following links. Ignored output/cache trees are excluded unless
// keepIgnored is set; installed dependencies and Git metadata are retained for
// builds and identities.
func walkConfiguration(ctx context.Context, source string, keepIgnored bool, visit func(string, string, fs.DirEntry) error) error {
	ignored := map[string]bool{}
	if !keepIgnored {
		ignored = configurationIgnoredPaths(ctx, source)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".testoptimization" || ignored[relative] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return visit(path, relative, entry)
	})
}
func configurationIgnoredPaths(ctx context.Context, source string) map[string]bool {
	ignored := map[string]bool{}
	// Git may be absent in a disposable fixture; then preserve all source inputs.
	command := exec.CommandContext(ctx, "git", "-C", source, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	data, err := command.Output()
	if err != nil {
		return ignored
	}
	for _, path := range strings.Split(string(data), "\x00") {
		path = filepath.FromSlash(strings.TrimSuffix(path, "/"))
		if path == "" || !filepath.IsLocal(path) {
			continue
		}
		parts := strings.Split(path, string(filepath.Separator))
		if slices.Contains(parts, "node_modules") || slices.Contains(parts, ".git") || slices.Contains(parts, ".yarn") || strings.HasPrefix(filepath.Base(path), ".pnp.") || strings.HasPrefix(filepath.Base(path), ".env") {
			continue
		}
		ignored[path] = true
	}
	return ignored
}
func configurationCopyEstimate(ctx context.Context, source string, keepIgnored bool) (int64, error) {
	var size int64
	err := walkConfiguration(ctx, source, keepIgnored, func(_, _ string, entry fs.DirEntry) error {
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
	free, err := configurationFreeSpace(directory)
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

func fileDescriptorsExhausted(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

func copyConfiguration(ctx context.Context, source, target string, keepIgnored bool) error {
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	source = canonical
	destination := canonicalWorkflowRoot(target)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		destination = filepath.Join(canonicalWorkflowRoot(filepath.Dir(target)), filepath.Base(target))
	}
	if relative, err := filepath.Rel(source, destination); err == nil && (relative == "." || filepath.IsLocal(relative)) {
		return fmt.Errorf("configuration copy destination is inside the repository; set TMPDIR outside the repository before retrying")
	}
	return walkConfiguration(ctx, source, keepIgnored, func(path, relative string, entry fs.DirEntry) error {
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm()|0700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			absolute := link
			if !filepath.IsAbs(absolute) {
				absolute = filepath.Join(filepath.Dir(path), link)
			}
			local, relErr := filepath.Rel(source, absolute)
			if relErr == nil && filepath.IsLocal(local) {
				if filepath.IsAbs(link) {
					link = filepath.Join(target, local)
				}
			} else {
				link = absolute
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot isolate special file %s", relative)
		}
		return copyWithDescriptorRetry(func() error { return copyConfigurationFile(path, destination, info) })
	})
}

// descriptorRetryDelays spaces the retries of one file copy after the host
// runs out of open files. Every trial hit this once at a few hundred open
// descriptors: the pressure was host-wide and transient.
var descriptorRetryDelays = []time.Duration{250 * time.Millisecond, time.Second}

func copyWithDescriptorRetry(copyFile func() error) error {
	err := copyFile()
	for _, delay := range descriptorRetryDelays {
		if !fileDescriptorsExhausted(err) {
			return err
		}
		time.Sleep(delay)
		err = copyFile()
	}
	return err
}
func copyConfigurationFile(source, target string, info fs.FileInfo) error {
	cloned, err := cloneConfigurationFile(source, target)
	if err != nil {
		return err
	}
	if !cloned {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0600)
		if err != nil {
			return errors.Join(err, in.Close())
		}
		_, copyErr := io.Copy(out, in)
		if err := errors.Join(copyErr, in.Close(), out.Close()); err != nil {
			return err
		}
	}
	if err := os.Chmod(target, info.Mode().Perm()|0600); err != nil {
		return err
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}

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

func (t *Testdrive) refreshConfigurationTracer(ctx context.Context) {
	if installer, ok := t.platform.(*platform.JavaScript); ok {
		// Missing project dependencies select the normal temporary fallback.
		// Never retain a path resolved in another package or outside this copy.
		t.projectTracer, _ = installer.DetectTracer(ctx, platform.TracerOptions{Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
	}
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
		check = onboard.CheckCIRuntimes(ctx, root, t.framework.Name())
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
