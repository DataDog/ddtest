//go:build unix

package integration

import (
	"bytes"
	"errors"
	"net"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// localTIASignalFixture's test connects to LOCAL_TIA_SIGNAL_ADDRESS, when
// set, and waits for the connection to close, so that the CLI can be
// signaled while go test runs.
var localTIASignalFixture = map[string]string{
	"lib/value.go": "package lib\n\nfunc Value() int { return 1 }\n",
	"lib/value_test.go": `package lib

import (
	"io"
	"net"
	"os"
	"testing"
)

func TestValue(t *testing.T) {
	_ = Value()
	if address := os.Getenv("LOCAL_TIA_SIGNAL_ADDRESS"); address != "" {
		if c, err := net.Dial("tcp", address); err == nil {
			_, _ = io.Copy(io.Discard, c)
		}
	}
}
`,
}

// A signal ends the work that local TIA does with Datadog's TIA artifacts
// API: a lookup, which runs before go test, stops at once and the run ends
// with the signal's status; an upload, after go test, stops at once too; and
// after a signal that reached go test, no upload starts.
func TestLocalTIARemoteMapSignals(t *testing.T) {
	for _, phase := range []string{"lookup", "upload", "go test"} {
		t.Run(phase, func(t *testing.T) {
			f := newTIAFixture(t, false, localTIASignalFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
			arrived := make(chan string, 4)
			f.intake.mu.Lock()
			f.intake.arrived, f.intake.release = arrived, make(chan struct{})
			// Before the intake closes, which waits for its requests.
			t.Cleanup(func() { close(f.intake.release) })
			if phase != "go test" {
				f.intake.block = phase
			}
			f.intake.mu.Unlock()
			env := append([]string{}, f.env...)
			// The test's connection, which closing releases the test.
			connected := make(chan net.Conn, 1)
			if phase == "go test" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				env = append(env, "LOCAL_TIA_SIGNAL_ADDRESS="+listener.Addr().String())
				go func() {
					if c, err := listener.Accept(); err == nil {
						connected <- c
						arrived <- "go test"
					}
				}()
			}
			args := append(append([]string{"test", "-json", "-count=1"}, f.flags...), "./...")
			cmd := exec.Command(f.driver, driverArgs(f.driver, args)...)
			cmd.Dir, cmd.Env = f.dir, append(env, "PWD="+f.dir)
			// A process that outlives the CLI and keeps its output cannot
			// hold Wait.
			cmd.WaitDelay = 5 * time.Second
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			select {
			case got := <-arrived:
				if got != phase {
					t.Fatalf("%s arrived first", got)
				}
			case err := <-exited:
				t.Fatalf("the CLI exited before the %s: %v\n%s", phase, err, output.String())
			}
			signaled := time.Now()
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if phase == "go test" {
				// go test has the signal: the test may end now.
				(<-connected).Close()
			}
			var err error
			select {
			case err = <-exited:
			case <-time.After(30 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("the CLI kept running after SIGTERM:\n%s", output.String())
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("exit %v\n%s", err, output.String())
			}
			// Before go test, and after it ended, the signal ends the CLI as
			// it would have ended go test; during go test, go test's status
			// is the CLI's.
			if status := exit.Sys().(syscall.WaitStatus); phase != "go test" && (!status.Signaled() || status.Signal() != syscall.SIGTERM) {
				t.Fatalf("status %v, want ended by SIGTERM, %s after the signal\n%s", status, time.Since(signaled), output.String())
			}
			if elapsed := time.Since(signaled); elapsed > 5*time.Second {
				t.Fatalf("exited %s after the signal", elapsed)
			}
			if _, uploads := f.intake.requests(); uploads != 0 {
				t.Fatalf("%d uploads", uploads)
			}
		})
	}
}
