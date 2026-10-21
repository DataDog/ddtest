package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kballard/go-shellquote"
)

// A private socket is essential: shutting down the user's default Watchman
// daemon would also interrupt watches unrelated to validation.
type ownedWatchman struct {
	directory, binary string
}

func (w *ownedWatchman) args() []string {
	return []string{"--no-site-spawner", "--sockname=" + filepath.Join(w.directory, "sock"), "--statefile=" + filepath.Join(w.directory, "state"), "--logfile=" + filepath.Join(w.directory, "log"), "--pidfile=" + filepath.Join(w.directory, "pid"), "--no-pretty"}
}

func (s *Session) watchmanEnvironment(env map[string]string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if s.watchman == nil {
		binary, err := exec.LookPath("watchman")
		if errors.Is(err, exec.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		binary, err = filepath.Abs(binary)
		if err != nil {
			return err
		}
		// Use a short path even when TMPDIR is too long for a Unix socket.
		directory, err := os.MkdirTemp("/tmp", "ddtest-watchman-")
		if err != nil {
			return err
		}
		s.watchman = &ownedWatchman{directory: directory, binary: binary}
		bin := filepath.Join(directory, "bin")
		if err := os.Mkdir(bin, 0700); err != nil {
			return err
		}
		command := append([]string{binary}, s.watchman.args()...)
		marker := shellquote.Join(filepath.Join(directory, "started"))
		if err := os.WriteFile(filepath.Join(bin, "watchman"), []byte("#!/bin/sh\n: > "+marker+" || exit 1\nexec "+shellquote.Join(command...)+" \"$@\"\n"), 0700); err != nil {
			return err
		}
	}
	env["PATH"] = filepath.Join(s.watchman.directory, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	// fb-watchman must discover the private socket through the wrapper instead
	// of connecting to a socket inherited from the user's shell.
	env["WATCHMAN_SOCK"] = ""
	return nil
}

func (w *ownedWatchman) close() error {
	if _, err := os.Lstat(filepath.Join(w.directory, "sock")); os.IsNotExist(err) {
		if _, started := os.Lstat(filepath.Join(w.directory, "started")); !os.IsNotExist(started) {
			return fmt.Errorf("cannot verify Watchman startup/exit without its private socket; retain owned storage %s", w.directory)
		}
		return os.RemoveAll(w.directory)
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(w.args(), "--no-spawn", "--no-local")
	output, err := exec.CommandContext(ctx, w.binary, append(args, "get-pid")...).Output()
	if err != nil {
		return fmt.Errorf("inspect owned Watchman at %s: %w", w.directory, err)
	}
	var response struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.PID <= 0 {
		return fmt.Errorf("invalid owned Watchman PID at %s", w.directory)
	}
	if err := exec.CommandContext(ctx, w.binary, append(args, "shutdown-server")...).Run(); err != nil {
		return fmt.Errorf("stop owned Watchman at %s: %w", w.directory, err)
	}
	// Verify exit through the existing bounded process lifecycle helper.
	if err := waitOwnedProcessExit(response.PID); err != nil {
		return fmt.Errorf("owned Watchman did not exit at %s: %w", w.directory, err)
	}
	return os.RemoveAll(w.directory)
}
