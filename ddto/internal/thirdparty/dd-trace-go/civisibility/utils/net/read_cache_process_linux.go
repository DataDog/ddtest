// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"bytes"
	"os"
	"strconv"
)

// processEnded reports whether the process pid is a zombie, which ended but
// which its parent has not reaped: the state that /proc/<pid>/stat lists after
// the command name, Z or X.
func processEnded(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 || i+2 >= len(stat) {
		return false
	}
	state := stat[i+2]
	return state == 'Z' || state == 'X'
}
