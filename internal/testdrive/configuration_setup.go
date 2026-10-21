package testdrive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Preserve unrelated external and dangling links in a snapshot, but never load a
// declared configuration through a link that escapes its checkout.
func validateConfigurationFiles(root, directory string, args, names []string) error {
	paths := []string{}
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			paths = append(paths, name)
		}
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, "--config=") {
			paths = append(paths, strings.TrimPrefix(arg, "--config="))
		}
		if strings.HasPrefix(arg, "-c") && !strings.HasPrefix(arg, "--") && len(arg) > 2 {
			paths = append(paths, strings.TrimPrefix(arg[2:], "="))
		}
		if (arg == "--config" || arg == "-c") && i+1 < len(args) {
			paths = append(paths, args[i+1])
		}
	}
	for _, path := range paths {
		if strings.HasPrefix(strings.TrimSpace(path), "{") {
			continue
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(directory, full)
		}
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return fmt.Errorf("configuration file unavailable; review %s: %w", path, err)
		}
		relative, err := filepath.Rel(canonicalWorkflowRoot(root), resolved)
		if err != nil || !filepath.IsLocal(relative) {
			return fmt.Errorf("configuration file resolves outside checkout; review %s", path)
		}
	}
	return nil
}
