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
)

type configurationExecutor struct {
	commandExecutor
	environment map[string]string
}

func (e configurationExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	merged := map[string]string{}
	maps.Copy(merged, e.environment)
	maps.Copy(merged, env)
	return e.commandExecutor.CombinedOutput(ctx, name, args, merged)
}

// Commands still use the existing process executor. The temporary copy gives
// every variant independent manifests, dependencies, test files and build output.
func (t *Testdrive) runPreparedConfiguration(ctx context.Context, output io.Writer, item *configurationResult) (result validationResult, runErr error) {
	if len(item.Prerequisites) == 0 || t.checkOnly {
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
	t.command = filepath.Join(workspace, "node_modules", ".bin", filepath.Base(t.command))
	t.repositoryRoot = workspace
	if err := t.runBuildPrerequisites(ctx, output, item); err != nil {
		result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration preparation failed; tests were not started."}
		return result, err
	}
	t.executor = configurationExecutor{t.executor, item.Environment}
	return t.run(ctx, output)
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
