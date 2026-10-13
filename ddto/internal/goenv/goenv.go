// Package goenv saves and restores Go command settings that ddto replaces
// only for its own go test command, such as a temporary GOWORK, or the GOROOT
// and PATH of a mirrored toolchain, and hands test processes values that ddto
// computes once per invocation.
//
// Importing this package changes nothing. The CLI and its build-tool helpers
// use it to save values; those helpers must keep ddto's settings because the
// tools they run belong to the build. Only the test runtime imports
// goenv/restore, which restores the saved values and receives the handed ones
// while initializing.
package goenv

import "syscall"

// SavedPrefix names the variables that carry a caller's original settings.
const SavedPrefix = "DDTO_ORIGINAL_"

// Settings are the variables that ddto may replace for go test.
var Settings = [...]string{"GOWORK", "GOFLAGS", "GOROOT", "PATH"}

// Save returns the environment entry that records name's current value:
// "=value" when it is set, or an empty value when it is unset.
func Save(name string) string {
	value, ok := syscall.Getenv(name)
	return SaveValue(name, value, ok)
}

// SaveValue returns the environment entry that records value for name, or
// that name is unset: the value a test process would see without ddto.
func SaveValue(name, value string, set bool) string {
	if set {
		return SavedPrefix + name + "==" + value
	}
	return SavedPrefix + name + "="
}

// Restore applies and removes every saved setting. Without saved values, as in
// a binary started outside ddto, it changes nothing.
func Restore() {
	for _, name := range Settings {
		key := SavedPrefix + name
		saved, ok := syscall.Getenv(key)
		if !ok {
			continue
		}
		_ = syscall.Unsetenv(key)
		if saved != "" && saved[0] == '=' {
			_ = syscall.Setenv(name, saved[1:])
		} else {
			_ = syscall.Unsetenv(name)
		}
	}
}

// GitHubJobIDHandoff carries the GitHub Actions job ID that ddto reads once
// from the runner's diagnostics for every test process. An empty value means
// that the diagnostics name none.
const GitHubJobIDHandoff = "DDTO_GITHUB_JOB_ID"

// GitStateHandoff carries the directory where ddto writes the Git metadata,
// repository upload and impacted-test state that it computes once for every
// test process of the same checkout.
const GitStateHandoff = "DDTO_GIT_STATE"

// handoffs are the values that ddto hands to test processes.
var handoffs = [...]string{GitHubJobIDHandoff, GitStateHandoff}

var handed [len(handoffs)]struct {
	value  string
	handed bool
}

// Receive removes the values that ddto hands to test processes from the
// environment and keeps them for the test runtime. Only goenv/restore calls
// it, so it runs before os initializes, like Restore; helpers such as
// test-exec pass the values on to the test process.
func Receive() {
	for i, name := range handoffs {
		handed[i].value, handed[i].handed = syscall.Getenv(name)
		if handed[i].handed {
			_ = syscall.Unsetenv(name)
		}
	}
}

// GitHubJobID returns the job ID that ddto handed to this process, and
// whether it handed one.
func GitHubJobID() (string, bool) { return handed[0].value, handed[0].handed }

// GitState returns the Git state directory that ddto handed to this process,
// and whether it handed one.
func GitState() (string, bool) { return handed[1].value, handed[1].handed }
