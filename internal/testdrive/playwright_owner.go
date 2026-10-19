package testdrive

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Live connections are ownership leases, not saved PIDs. Node descendants stop
// themselves when asked, or when ddtest dies and their owner connection closes.
type playwrightOwner struct {
	directory string
	listener  net.Listener
	accepted  chan struct{}
	clients   sync.Map
	wait      sync.WaitGroup
}

func startPlaywrightOwner(directory string, env map[string]string) (*playwrightOwner, error) {
	for name, contents := range map[string]string{"children.cjs": playwrightChildren, "playwright_owner.cjs": playwrightOwnerScript, "playwright_guard.cjs": playwrightGuardScript} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
			return nil, err
		}
	}
	owner, err := newPlaywrightOwner()
	if err == nil {
		env["DDTEST_PLAYWRIGHT_OWNER"] = owner.listener.Addr().String()
	}
	return owner, err
}

func newPlaywrightOwner() (*playwrightOwner, error) {
	directory, err := os.MkdirTemp("/tmp", "ddtest-process-")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", filepath.Join(directory, "owner"))
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(directory))
	}
	owner := &playwrightOwner{directory: directory, listener: listener, accepted: make(chan struct{})}
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

func (o *playwrightOwner) Close() error {
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
		return os.RemoveAll(o.directory)
	case <-time.After(5 * time.Second):
		o.clients.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
		return fmt.Errorf("owned Playwright process did not release its live connection; retain %s", o.directory)
	}
}
