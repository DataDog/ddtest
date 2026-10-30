package minitracer

import (
	"context"
	_ "unsafe" // the SDK's mirror hook names these variables
)

// The variables that the application's dd-trace-go, compiled with the span
// mirror's edits, calls. The edited SDK declares the same variables with no
// value and calls them only when they are not nil, so a binary without Mini
// starts no mirror; the linker keeps these definitions, which have values.
// The types must stay as written: the linker does not compare them.

//go:linkname sdkMirrorEnableHook
var sdkMirrorEnableHook func() = sdkMirrorEnable

//go:linkname sdkMirrorStartHook
var sdkMirrorStartHook func(context.Context, any, string) any = sdkMirrorStart

//go:linkname sdkMirrorContextHook
var sdkMirrorContextHook func(context.Context, any) context.Context = sdkMirrorContext

//go:linkname sdkMirrorCaptureHook
var sdkMirrorCaptureHook func(any, string, string, string, string, int64, int64, int32, func(func(string, string) bool), map[string]float64, [16]byte, uint64) any = sdkMirrorCapture

//go:linkname sdkMirrorDeliverHook
var sdkMirrorDeliverHook func(any) = sdkMirrorDeliver

//go:linkname sdkMirrorAdoptHook
var sdkMirrorAdoptHook func(any, any, uint64, bool) ([16]byte, bool) = sdkMirrorAdopt

//go:linkname sdkMirrorAdoptedHook
var sdkMirrorAdoptedHook func(any) bool = sdkMirrorAdopted

//go:linkname sdkMirrorWithheldHook
var sdkMirrorWithheldHook func(int, int) = sdkMirrorWithheld
