package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/bundle"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// go runs ddto's -toolexec once per tool invocation of a build: hundreds of
// times for a module. Each start of ddto's own executable maps and initializes
// its whole package graph, about 1 ms more than a small program that only
// replaces itself with the tool, which is all that most invocations need.
// ddto therefore builds internal/toolwrap into a small executable, once per
// version and host, in its user cache, and gives go that executable instead.
// The executable runs ddto for the invocations that need the plan. ddto itself
// stays the wrapper when the executable cannot be built or run.

const (
	// toolWrapperCheckTimeout bounds the key check of a wrapper executable.
	toolWrapperCheckTimeout = 10 * time.Second
	// toolWrapperWaitDelay bounds the wait for output after the key check or
	// the build ends, or after a signal cancels it.
	toolWrapperWaitDelay = time.Second
)

// toolWrapperBuildEnv follows the build's environment; tests use it to build
// an executable that the host cannot run.
var toolWrapperBuildEnv []string

// ensureToolWrapper returns ddto's wrapper executable for env's host and its
// lock, held shared while the plan's builds may run it so that pruners keep
// it. It builds a missing or unusable executable with the plan's go, under
// the exclusive lock, which waits for runs still using the old one.
func ensureToolWrapper(ctx context.Context, env *goEnvironment, dir string, debug *cliDebug) (path string, lock *os.File, err error) {
	files, sum := toolwrap.Sources(version.BuildLogPrefix)
	parent, err := ddtoCacheDir(env, dir, "toolwraps")
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", nil, err
	}
	key := sum[:16] + "-" + env.GOHOSTOS + "_" + env.GOHOSTARCH
	root := filepath.Join(parent, key)
	path = filepath.Join(root, toolWrapperName(env.GOHOSTOS))
	if lock, err = holdGOROOTMirrorLock(ctx, root+".lock", false); err != nil {
		return "", nil, err
	}
	if validToolWrapper(ctx, path, sum) {
		touchGOROOTMirror(root)
		debug.printf("tool wrapper state=reused path=%q", path)
		return path, lock, nil
	}
	lock.Close()
	// The exclusive lock waits for runs that use another executable here.
	debug.printf("tool wrapper state=stale path=%q", path)
	if lock, err = holdGOROOTMirrorLock(ctx, root+".lock", true); err != nil {
		return "", nil, err
	}
	created := false
	if !validToolWrapper(ctx, path, sum) {
		if err := buildToolWrapper(ctx, env, parent, key, root, files, sum, debug); err != nil {
			lock.Close()
			return "", nil, err
		}
		created = true
	}
	touchGOROOTMirror(root)
	lock.Close()
	if lock, err = holdGOROOTMirrorLock(ctx, root+".lock", false); err != nil {
		return "", nil, err
	}
	// The exclusive lock was released: another run may only have reused it,
	// and its fresh time keeps pruners away.
	if !validToolWrapper(ctx, path, sum) {
		lock.Close()
		return "", nil, errors.New("tool wrapper changed while it was published")
	}
	if created {
		pruneGOROOTMirrors(parent, key)
	} else {
		debug.printf("tool wrapper state=reused path=%q", path)
	}
	return path, lock, nil
}

func toolWrapperName(goos string) string {
	if goos == "windows" {
		return toolwrap.Executable + ".exe"
	}
	return toolwrap.Executable
}

// validToolWrapper reports whether path runs and is the wrapper with key sum.
// The check also fails for an executable that the host cannot run, such as
// one in a directory mounted without execution.
func validToolWrapper(ctx context.Context, path, sum string) bool {
	ctx, cancel := context.WithTimeout(ctx, toolWrapperCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, toolwrap.KeyFlag)
	// A process that it leaves holding the output does not hold the check.
	cmd.WaitDelay = toolWrapperWaitDelay
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == sum
}

// buildToolWrapper replaces the wrapper executable in root. The caller holds
// its exclusive lock.
func buildToolWrapper(ctx context.Context, env *goEnvironment, parent, key, root string, files map[string][]byte, sum string, debug *cliDebug) error {
	started := time.Now()
	if err := removeGOROOTMirror(root); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, key+".tmp-")
	if err != nil {
		return err
	}
	defer removeGOROOTMirror(staging)
	bin := filepath.Join(staging, "bin")
	executable := filepath.Join(bin, toolWrapperName(env.GOHOSTOS))
	source := "bundle"
	if !installBundledToolWrapper(ctx, env, executable, sum, debug) {
		source = "build"
		src := filepath.Join(staging, "src")
		for name, data := range files {
			file := filepath.Join(src, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(file, data, 0600); err != nil {
				return err
			}
		}
		// The wrapper runs on the host, whatever the build targets, and needs
		// none of the caller's flags, workspace or toolchain selection.
		cmd := goCommand(ctx, src, "build", "-buildvcs=false", "-ldflags=-s -w", "-o", executable, ".")
		cmd.Env = append(cmd.Environ(), "GOOS="+env.GOHOSTOS, "GOARCH="+env.GOHOSTARCH,
			goFlagsEnv(""), "GOWORK=off", "GO111MODULE=on", "GOTOOLCHAIN=local")
		cmd.Env = append(cmd.Env, toolWrapperBuildEnv...)
		cmd.WaitDelay = toolWrapperWaitDelay
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("building the tool wrapper: %w: %s", err, bytes.TrimSpace(output))
		}
		if !validToolWrapper(ctx, executable, sum) {
			return errors.New("the built tool wrapper does not run")
		}
	}
	if err := os.Rename(bin, root); err != nil {
		return err
	}
	debug.printf("tool wrapper state=created source=%s path=%q duration_ms=%d", source, filepath.Join(root, toolWrapperName(env.GOHOSTOS)), time.Since(started).Milliseconds())
	return nil
}

// bundledToolWrapper is the wrapper executable that a release build carries
// for the platform it was built for, or nil.
var bundledToolWrapper = bundle.ToolWrapper()

// installBundledToolWrapper writes the wrapper that a release build carries
// when it was built for the host, and reports whether it runs with the
// expected key. Otherwise ddto builds the wrapper from its sources.
func installBundledToolWrapper(ctx context.Context, env *goEnvironment, executable, sum string, debug *cliDebug) bool {
	if len(bundledToolWrapper) == 0 || env.GOHOSTOS != runtime.GOOS || env.GOHOSTARCH != runtime.GOARCH {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		return false
	}
	if err := os.WriteFile(executable, bundledToolWrapper, 0700); err == nil && validToolWrapper(ctx, executable, sum) {
		return true
	}
	debug.printf("tool wrapper bundled=unusable")
	_ = os.Remove(executable)
	return false
}
