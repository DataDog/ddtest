// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package workspace owns ddtest's language-neutral scratch storage: private
// sessions, the per-checkout lock, crash recovery and isolated repository copies.
package workspace

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

// OwnerMarker records which checkout and user own a scratch directory, so a
// later invocation holding the same checkout's lock can reclaim it after a crash.
const OwnerMarker = ".ddtest-owner.json"

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
	s := PlanSession()
	if err := s.Create(); err != nil {
		return nil, err
	}
	return s, nil
}

// PlanSession names a session without creating any scratch files, so a
// preview can show exact paths. Call Create before using it.
func PlanSession() *Session {
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

// Persist keeps the session's files in directory after Close, for runs whose
// session directory is itself the retained evidence.
func (s *Session) Persist(directory string) {
	s.directory, s.persistent = directory, true
}

// Persistent reports whether Close keeps the session's files.
func (s *Session) Persistent() bool {
	return s.persistent
}

// SetOwnerRoot records the checkout whose recovery may reclaim this session.
func (s *Session) SetOwnerRoot(root string) {
	s.root = CanonicalRoot(root)
}

// Create makes the session's directory and marks its owner.
func (s *Session) Create() error {
	if s.persistent {
		return os.MkdirAll(s.directory, 0700)
	}
	if err := os.Mkdir(s.directory, 0700); err != nil {
		return fmt.Errorf("create temporary testdrive session: %w", err)
	}
	err := MarkStorage(s.directory, s.root)
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
	if err := RemoveMarkedStorage(s.directory); err != nil {
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

// MarkStorage marks directory as scratch storage owned by root's invocations.
func MarkStorage(directory, root string) error {
	data, err := json.Marshal(Owner{Root: CanonicalRoot(root), User: os.Getuid()})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, OwnerMarker), data, 0600)
}

// RemoveMarkedStorage deletes the owner marker only after the rest of the
// directory, so storage that cannot be fully removed is reclaimed by a later run.
func RemoveMarkedStorage(directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var removeErr error
	for _, entry := range entries {
		if entry.Name() != OwnerMarker {
			removeErr = errors.Join(removeErr, os.RemoveAll(filepath.Join(directory, entry.Name())))
		}
	}
	if removeErr != nil {
		return removeErr
	}
	return os.RemoveAll(directory)
}

// Owner is the content of an OwnerMarker.
type Owner struct {
	Root string `json:"root"`
	User int    `json:"user"`
}

// RecoverSessions removes marked sessions that root's crashed invocations left
// behind. The caller holds root's lock, so no matching marked session can be live.
func RecoverSessions(root string, output io.Writer) error {
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
		if err != nil || !PrivateOwnedDirectory(info) {
			continue
		}
		marker := filepath.Join(path, OwnerMarker)
		info, err = os.Lstat(marker)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 || !OwnedByCurrentUser(info) {
			continue
		}
		data, err := os.ReadFile(marker)
		if err != nil {
			continue
		}
		var owner Owner
		if json.Unmarshal(data, &owner) != nil || owner.Root != CanonicalRoot(root) || owner.User != os.Getuid() {
			continue
		}
		if err := RemoveMarkedStorage(path); err != nil {
			return fmt.Errorf("recover owned session: %w", err)
		}
		_, _ = fmt.Fprintf(output, "Recovered abandoned testdrive session: %s\n", entry.Name())
	}
	return nil
}

// CanonicalRoot resolves symlinks so aliases of one checkout share ownership.
func CanonicalRoot(root string) string {
	if value, err := filepath.EvalSymlinks(root); err == nil {
		return value
	}
	return root
}
