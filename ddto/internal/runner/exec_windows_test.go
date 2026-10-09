package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestExecHelperProcess is the wrapper, the -exec program and the process the
// program leaves running in TestExecWrapperEndsProgramWithItself.
func TestExecHelperProcess(t *testing.T) {
	self := []string{os.Args[0], "-test.run=^TestExecHelperProcess$"}
	switch os.Getenv("DDTEST_EXEC_HELPER") {
	case "wrapper":
		os.Setenv("DDTEST_EXEC_HELPER", os.Getenv("DDTEST_EXEC_PROGRAM"))
		os.Exit(ExecWithCallerEnvironment(self))
	case "sleep":
		path := os.Getenv("DDTEST_EXEC_PID")
		if err := os.WriteFile(path+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(3)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			os.Exit(3)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	case "spawn":
		cmd := exec.Command(self[0], self[1:]...)
		cmd.Env = append(os.Environ(), "DDTEST_EXEC_HELPER=sleep")
		if err := cmd.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
}

// Native go test ends a timed-out -exec program by terminating it. Terminating
// ddtest's wrapper must end the program as well, while a process that the
// program leaves running after it exits outlives the wrapper, as it outlives
// native go test.
func TestExecWrapperEndsProgramWithItself(t *testing.T) {
	start := func(t *testing.T, program string) (*exec.Cmd, string) {
		pid := filepath.Join(t.TempDir(), "pid")
		wrapper := exec.Command(os.Args[0], "-test.run=^TestExecHelperProcess$")
		wrapper.Env = append(os.Environ(), "DDTEST_EXEC_HELPER=wrapper", "DDTEST_EXEC_PROGRAM="+program, "DDTEST_EXEC_PID="+pid)
		if err := wrapper.Start(); err != nil {
			t.Fatal(err)
		}
		return wrapper, pid
	}
	started := func(t *testing.T, path string) *os.Process {
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			process, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = process.Kill() })
			return process
		}
		t.Fatal("exec program did not start")
		return nil
	}
	exits := func(process *os.Process, within time.Duration) bool {
		done := make(chan struct{})
		go func() { _, _ = process.Wait(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(within):
			return false
		}
	}
	t.Run("terminated wrapper", func(t *testing.T) {
		wrapper, pid := start(t, "sleep")
		program := started(t, pid)
		if err := wrapper.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = wrapper.Wait()
		if !exits(program, 10*time.Second) {
			t.Fatal("exec program outlived its terminated wrapper")
		}
	})
	t.Run("finished program", func(t *testing.T) {
		wrapper, pid := start(t, "spawn")
		if err := wrapper.Wait(); err != nil {
			t.Fatal(err)
		}
		if exits(started(t, pid), time.Second) {
			t.Fatal("a process left by the exec program ended with the wrapper")
		}
	})
}
