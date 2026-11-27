package bazelupload

import (
	"fmt"
	"slices"
)

// A source's status in the report.
const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
	statusCancelled = "cancelled"
)

type FileResult struct {
	ID               string        `json:"task_id"`
	Path             string        `json:"source_path"`
	Kind             Kind          `json:"payload_type"`
	Status           string        `json:"status"`
	Events           int           `json:"events"`
	TestEvents       int           `json:"test_events"`
	EmptyTestSession bool          `json:"empty_test_session"`
	ChunksCreated    int           `json:"chunks_created"`
	ChunksUploaded   int           `json:"chunks_uploaded"`
	ChunksFailed     int           `json:"chunks_failed"`
	Deleted          bool          `json:"source_deleted"`
	Failure          string        `json:"failure_code,omitempty"`
	FailureMessage   string        `json:"failure_message,omitempty"`
	Warnings         []string      `json:"warning_codes,omitempty"`
	SourceBytes      int           `json:"source_bytes"`
	PreparedBytes    int           `json:"prepared_body_bytes"`
	Requests         RequestCounts `json:"requests"`
	SharedRequests   int           `json:"shared_requests,omitempty"`
	// RequestIDs names the test requests that carried the source when
	// batching; Requests counts them, as the rules uploader's Python batches
	// count theirs.
	RequestIDs []string `json:"request_ids,omitempty"`
	// batch is the part of Requests that came from batch requests, which the
	// report's totals count once each.
	batch RequestCounts
}

// BatchRequest is one test request sent with batching, for one or several
// sources.
type BatchRequest struct {
	ID       string   `json:"request_id"`
	Sources  []string `json:"source_ids"`
	Attempts int      `json:"attempts"`
	// Succeeded is null when nothing was sent, as in a dry run.
	Succeeded *bool `json:"succeeded"`
}

type RequestCounts struct {
	Planned   int `json:"planned"`
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Retries   int `json:"retries"`
}

type FileCounts struct {
	Discovered int `json:"discovered"`
	Eligible   int `json:"eligible"`
	Processed  int `json:"processed"`
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
	Cancelled  int `json:"cancelled"`
	Deleted    int `json:"deleted"`
	Retained   int `json:"retained"`
}

type Report struct {
	Version        int            `json:"schema_version"`
	Mode           string         `json:"mode"`
	Result         string         `json:"result"`
	ExitCode       int            `json:"exit_code"`
	Workers        int            `json:"workers"`
	ElapsedSeconds float64        `json:"elapsed_seconds"`
	Files          FileCounts     `json:"files"`
	Requests       RequestCounts  `json:"requests"`
	SourceBytes    int64          `json:"processed_source_bytes"`
	PreparedBytes  int64          `json:"prepared_body_bytes"`
	SharedRequests int            `json:"shared_requests"`
	Warnings       map[string]int `json:"warnings"`
	Sources        []FileResult   `json:"sources"`
	BatchRequests  []BatchRequest `json:"batch_requests,omitempty"`
}

func (result *FileResult) fail(code string, err error) {
	result.Status, result.Failure = statusFailed, code
	result.FailureMessage = err.Error()
}

func (result *FileResult) warn(code string) {
	if !slices.Contains(result.Warnings, code) {
		result.Warnings = append(result.Warnings, code)
	}
}

func (result *FileResult) record(delivery delivery) {
	result.Requests.add(delivery)
	result.PreparedBytes += delivery.Bytes
}

func (counts *RequestCounts) add(delivery delivery) {
	counts.Planned++
	counts.Attempted += delivery.Attempts
	counts.Retries += max(0, delivery.Attempts-1)
	if delivery.Attempts != 0 {
		if delivery.Success {
			counts.Succeeded++
		} else {
			counts.Failed++
		}
	}
}

func (report *Report) aggregate() {
	report.Files.Eligible = len(report.Sources)
	report.Files.Discovered = report.Files.Eligible
	report.Warnings = make(map[string]int)
	report.Result = "success"
	for _, result := range report.Sources {
		switch result.Status {
		case statusSucceeded:
			report.Files.Succeeded++
		case statusFailed:
			report.Files.Failed++
		case statusSkipped:
			report.Files.Skipped++
		default:
			report.Files.Cancelled++
		}
		if result.Deleted {
			report.Files.Deleted++
		}
		report.Requests.Planned += result.Requests.Planned - result.batch.Planned
		report.Requests.Attempted += result.Requests.Attempted - result.batch.Attempted
		report.Requests.Retries += result.Requests.Retries - result.batch.Retries
		report.Requests.Succeeded += result.Requests.Succeeded - result.batch.Succeeded
		report.Requests.Failed += result.Requests.Failed - result.batch.Failed
		report.SharedRequests += result.SharedRequests
		report.SourceBytes += int64(result.SourceBytes)
		report.PreparedBytes += int64(result.PreparedBytes)
		for _, warning := range result.Warnings {
			report.Warnings[warning]++
		}
	}
	for _, request := range report.BatchRequests {
		report.Requests.Planned++
		report.Requests.Attempted += request.Attempts
		report.Requests.Retries += max(0, request.Attempts-1)
		if request.Succeeded != nil && *request.Succeeded {
			report.Requests.Succeeded++
		} else if request.Succeeded != nil {
			report.Requests.Failed++
		}
	}
	report.Files.Processed = report.Files.Eligible - report.Files.Cancelled
	report.Files.Retained = report.Files.Eligible - report.Files.Deleted
	if report.Files.Failed+report.Files.Cancelled > 0 {
		report.ExitCode, report.Result = 1, "failure"
		if report.Files.Succeeded > 0 || report.Requests.Succeeded > 0 {
			report.Result = "partial_failure"
		}
	}
}

func (report Report) summary() string {
	return fmt.Sprintf("[dd-uploader] summary: mode=%s result=%s exit_code=%d workers=%d elapsed=%.2fs\n"+
		"[dd-uploader] files: discovered=%d eligible=%d processed=%d succeeded=%d failed=%d skipped=%d cancelled=%d\n"+
		"[dd-uploader] requests: planned=%d attempted=%d succeeded=%d failed=%d retries=%d\n"+
		"[dd-uploader] cleanup: deleted=%d retained=%d\n",
		report.Mode, report.Result, report.ExitCode, report.Workers, report.ElapsedSeconds,
		report.Files.Discovered, report.Files.Eligible, report.Files.Processed, report.Files.Succeeded, report.Files.Failed, report.Files.Skipped, report.Files.Cancelled,
		report.Requests.Planned, report.Requests.Attempted, report.Requests.Succeeded, report.Requests.Failed, report.Requests.Retries,
		report.Files.Deleted, report.Files.Retained)
}
