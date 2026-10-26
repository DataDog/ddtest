// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sessionTimeFormat = "2006-01-02-150405"

const sessionOwnerMarker = ".ddtest-owner.json"

// Session owns the files produced by one testdrive run.
type Session struct {
	id         string
	directory  string
	persistent bool
	root       string
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
	err := markSessionStorage(s.directory, s.root)
	if err != nil {
		_ = os.RemoveAll(s.directory)
		return err
	}
	return nil
}

// Close removes only the scratch directory owned by this session.
func (s *Session) Close() error {
	if s.persistent {
		return nil
	}
	if err := removeMarkedStorage(s.directory); err != nil {
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

func markSessionStorage(directory, root string) error {
	data, err := json.Marshal(sessionOwner{Root: canonicalWorkflowRoot(root), User: os.Getuid()})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, sessionOwnerMarker), data, 0600)
}

// removeMarkedStorage deletes the owner marker only after the rest of the
// directory, so storage that cannot be fully removed is reclaimed by a later run.
func removeMarkedStorage(directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var removeErr error
	for _, entry := range entries {
		if entry.Name() != sessionOwnerMarker {
			removeErr = errors.Join(removeErr, os.RemoveAll(filepath.Join(directory, entry.Name())))
		}
	}
	if removeErr != nil {
		return removeErr
	}
	return os.RemoveAll(directory)
}

type sessionOwner struct {
	Root string `json:"root"`
	User int    `json:"user"`
}

// The caller holds the repository lock, so no matching marked session can be live.
func recoverSessions(root string, output io.Writer) error {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ddtest-") {
			continue
		}
		path := filepath.Join(os.TempDir(), entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !privateOwnedDirectory(info) {
			continue
		}
		marker := filepath.Join(path, sessionOwnerMarker)
		info, err = os.Lstat(marker)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 || !ownedByCurrentUser(info) {
			continue
		}
		data, err := os.ReadFile(marker)
		if err != nil {
			continue
		}
		var owner sessionOwner
		if json.Unmarshal(data, &owner) != nil || owner.Root != canonicalWorkflowRoot(root) || owner.User != os.Getuid() {
			continue
		}
		if err := removeMarkedStorage(path); err != nil {
			return fmt.Errorf("recover owned session: %w", err)
		}
		_, _ = fmt.Fprintf(output, "Recovered abandoned testdrive session: %s\n", entry.Name())
	}
	return nil
}
