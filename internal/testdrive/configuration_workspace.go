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

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/onboard"
)

type configurationExecutor struct {
	commandExecutor
	environment map[string]string
}

type separateOutputExecutor interface {
	Output(context.Context, string, []string, map[string]string) ([]byte, []byte, error)
}

func (e configurationExecutor) CombinedOutputInDirectory(ctx context.Context, directory, name string, args []string, env map[string]string) ([]byte, error) {
	merged := maps.Clone(e.environment)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, env)
	executor, ok := e.commandExecutor.(directoryExecutor)
	if !ok {
		return nil, fmt.Errorf("command executor cannot set setup working directory")
	}
	return executor.CombinedOutputInDirectory(ctx, directory, name, args, merged)
}

func (e configurationExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	merged := map[string]string{}
	maps.Copy(merged, e.environment)
	maps.Copy(merged, env)
	return e.commandExecutor.CombinedOutput(ctx, name, args, merged)
}

func (e configurationExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	merged := map[string]string{}
	maps.Copy(merged, e.environment)
	maps.Copy(merged, env)
	executor, ok := e.commandExecutor.(separateOutputExecutor)
	if !ok {
		return nil, nil, fmt.Errorf("command executor cannot capture stdout and stderr separately")
	}
	return executor.Output(ctx, name, args, merged)
}

// Commands still use the existing process executor. The temporary copy gives
// every variant independent manifests, dependencies, test files and build output.
func (t *Testdrive) runPreparedConfiguration(ctx context.Context, output io.Writer, item *configurationResult) (result validationResult, runErr error) {
	if item.Directory == "" {
		item.Directory = "."
	}
	if !t.checkOnly {
		if err := checkCypressStartupPorts(t.cypress); err != nil {
			return result, err
		}
	}
	if t.language != "ruby" && (len(item.Prerequisites) == 0 && len(item.Services) == 0 && item.Directory == "." && !onboard.IsDDTestCommand(t.command) && t.framework.Name() != "playwright" && t.framework.Name() != "mocha" && t.framework.Name() != "cypress" && t.framework.Name() != "cucumber" && t.framework.Name() != "webdriverio" || t.checkOnly) {
		if err := t.runBuildPrerequisites(ctx, output, item); err != nil {
			return result, err
		}
		t.executor = configurationExecutor{t.executor, item.Environment}
		return t.run(ctx, output)
	}
	original := t.repositoryRoot
	if onboard.IsDDTestCommand(t.command) {
		t.vitestRunner = true
		// Resolve before changing directory. Never substitute a different binary.
		var command string
		var err error
		if t.command == "ddtest" {
			command, err = os.Executable()
		} else {
			command, err = exec.LookPath(t.command)
		}
		if err != nil {
			return result, err
		}
		t.command, err = filepath.Abs(command)
		if err != nil {
			return result, err
		}
	}
	workspace, err := os.MkdirTemp("", "ddtest-configuration-")
	if err != nil {
		return result, err
	}
	defer func() {
		err := os.RemoveAll(workspace)
		if err != nil {
			result.Cleanup = &verdict{Status: "failed", Reason: "Could not remove configuration workspace: " + workspace}
		}
		runErr = errors.Join(runErr, err)
	}()
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return result, err
	}
	if err := copyConfiguration(ctx, original, workspace); err != nil {
		return result, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return result, err
	}
	if err := os.Chdir(workspace); err != nil {
		return result, err
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(cwd)) }()
	if !t.vitestRunner && filepath.IsAbs(t.command) {
		relative, err := filepath.Rel(original, t.command)
		if err == nil && filepath.IsLocal(relative) {
			t.command = filepath.Join(workspace, relative)
		}
	}
	t.repositoryRoot = workspace
	t.isolated = true
	if err := t.runBuildPrerequisites(ctx, output, item); err != nil {
		result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration preparation failed; tests were not started."}
		return result, err
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
	if item.Directory != "." {
		directory, err := localSetupDirectory(workspace, item.Directory)
		if err != nil {
			return result, err
		}
		if err := os.Chdir(directory); err != nil {
			return result, err
		}
		t.repositoryRoot = directory
		t.command = t.configurationExecutable(filepath.Base(t.command))
	}
	environment := isolatedCIEnvironment(item.Environment, workspace)
	if t.language == "ruby" {
		gemfile := environment["BUNDLE_GEMFILE"]
		if gemfile == "" {
			gemfile = os.Getenv("BUNDLE_GEMFILE")
		}
		if filepath.IsAbs(gemfile) {
			canonicalGemfile, err := filepath.EvalSymlinks(gemfile)
			if err != nil {
				return result, err
			}
			canonicalRoot, err := filepath.EvalSymlinks(original)
			if err != nil {
				return result, err
			}
			relative, err := filepath.Rel(canonicalRoot, canonicalGemfile)
			if err != nil || !filepath.IsLocal(relative) {
				return result, fmt.Errorf("BUNDLE_GEMFILE is outside the repository; use a repository-local Gemfile for isolated Ruby validation")
			}
			environment["BUNDLE_GEMFILE"] = filepath.Join(workspace, relative)
		}
	}
	t.executor = configurationExecutor{t.executor, environment}
	return t.run(ctx, output)
}

// Tracers resolve repository-relative source and skip paths against the CI
// checkout. A disposable copy must advertise its own root while retaining the
// provider, commit, job and other CI metadata.
func isolatedCIEnvironment(overrides map[string]string, workspace string) map[string]string {
	result := maps.Clone(overrides)
	if result == nil {
		result = map[string]string{}
	}
	for _, name := range environment.CIWorkspaceEnvironmentVariables() {
		value, configured := overrides[name]
		if !configured {
			value = os.Getenv(name)
		}
		if value != "" {
			result[name] = workspace
		}
	}
	return result
}

func copyConfiguration(ctx context.Context, source, target string) error {
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	source = canonical
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
		if relative == ".testoptimization" {
			return filepath.SkipDir
		}
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
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("cannot isolate symlink %s: %w", relative, err)
			}
			local, err := filepath.Rel(source, resolved)
			if err != nil || !filepath.IsLocal(local) {
				return fmt.Errorf("configuration symlink escapes repository: %s", relative)
			}
			if filepath.IsAbs(link) {
				link = filepath.Join(target, local)
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot isolate special file %s", relative)
		}
		// Git objects can be read-only; the copy must never share writable inodes.
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		return errors.Join(copyErr, out.Close())
	})
}
