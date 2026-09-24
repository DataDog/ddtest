// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const sessionTimeFormat = "2006-01-02-150405"

// Session owns the files produced by one testdrive run.
type Session struct {
	id        string
	directory string
}

// NewSession creates private scratch storage outside the customer repository.
// Call Close on every exit path, including setup failures and cancellation.
func NewSession() (*Session, error) {
	prefix := "ddtest-testdrive-" + time.Now().UTC().Format(sessionTimeFormat) + "-"
	directory, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, fmt.Errorf("create temporary testdrive session: %w", err)
	}
	return &Session{id: filepath.Base(directory), directory: directory}, nil
}

// Close removes only the scratch directory owned by this session.
func (s *Session) Close() error {
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
