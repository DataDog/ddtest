// Copyright 2014 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../LICENSE.

#include "textflag.h"

TEXT ·rawSysvicall6(SB),NOSPLIT,$0-88
 JMP syscall·rawSysvicall6(SB)
