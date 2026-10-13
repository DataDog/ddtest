//go:build darwin || linux

package testdrive

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlaywrightCleanupStopsOnlyRecordedDescendants(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed for the process lifecycle regression")
	}
	// An unrelated listener is deliberately not in the child record.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	dir := t.TempDir()
	preload := filepath.Join(dir, "children.cjs")
	record := filepath.Join(dir, "children.txt")
	portFile := filepath.Join(dir, "port")
	require.NoError(t, os.WriteFile(preload, []byte(playwrightChildren), 0600))
	child := fmt.Sprintf(`const fs=require('node:fs');const s=require('node:net').createServer();s.listen(0,'127.0.0.1',()=>fs.writeFileSync(%q,String(s.address().port)));`, portFile)
	// Simulate a descendant that starts its own group and outlives its wrapper.
	parent := fmt.Sprintf(`const child=require('node:child_process').spawn(process.execPath,['-e',%q],{detached:true,stdio:'ignore'});child.unref();`, child)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", parent)
	cmd.Env = append(os.Environ(), "NODE_OPTIONS=--require "+preload, "DDTEST_PLAYWRIGHT_CHILDREN="+record)
	require.NoError(t, cmd.Run())
	t.Cleanup(func() { require.NoError(t, cleanupPlaywrightChildren(record)) })
	var port []byte
	require.Eventually(t, func() bool { port, err = os.ReadFile(portFile); return err == nil }, 5*time.Second, 20*time.Millisecond)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+string(port), time.Second)
	require.NoError(t, err)
	_ = conn.Close()
	require.NoError(t, cleanupPlaywrightChildren(record))
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+string(port), 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
		}
		return err != nil
	}, time.Second, 20*time.Millisecond)
	conn, err = net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	require.NoError(t, err)
	_ = conn.Close()
}
