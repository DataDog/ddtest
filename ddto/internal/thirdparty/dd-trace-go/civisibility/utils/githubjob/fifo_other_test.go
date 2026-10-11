//go:build !unix

package githubjob

import "testing"

func makeFIFO(*testing.T, string) bool { return false }
