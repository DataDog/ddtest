//go:build unix

package unix

import "strings"

// KernelInfo is the native kernel identity used in CI metadata.
type KernelInfo struct{ Name, Release, Version string }

// Trim only trailing padding, retaining interior NUL bytes exactly as before.
// syscall uses int8 arrays on some targets and uint8 on others.
func utsString[T ~int8 | ~uint8](values []T) string {
	bytes := make([]byte, len(values))
	for i, v := range values {
		bytes[i] = byte(v)
	}
	return strings.TrimRight(string(bytes), "\x00")
}
