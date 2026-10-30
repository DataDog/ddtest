package minitracer

import _ "unsafe" // the SDK's guard files name this variable with go:linkname

// sdkGuardMiniLinked is true in every binary that links Mini. ddtest instrument
// compiles the application's dd-trace-go with guards that keep its CI
// Visibility mode, its Bazel manifest and its payload files off. Each guarded
// package declares this variable with no value under the same name, and the
// linker keeps this definition, which has one. The value is data in the
// executable, so the guards see it before any package initializes, including
// a dd-trace-go package that initializes before Mini. A binary without Mini
// has only the zero definitions, and its dd-trace-go keeps its own behavior.
//
//go:linkname sdkGuardMiniLinked
var sdkGuardMiniLinked = true
