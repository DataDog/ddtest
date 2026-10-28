// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"testing"
	"time"
	"unsafe"
)

// Go 1.28 keeps testing.common.start as a time.Time instead of a
// highPrecisionTime. A fresh retry attempt still starts its clock and adds
// its elapsed time to the duration.
func TestRetryAttemptTimeHandlesATimeStart(t *testing.T) {
	type common struct {
		start    time.Time
		duration time.Duration
	}
	field := func(name string) unsafeField {
		f, ok := reflect.TypeFor[common]().FieldByName(name)
		if !ok {
			t.Fatal(name)
		}
		return unsafeField{name: name, offset: f.Offset, size: f.Type.Size(), typ: f.Type, available: true}
	}
	var c common
	base := unsafe.Pointer(&c)
	layout := &testingInternalsLayout{}
	layout.common.start.unsafeField = field("start")
	layout.common.duration = field("duration")

	before := time.Now()
	initializeRetryAttemptStart(base, layout.common.start.unsafeField)
	if c.start.Before(before) {
		t.Fatalf("start %v was not set after %v", c.start, before)
	}
	c.start = c.start.Add(-time.Second)
	addRetryAttemptElapsed(base, layout)
	if c.duration < time.Second {
		t.Fatalf("duration %v, want at least 1s", c.duration)
	}
}
