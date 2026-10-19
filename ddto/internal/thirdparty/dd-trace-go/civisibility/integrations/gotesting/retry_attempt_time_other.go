//go:build !windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"time"
	"unsafe"
)

// startNativeClock starts a measurement on the clock that testing uses for
// test durations, the runtime's monotonic clock outside Windows. The returned
// function reports the time elapsed since then.
func startNativeClock() func() time.Duration {
	start := time.Now()
	return func() time.Duration { return time.Since(start) }
}

func initializeRetryAttemptStart(base unsafe.Pointer, field unsafeField) {
	if base == nil || !field.available {
		return
	}
	value := reflect.NewAt(field.typ, fieldRawPtr(base, field)).Elem()
	if value.Type() == reflect.TypeFor[time.Time]() {
		// Go 1.28 keeps the start as a time.Time.
		value.Set(reflect.ValueOf(time.Now()))
		return
	}
	now := value.FieldByName("now")
	if now.IsValid() && now.CanAddr() && now.Type() == reflect.TypeFor[time.Time]() {
		reflect.NewAt(now.Type(), unsafe.Pointer(now.UnsafeAddr())).Elem().Set(reflect.ValueOf(time.Now()))
	}
}

func addRetryAttemptElapsed(base unsafe.Pointer, layout *testingInternalsLayout) {
	field := layout.common.start.unsafeField
	if base == nil || !field.available {
		return
	}
	value := reflect.NewAt(field.typ, fieldRawPtr(base, field)).Elem()
	if value.Type() == reflect.TypeFor[time.Time]() {
		*fieldPtr[time.Duration](base, layout.common.duration) += time.Since(value.Interface().(time.Time))
		return
	}
	now := value.FieldByName("now")
	if !now.IsValid() || !now.CanAddr() || now.Type() != reflect.TypeFor[time.Time]() {
		return
	}
	started := reflect.NewAt(now.Type(), unsafe.Pointer(now.UnsafeAddr())).Elem().Interface().(time.Time)
	*fieldPtr[time.Duration](base, layout.common.duration) += time.Since(started)
}
