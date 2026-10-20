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

	"github.com/DataDog/ddtest/internal/environment"
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
	t.configurationEnvironment = item.Environment
	if item.Directory == "" {
		item.Directory = "."
	}
	if len(item.Prerequisites) == 0 && item.Directory == "." || t.checkOnly {
		if err := t.runBuildPrerequisites(ctx, output, item); err != nil {
			return result, err
		}
		t.executor = configurationExecutor{t.executor, item.Environment}
		return t.run(ctx, output)
	}
	original := t.repositoryRoot
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
	if filepath.IsAbs(t.command) {
		relative, err := filepath.Rel(original, t.command)
		if err == nil && filepath.IsLocal(relative) {
			t.command = filepath.Join(workspace, relative)
		}
	}
	t.repositoryRoot = workspace
	t.configurationRoot = workspace
	if err := t.runBuildPrerequisites(ctx, output, item); err != nil {
		result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration preparation failed; tests were not started."}
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
	t.executor = configurationExecutor{t.executor, isolatedCIEnvironment(item.Environment, workspace)}
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
