// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package githubjob reads the numeric GitHub Actions job ID from the runner's
// diagnostics files. It holds the SDK's ci_providers.go code, so that ddto can
// resolve the ID once for all of its test processes without CI metadata.
package githubjob

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// JobIDEnv is the environment variable name for the numeric job ID.
const JobIDEnv = "JOB_CHECK_RUN_ID"

// maxDiagFileSize is the maximum file size to read from diagnostics files (10MB)
const maxDiagFileSize = 10 * 1024 * 1024

// diagDirsLinux contains the possible diagnostics directories on Linux
var diagDirsLinux = []string{
	"/home/runner/actions-runner/cached/_diag",
	"/home/runner/actions-runner/_diag",
}

// diagDirsDarwin contains the possible diagnostics directories on macOS
var diagDirsDarwin = []string{
	"/Users/runner/actions-runner/cached/_diag",
	"/Users/runner/actions-runner/_diag",
}

// checkRunIDRegex is used to extract the check_run_id from Worker log files
var checkRunIDRegex = regexp.MustCompile(`"k"\s*:\s*"check_run_id"\s*,\s*"v"\s*:\s*([0-9]+)(?:\.0)?`)

// diagJobData represents the JSON structure of GitHub Actions diagnostics files
type diagJobData struct {
	Job struct {
		D []struct {
			K string `json:"k"`
			V any    `json:"v"`
		} `json:"d"`
	} `json:"job"`
}

// DiagnosticsDirs returns the OS-specific diagnostics directory paths.
func DiagnosticsDirs() []string {
	switch runtime.GOOS {
	case "windows":
		var candidates []string
		// Only add paths with ProgramFiles if the env var is set (avoid relative paths)
		//nolint:forbidigo
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates,
				filepath.Join(programFiles, "actions-runner", "cached", "_diag"),
				filepath.Join(programFiles, "actions-runner", "_diag"),
			)
		}
		//nolint:forbidigo
		if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			candidates = append(candidates,
				filepath.Join(programFilesX86, "actions-runner", "cached", "_diag"),
				filepath.Join(programFilesX86, "actions-runner", "_diag"),
			)
		}
		// Always include hardcoded fallback paths
		candidates = append(candidates,
			`C:\actions-runner\cached\_diag`,
			`C:\actions-runner\_diag`,
		)
		return deduplicatePaths(candidates)
	case "darwin":
		return diagDirsDarwin
	default:
		return diagDirsLinux
	}
}

// deduplicatePaths removes empty and duplicate paths from the slice.
func deduplicatePaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// FromDiagnostics attempts to extract the job ID from GitHub Actions diagnostics files.
// It scans Worker_*.log files in the diagnostics directories, sorted by modification time (newest first).
// debugf receives the SDK's debug messages.
func FromDiagnostics(diagDirs []string, debugf func(format string, args ...any)) (string, bool) {
	for _, diagDir := range diagDirs {
		// Check if directory exists
		if info, err := os.Stat(diagDir); err != nil || !info.IsDir() {
			continue
		}

		// Find Worker_*.log files
		files, err := filepath.Glob(filepath.Join(diagDir, "Worker_*.log"))
		if err != nil {
			debugf("civisibility: error globbing worker logs in %s: %s", diagDir, err.Error())
			continue
		}

		if len(files) == 0 {
			continue
		}

		// Sort by modification time (newest first)
		sort.Slice(files, func(i, j int) bool {
			iInfo, _ := os.Stat(files[i])
			jInfo, _ := os.Stat(files[j])
			if iInfo == nil || jInfo == nil {
				return false
			}
			return iInfo.ModTime().After(jInfo.ModTime())
		})

		// Try to extract job ID from each file
		for _, file := range files {
			if jobID, ok := fromFile(file, debugf); ok {
				return jobID, true
			}
		}
	}

	return "", false
}

// fromFile attempts to extract the job ID from a single Worker log file.
// It first tries JSON parsing, then falls back to regex extraction.
func fromFile(path string, debugf func(format string, args ...any)) (string, bool) {
	// Check file size before reading
	info, err := os.Stat(path)
	if err != nil {
		debugf("civisibility: error stating file %s: %s", path, err.Error())
		return "", false
	}
	if info.Size() > maxDiagFileSize {
		debugf("civisibility: skipping oversized diagnostics file %s (%d bytes)", path, info.Size())
		return "", false
	}
	// Reading a FIFO or device could block indefinitely.
	if !info.Mode().IsRegular() {
		debugf("civisibility: skipping non-regular diagnostics file %s", path)
		return "", false
	}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		debugf("civisibility: error reading file %s: %s", path, err.Error())
		return "", false
	}

	// Try JSON parsing first
	if jobID, ok := fromJSON(content); ok {
		debugf("civisibility: extracted github actions job id via JSON: %s from %s", jobID, path)
		return jobID, true
	}

	// Fall back to regex extraction
	if jobID, ok := fromRegex(content); ok {
		debugf("civisibility: extracted github actions job id via regex: %s from %s", jobID, path)
		return jobID, true
	}

	return "", false
}

// fromJSON attempts to parse the content as JSON and extract the check_run_id.
func fromJSON(content []byte) (string, bool) {
	var data diagJobData
	if err := json.Unmarshal(content, &data); err != nil {
		return "", false
	}

	for _, item := range data.Job.D {
		if item.K == "check_run_id" {
			var jobID string
			switch v := item.V.(type) {
			case float64:
				// Reject non-integer floats (e.g., 12345.5)
				if v != float64(int64(v)) {
					continue
				}
				jobID = strconv.FormatFloat(v, 'f', 0, 64)
			case string:
				jobID = v
			case json.Number:
				jobID = v.String()
			default:
				continue
			}
			jobID = strings.TrimSpace(jobID)
			if jobID != "" && Numeric(jobID) {
				return jobID, true
			}
		}
	}

	return "", false
}

// fromRegex attempts to extract the check_run_id using regex.
func fromRegex(content []byte) (string, bool) {
	matches := checkRunIDRegex.FindSubmatch(content)
	if len(matches) >= 2 {
		jobID := strings.TrimSpace(string(matches[1]))
		if jobID != "" && Numeric(jobID) {
			return jobID, true
		}
	}
	return "", false
}

// Numeric validates that the job ID contains only digits.
func Numeric(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
