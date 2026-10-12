// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
)

// A process that ended but that its parent has not reaped is not running,
// although it still accepts a signal 0.
func TestProcessAliveZombie(t *testing.T) {
	zombie := exec.Command(os.Args[0], "-test.run=^$")
	require.NoError(t, zombie.Start())
	defer zombie.Wait()
	for deadline := time.Now().Add(30 * time.Second); !processEnded(zombie.Process.Pid); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child did not end")
		}
	}
	alive, known := processAlive(zombie.Process.Pid)
	require.True(t, known)
	require.False(t, alive)
}
