package cidelivery

import _ "unsafe" // ddtest instrument's goleak edit names this variable

// leakCheckHook is what goleak's Find calls before it checks for leaks. The
// edited goleak declares the same variable with no value and calls it only
// when it is not nil, so a binary without Mini checks leaks natively; the
// linker keeps this definition, which has a value.
//
//go:linkname leakCheckHook
var leakCheckHook func() func() = PrepareLeakCheck
