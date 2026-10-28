// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// publishTerminal creates the terminal file, then writes it: a claim that
// reads the empty file in between sees no terminal state yet, instead of
// keeping an unavailable one for the rest of its process.
func TestEFDFaultySessionTerminalBeingPublished(t *testing.T) {
	store := &efdFaultySessionFilesystemStore{directory: filepath.Join(t.TempDir(), "efd"), threshold: 50, knownCount: func() uint64 { return 4 }, now: time.Now, sleep: time.Sleep}
	if err := store.ensureDirectory(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.directory, efdFaultySessionTerminalFile)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if admission, cache := store.observe(); admission != earlyFlakeDetectionAdmissionAllowed || cache {
		t.Fatalf("an empty terminal file: %s, cache %t", efdFaultySessionAdmissionName(admission), cache)
	}
	if admission := store.claim(); admission != earlyFlakeDetectionAdmissionAllowed {
		t.Fatalf("a claim with an empty terminal file: %s", efdFaultySessionAdmissionName(admission))
	}
	if err := os.WriteFile(path, []byte("faulty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if admission, cache := store.observe(); admission != earlyFlakeDetectionAdmissionFaulty || !cache {
		t.Fatalf("a published terminal file: %s, cache %t", efdFaultySessionAdmissionName(admission), cache)
	}
}
