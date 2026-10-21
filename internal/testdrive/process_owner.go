package testdrive

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed scripts/process_children.cjs
var processChildren string

//go:embed scripts/process_owner.cjs
var processOwnerScript string

//go:embed scripts/process_guard.cjs
var processGuardScript string

type processCleanupError struct{ error }
type processSupervisionError struct{ Code, Reason string }

func (e *processSupervisionError) Error() string {
	return "ddtest process supervision unavailable; comparison is not validated: " + e.Reason
}

// Live connections are ownership leases, not saved PIDs. Node descendants stop
// themselves when asked, or when ddtest dies and their owner connection closes.
type processOwner struct {
	directory  string
	errorsPath string
	listener   net.Listener
	accepted   chan struct{}
	clients    sync.Map
	wait       sync.WaitGroup
}

func startProcessOwner(directory string, env map[string]string) (*processOwner, error) {
	for name, contents := range map[string]string{"children.cjs": processChildren, "process_owner.cjs": processOwnerScript, "process_guard.cjs": processGuardScript} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
			return nil, err
		}
	}
	errorsPath := filepath.Join(directory, "process-errors.jsonl")
	if err := os.WriteFile(errorsPath, nil, 0600); err != nil {
		return nil, err
	}
	owner, err := newProcessOwner()
	if err == nil {
		env["DDTEST_PROCESS_OWNER"] = owner.listener.Addr().String()
		env["DDTEST_PROCESS_ERRORS"] = errorsPath
		owner.errorsPath = errorsPath
	}
	return owner, err
}

func newProcessOwner() (*processOwner, error) {
	socketBase := os.TempDir()
	// Darwin Unix socket paths are limited to 104 bytes; customer TMPDIR paths
	// can already exceed that before adding the private directory and socket.
	if runtime.GOOS == "darwin" {
		socketBase = "/tmp"
	}
	directory, err := os.MkdirTemp(socketBase, "ddtest-process-")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", filepath.Join(directory, "owner"))
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(directory))
	}
	owner := &processOwner{directory: directory, listener: listener, accepted: make(chan struct{})}
	go func() {
		defer close(owner.accepted)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			owner.clients.Store(client, true)
			owner.wait.Add(1)
			go func() {
				defer owner.wait.Done()
				defer owner.clients.Delete(client)
				defer func() { _ = client.Close() }()
				_, _ = io.Copy(io.Discard, client)
			}()
		}
	}()
	return owner, nil
}

func (o *processOwner) Close() error {
	_ = o.listener.Close()
	<-o.accepted // No connection can be added after the cleanup snapshot.
	o.clients.Range(func(key, _ any) bool {
		client := key.(net.Conn)
		_ = client.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = io.WriteString(client, "stop\n")
		return true
	})
	done := make(chan struct{})
	go func() { o.wait.Wait(); close(done) }()
	select {
	case <-done:
		var cleanupErr error
		if err := os.RemoveAll(o.directory); err != nil {
			cleanupErr = &processCleanupError{err}
		}
		return errors.Join(cleanupErr, o.supervisionError())
	case <-time.After(5 * time.Second):
		o.clients.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
		return &processCleanupError{fmt.Errorf("owned browser process did not release its live connection; retain %s", o.directory)}
	}
}

func (o *processOwner) supervisionError() error {
	if o.errorsPath == "" {
		return nil
	}
	file, err := os.Open(o.errorsPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 16384))
	if err != nil {
		return err
	}
	if len(data) != 0 {
		var failure struct{ Code, Reason string }
		if err := json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &failure); err != nil {
			return err
		}
		return &processSupervisionError{Code: failure.Code, Reason: failure.Reason}
	}
	return nil
}
