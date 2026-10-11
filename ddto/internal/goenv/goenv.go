// Package goenv saves and restores Go command settings that ddto replaces
// only for its own go test command, such as a temporary GOWORK, and hands test
// processes values that ddto computes once per invocation.
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
var Settings = [...]string{"GOWORK", "GOFLAGS"}

// Save returns the environment entry that records name's current value:
// "=value" when it is set, or an empty value when it is unset.
func Save(name string) string {
	if value, ok := syscall.Getenv(name); ok {
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

var gitHubJobID struct {
	value  string
	handed bool
}

// Receive removes the values that ddto hands to test processes from the
// environment and keeps them for the test runtime. Only goenv/restore calls
// it, so it runs before os initializes, like Restore; helpers such as
// test-exec pass the values on to the test process.
func Receive() {
	gitHubJobID.value, gitHubJobID.handed = syscall.Getenv(GitHubJobIDHandoff)
	if gitHubJobID.handed {
		_ = syscall.Unsetenv(GitHubJobIDHandoff)
	}
}

// GitHubJobID returns the job ID that ddto handed to this process, and
// whether it handed one.
func GitHubJobID() (string, bool) { return gitHubJobID.value, gitHubJobID.handed }
