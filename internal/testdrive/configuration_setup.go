package testdrive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type directoryExecutor interface {
	CombinedOutputInDirectory(context.Context, string, string, []string, map[string]string) ([]byte, error)
}

func setupOutput(ctx context.Context, executor commandExecutor, root, relative, name string, args []string, env map[string]string) ([]byte, error) {
	directory, err := localSetupDirectory(root, relative)
	if err != nil {
		return nil, err
	}
	if directory == canonicalWorkflowRoot(root) && directory == canonicalWorkflowRoot(currentDirectory()) {
		return executor.CombinedOutput(ctx, name, args, env)
	}
	if e, ok := executor.(directoryExecutor); ok {
		return e.CombinedOutputInDirectory(ctx, directory, name, args, env)
	}
	return nil, fmt.Errorf("command executor cannot set setup working directory %s", relative)
}

func currentDirectory() string { directory, _ := os.Getwd(); return directory }

func localSetupDirectory(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) {
		return "", fmt.Errorf("setup working directory must stay in the repository: %q", relative)
	}
	directory, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	local, err := filepath.Rel(canonicalWorkflowRoot(root), directory)
	if err != nil || !filepath.IsLocal(local) {
		return "", fmt.Errorf("setup working directory escapes repository")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("setup working directory is not a directory")
	}
	return directory, nil
}
