//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"math/bits"
	"reflect"
	"time"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/xsys/windows"
)

var (
	retryAttemptKernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	retryAttemptQueryPerformanceCounter   = retryAttemptKernel32.NewProc("QueryPerformanceCounter")
	retryAttemptQueryPerformanceFrequency = retryAttemptKernel32.NewProc("QueryPerformanceFrequency")
)

func retryAttemptPerformanceValue(proc *windows.LazyProc) (int64, bool) {
	var value int64
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&value)))
	return value, ok != 0
}

// startNativeClock starts a measurement on the clock that testing uses for
// test durations: QueryPerformanceCounter on Windows before Go 1.28, where the
// runtime's clock is coarser, and the runtime's clock once testing keeps the
// start as a time.Time. The returned function reports the time elapsed since
// then.
func startNativeClock() func() time.Duration {
	if start := getTestingInternalsLayout().common.start; start.available && start.typ == reflect.TypeFor[time.Time]() {
		// Go 1.28 measures tests with the runtime's clock on Windows too.
		begin := time.Now()
		return func() time.Duration { return time.Since(begin) }
	}
	started, startedOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
	frequency, frequencyOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceFrequency)
	if !startedOK || !frequencyOK || frequency <= 0 {
		start := time.Now()
		return func() time.Duration { return time.Since(start) }
	}
	return func() time.Duration {
		now, ok := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
		if !ok || now <= started {
			return 0
		}
		hi, lo := bits.Mul64(uint64(now-started), uint64(time.Second)/uint64(time.Nanosecond))
		elapsed, _ := bits.Div64(hi, lo, uint64(frequency))
		return time.Duration(elapsed)
	}
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
	counter, ok := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
	if !ok {
		return
	}
	now := value.FieldByName("now")
	if now.IsValid() && now.CanAddr() && now.Kind() == reflect.Int64 {
		reflect.NewAt(now.Type(), unsafe.Pointer(now.UnsafeAddr())).Elem().SetInt(counter)
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
	now, counterOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
	frequency, frequencyOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceFrequency)
	if !counterOK || !frequencyOK || frequency <= 0 {
		return
	}
	startedField := value.FieldByName("now")
	if !startedField.IsValid() || !startedField.CanAddr() || startedField.Kind() != reflect.Int64 {
		return
	}
	started := reflect.NewAt(startedField.Type(), unsafe.Pointer(startedField.UnsafeAddr())).Elem().Int()
	if now <= started {
		return
	}
	hi, lo := bits.Mul64(uint64(now-started), uint64(time.Second)/uint64(time.Nanosecond))
	elapsedNanoseconds, _ := bits.Div64(hi, lo, uint64(frequency))
	elapsed := time.Duration(elapsedNanoseconds)
	*fieldPtr[time.Duration](base, layout.common.duration) += elapsed
}
