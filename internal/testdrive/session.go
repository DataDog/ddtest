// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const sessionTimeFormat = "2006-01-02-150405"

// Session owns the files produced by one testdrive run.
type Session struct {
	id         string
	directory  string
	persistent bool
}

// NewSession creates private scratch storage outside the customer repository.
// Call Close on every exit path, including setup failures and cancellation.
func NewSession() (*Session, error) {
	s := planSession()
	if err := s.create(); err != nil {
		return nil, err
	}
	return s, nil
}

// Planning permits an exact preview without creating any scratch files.
func planSession() *Session {
	token := rand.Text()
	id := "ddtest-testdrive-" + time.Now().UTC().Format(sessionTimeFormat) + "-" + token
	// Keep the descriptive report ID, but leave room for native helper sockets
	// beneath TMPDIR (notably Chrome's SingletonSocket on Linux).
	temporaryRoot := os.TempDir()
	if canonical, err := filepath.EvalSymlinks(temporaryRoot); err == nil {
		temporaryRoot = canonical
	}
	return &Session{id: id, directory: filepath.Join(temporaryRoot, "ddtest-"+token[:16])}
}

func (s *Session) create() error {
	if s.persistent {
		return os.MkdirAll(s.directory, 0700)
	}
	if err := os.Mkdir(s.directory, 0700); err != nil {
		return fmt.Errorf("create temporary testdrive session: %w", err)
	}
	return nil
}

// Close removes only the scratch directory owned by this session.
func (s *Session) Close() error {
	if s.persistent {
		return nil
	}
	if err := os.RemoveAll(s.directory); err != nil {
		return fmt.Errorf("clean up testdrive session %s: %w", s.directory, err)
	}
	return nil
}

// ID returns the unique name of the session.
func (s *Session) ID() string {
	return s.id
}

// Directory returns the directory owned by the session.
func (s *Session) Directory() string {
	return s.directory
}
