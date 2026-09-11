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

	"github.com/DataDog/ddtest/internal/constants"
)

const sessionTimeFormat = "2006-01-02-150405"

// Session owns the files produced by one testdrive run.
type Session struct {
	id        string
	directory string
}

// NewSession creates an independent directory for one testdrive run.
func NewSession(repositoryRoot string) (*Session, error) {
	sessionsDirectory := filepath.Join(repositoryRoot, constants.PlanDirectory, "testdrive")
	if err := os.MkdirAll(sessionsDirectory, 0755); err != nil {
		return nil, fmt.Errorf("create testdrive sessions directory: %w", err)
	}

	prefix := time.Now().UTC().Format(sessionTimeFormat) + "-"
	directory, err := os.MkdirTemp(sessionsDirectory, prefix)
	if err != nil {
		return nil, fmt.Errorf("create testdrive session directory: %w", err)
	}

	return &Session{
		id:        filepath.Base(directory),
		directory: directory,
	}, nil
}

// ID returns the unique name of the session.
func (s *Session) ID() string {
	return s.id
}

// Directory returns the directory owned by the session.
func (s *Session) Directory() string {
	return s.directory
}
