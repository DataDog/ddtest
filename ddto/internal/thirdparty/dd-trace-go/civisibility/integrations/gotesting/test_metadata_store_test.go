// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"sync"
	"testing"
	"unsafe"
)

// The execution-metadata store must keep the SDK map's semantics: entries are
// keyed by the testing object, a second create replaces the first, and delete
// removes only that object's entry.
func TestTestMetadataStoreKeepsMapSemantics(t *testing.T) {
	first, second := &testing.T{}, &testing.T{}
	if getTestMetadata(first) != nil {
		t.Fatal("unexpected entry before create")
	}
	original := &testing.T{}
	created := createTestMetadata(first, original)
	if created == nil || created.originalTest != original {
		t.Fatalf("create returned %#v", created)
	}
	if got := getTestMetadata(first); got != created {
		t.Fatal("lookup by testing object returned another entry")
	}
	if got := getTestMetadataFromPointer(unsafe.Pointer(first)); got != created {
		t.Fatal("lookup by pointer returned another entry")
	}
	if getTestMetadata(second) != nil {
		t.Fatal("another testing object shares the entry")
	}

	replacement := createTestMetadata(first, nil)
	if replacement == created || getTestMetadata(first) != replacement {
		t.Fatal("a second create must replace the entry")
	}

	other := createTestMetadata(second, nil)
	deleteTestMetadata(first)
	if getTestMetadata(first) != nil {
		t.Fatal("delete kept the entry")
	}
	if getTestMetadata(second) != other {
		t.Fatal("delete removed another testing object's entry")
	}
	deleteTestMetadata(first) // absent: no-op
	deleteTestMetadata(second)
	if getTestMetadata(second) != nil {
		t.Fatal("delete kept the second entry")
	}

	benchmark := &testing.B{}
	entry := createTestMetadata(benchmark, nil)
	if getTestMetadata(benchmark) != entry {
		t.Fatal("benchmark entry lost")
	}
	deleteTestMetadata(benchmark)
}

// Parallel tests create, read, replace and delete their own entries while
// others do the same. Run with -race.
func TestTestMetadataStoreConcurrentTests(t *testing.T) {
	const workers, iterations = 32, 200
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				tb := &testing.T{}
				created := createTestMetadata(tb, nil)
				created.isARetry = true
				if got := getTestMetadata(tb); got != created || !got.isARetry {
					errs <- "lookup returned another entry"
					return
				}
				replacement := createTestMetadata(tb, nil)
				if getTestMetadataFromPointer(unsafe.Pointer(tb)) != replacement {
					errs <- "replacement lost"
					return
				}
				deleteTestMetadata(tb)
				if getTestMetadata(tb) != nil {
					errs <- "delete kept the entry"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// BenchmarkTestMetadataLifecycleParallel is one test's create, three hook
// lookups and delete, with every P doing the same.
func BenchmarkTestMetadataLifecycleParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		tb := &testing.T{}
		for pb.Next() {
			createTestMetadata(tb, nil)
			for range 3 {
				if getTestMetadata(tb) == nil {
					b.Error("missing entry")
				}
			}
			deleteTestMetadata(tb)
		}
	})
}
