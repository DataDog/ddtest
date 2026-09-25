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
	s := planSession(repositoryRoot)
	if err := s.create(); err != nil {
		return nil, err
	}
	return s, nil
}

func planSession(repositoryRoot string) *Session {
	id := time.Now().UTC().Format(sessionTimeFormat) + "-" + rand.Text()
	return &Session{id: id, directory: filepath.Join(repositoryRoot, constants.PlanDirectory, "testdrive", id)}
}

func (s *Session) create() error {
	if err := os.MkdirAll(filepath.Dir(s.directory), 0755); err != nil {
		return fmt.Errorf("create testdrive sessions directory: %w", err)
	}
	if err := os.Mkdir(s.directory, 0755); err != nil {
		return fmt.Errorf("create testdrive output folder: %w", err)
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
