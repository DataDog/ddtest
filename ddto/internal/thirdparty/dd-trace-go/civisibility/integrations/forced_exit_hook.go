package integrations

import _ "unsafe" // ddtest instrument's testing overlay names this variable

// forcedExitHook is the function that testing's exit hook calls before
// os.Exit ends the process. testing declares the same variable with no value
// and calls it only when it is not nil, so a binary without Mini keeps the
// native exit; the linker keeps this definition, which has a value.
//
//go:linkname forcedExitHook
var forcedExitHook func() = forcedExit
