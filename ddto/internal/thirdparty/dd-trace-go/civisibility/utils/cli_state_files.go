// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"io/fs"
	"os"
	"runtime"
	"strings"
	"syscall"
)

// openUnlogged opens name as os.OpenFile does, without recording it in Go's
// test log. Test processes read and create the CLI state's files while tests
// run. If the state directory lies in the module, as with TMPDIR inside it,
// go test would check those files, which change on every run, and would cache
// no result.
func openUnlogged(name string, flag int, perm uint32) (*os.File, error) {
	fd, err := syscall.Open(windowsLongPath(name), flag|syscall.O_CLOEXEC, perm)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// windowsLongPath adds the prefix with which Windows opens an absolute path
// of 248 characters or more, as os does and syscall.Open does not. Other
// systems keep name.
func windowsLongPath(name string) string {
	if runtime.GOOS != "windows" {
		return name
	}
	return extendedWindowsPath(name)
}

// extendedWindowsPath classifies name as os/path_windows.go does: paths
// already extended (\\?\, \??\) and device paths (\\.\) keep their meaning
// and stay as they are; a UNC path gets \\?\UNC\ and a drive path \\?\.
// Short and relative paths stay as they are: the CLI state's paths are
// absolute.
func extendedWindowsPath(name string) string {
	separator := func(c byte) bool { return c == '\\' || c == '/' }
	if len(name) < 248 {
		return name
	}
	if strings.HasPrefix(name, `\??\`) || (separator(name[0]) && separator(name[1]) && (name[2] == '?' || name[2] == '.') && separator(name[3])) {
		return name
	}
	name = strings.ReplaceAll(name, "/", `\`)
	switch {
	case strings.HasPrefix(name, `\\`):
		return `\\?\UNC\` + name[2:]
	case len(name) >= 3 && name[1] == ':' && name[2] == '\\':
		return `\\?\` + name
	}
	return name
}

// createUnlogged creates name, which must not exist, without recording it in
// Go's test log.
func createUnlogged(name string) error {
	file, err := openUnlogged(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		err = file.Close()
	}
	return err
}

// existsUnlogged reports whether name can be opened, without recording it in
// Go's test log.
func existsUnlogged(name string) bool {
	file, err := openUnlogged(name, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}
