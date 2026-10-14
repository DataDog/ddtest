package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestMain keeps persistent vendor workspaces out of the developer's cache.
// The directory is created on first use: helper processes that re-execute this
// binary exit without returning from m.Run, so they must not create one.
func TestMain(m *testing.M) {
	// The tool wrapper's tests run this binary as ddto or as a tool: it prints
	// its arguments, one per line, and exits with the requested status.
	if os.Getenv(fakeToolEnv) == "1" {
		if os.Getenv(fakeToolOrphanEnv) == "1" {
			// A child that keeps this process's output after it exits. It
			// runs elsewhere: Windows keeps a process's working directory.
			child := exec.Command(os.Args[0])
			child.Dir = filepath.Dir(os.Args[0])
			child.Env = append(os.Environ(), fakeToolOrphanEnv+"=", fakeToolSleepEnv+"=20s")
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if err := child.Start(); err != nil {
				os.Exit(3)
			}
			// The test ends the child: Windows cannot remove a running
			// test binary.
			if file, err := os.OpenFile(os.Getenv(fakeToolOrphansEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
				fmt.Fprintln(file, child.Process.Pid)
				file.Close()
			}
			os.Exit(0)
		}
		if sleep, err := time.ParseDuration(os.Getenv(fakeToolSleepEnv)); err == nil {
			time.Sleep(sleep)
		}
		if os.Getenv(fakeToolPIDEnv) == "1" {
			fmt.Println("pid=" + strconv.Itoa(os.Getpid()))
		}
		for _, arg := range os.Args[1:] {
			fmt.Println(arg)
		}
		code, _ := strconv.Atoi(os.Getenv(fakeToolExitEnv))
		os.Exit(code)
	}
	var (
		once     sync.Once
		cache    string
		cacheErr error
	)
	userCacheDir = func() (string, error) {
		once.Do(func() { cache, cacheErr = os.MkdirTemp("", "ddto-runner-cache-") })
		return cache, cacheErr
	}
	code := m.Run()
	if cache != "" {
		if err := os.RemoveAll(cache); err != nil {
			fmt.Fprintln(os.Stderr, "remove user cache:", err)
			code = 1
		}
	}
	os.Exit(code)
}

// Helper processes re-execute this binary and exit inside m.Run, so TestMain's
// cleanup never runs for them. They must not create a user cache.
func TestHelperProcessesLeaveNoTemporaryFiles(t *testing.T) {
	temp := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPackageListHelperProcess$", "--", "success")
	cmd.Env = append(os.Environ(), "DDTO_PACKAGE_LIST_HELPER=1", "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
		t.Fatalf("helper process left temporary files: %v %v", entries, err)
	}
}
