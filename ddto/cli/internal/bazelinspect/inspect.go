// Package bazelinspect is ddtest instrument bazel inspect: it lists the
// payloads and target sidecars of Bazel test.outputs directories and decodes
// them in parallel, so that the rules_test_optimization doctor validates them
// without reading every file itself.
//
// The doctor keeps its checks and messages. This package reports the facts
// that it derives from each file, as the doctor's Python would, and marks the
// files that it cannot decode; the doctor reads those itself, so that its
// errors and its acceptance of Python-only JSON, such as NaN, do not change.
package bazelinspect

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const metadataName = "bazel_target_metadata.json"

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// Request lists the test.outputs directories to inspect.
type Request struct {
	Version int      `json:"schema_version"`
	Outputs []string `json:"outputs"`
}

// Report describes each requested output, in the request's order. File paths
// are relative to their output and slash-separated, so that the doctor joins
// them to the path that it requested; lists are in directory order, and the
// doctor sorts them.
type Report struct {
	Version int      `json:"schema_version"`
	Outputs []Output `json:"outputs"`
}

// Output is one test.outputs directory. JSON and msgpack payloads are the
// files below payloads; sidecars may be anywhere below the output.
type Output struct {
	Path            string     `json:"path"`
	JSONPayloads    []Payload  `json:"json_payloads"`
	MsgpackPayloads []string   `json:"msgpack_payloads"`
	Metadata        []Metadata `json:"metadata"`
}

// Payload is a JSON payload. Without Decoded, the other fields are unset and
// the doctor reads the file itself.
type Payload struct {
	Path    string `json:"path"`
	Decoded bool   `json:"decoded"`
	// TestEvents counts typed test events and legacy test spans, as
	// test_result_event_count in topt_runtime/bep_execution.py.
	TestEvents int `json:"test_events"`
	// EmptySession is completed_empty_test_session in the same file.
	EmptySession bool `json:"empty_session"`
}

// Metadata is a target sidecar and, when it decodes, its document as written.
type Metadata struct {
	Path     string          `json:"path"`
	Document json.RawMessage `json:"document,omitempty"`
}

// Inspect lists and decodes the outputs with up to workers at once, or one per
// CPU when workers is not positive.
func Inspect(outputs []string, workers int) Report {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	report := Report{Version: 1, Outputs: make([]Output, len(outputs))}
	indexes := make(chan int)
	var group sync.WaitGroup
	for range min(workers, max(len(outputs), 1)) {
		group.Go(func() {
			for index := range indexes {
				report.Outputs[index] = inspectOutput(outputs[index])
			}
		})
	}
	for index := range outputs {
		indexes <- index
	}
	close(indexes)
	group.Wait()
	return report
}

func inspectOutput(path string) Output {
	output := Output{Path: path, JSONPayloads: []Payload{}, MsgpackPayloads: []string{}, Metadata: []Metadata{}}
	payloads := join(path, "payloads")
	if _, err := os.Stat(payloads); err == nil {
		for _, file := range matchingFiles(payloads, isPayloadName) {
			if hasSuffix(filepath.Base(file), ".json") {
				payload := decodePayload(file)
				payload.Path = relative(path, file)
				output.JSONPayloads = append(output.JSONPayloads, payload)
			} else {
				output.MsgpackPayloads = append(output.MsgpackPayloads, relative(path, file))
			}
		}
	}
	for _, file := range matchingFiles(path, isMetadataName) {
		metadata := decodeMetadata(file)
		metadata.Path = relative(path, file)
		output.Metadata = append(output.Metadata, metadata)
	}
	return output
}

// join appends a name to a directory without cleaning the result, as Python's
// os.path.join and pathlib do. Cleaning would resolve link/.. lexically and
// leave the link's target for another directory.
func join(directory, name string) string {
	if strings.HasSuffix(directory, string(os.PathSeparator)) {
		return directory + name
	}
	return directory + string(os.PathSeparator) + name
}

// relative names a file below output, which the walk joined to it.
func relative(output, file string) string {
	return filepath.ToSlash(strings.TrimPrefix(file, join(output, "")))
}

// matchingFiles returns the files below root whose names match, as the
// doctor's _matching_files and Path.rglob find them: it descends into real
// directories only, keeps links to regular files, and skips a directory that
// it cannot list.
func matchingFiles(root string, match func(string) bool) []string {
	var files []string
	pending := []string{root}
	for len(pending) > 0 {
		directory := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := join(directory, entry.Name())
			switch {
			case entry.IsDir():
				pending = append(pending, path)
			case match(entry.Name()) && isRegularFile(entry, path):
				files = append(files, path)
			}
		}
	}
	return files
}

func isRegularFile(entry os.DirEntry, path string) bool {
	if entry.Type().IsRegular() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isPayloadName(name string) bool {
	return hasSuffix(name, ".json") || hasSuffix(name, ".msgpack") || hasSuffix(name, ".msgpack.gz")
}

func isMetadataName(name string) bool {
	return foldName(name) == metadataName
}

// hasSuffix matches the doctor's "*<suffix>" patterns, which fnmatch applies
// to the case-folded name on Windows only.
func hasSuffix(name, suffix string) bool {
	return strings.HasSuffix(foldName(name), suffix)
}

func foldName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

// readJSON returns a file's text as the doctor's utf-8-sig reader sees it, or
// false when Python would fail to read or decode it as JSON.
func readJSON(path string) ([]byte, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	raw = bytes.TrimPrefix(raw, utf8BOM)
	return raw, utf8.Valid(raw)
}

func decodeMetadata(path string) Metadata {
	raw, ok := readJSON(path)
	if !ok || !json.Valid(raw) {
		return Metadata{Path: path}
	}
	return Metadata{Path: path, Document: raw}
}

func decodePayload(path string) Payload {
	raw, ok := readJSON(path)
	if !ok {
		return Payload{Path: path}
	}
	object, isObject, err := decodeObject(raw)
	if err != nil {
		return Payload{Path: path}
	}
	payload := Payload{Path: path, Decoded: true}
	if isObject {
		payload.TestEvents, payload.EmptySession = testFacts(object)
	}
	return payload
}

// decodeObject validates raw as JSON and decodes a top-level object one level
// deep. Map keys stay exact and the last duplicate wins, as in Python's dict.
func decodeObject(raw []byte) (map[string]json.RawMessage, bool, error) {
	if first(raw) != '{' {
		if !json.Valid(raw) {
			return nil, false, errors.New("invalid JSON")
		}
		return nil, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false, err
	}
	return object, true, nil
}

// object decodes a JSON object value, or returns false for another kind.
func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if first(raw) != '{' {
		return nil, false
	}
	var fields map[string]json.RawMessage
	return fields, json.Unmarshal(raw, &fields) == nil
}

func first(raw []byte) byte {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 {
		return 0
	}
	return trimmed[0]
}

// stringValue returns a JSON string's value, or false for another kind.
func stringValue(raw json.RawMessage) (string, bool) {
	if first(raw) != '"' {
		return "", false
	}
	var value string
	return value, json.Unmarshal(raw, &value) == nil
}

func isString(raw json.RawMessage, want string) bool {
	value, ok := stringValue(raw)
	return ok && value == want
}

// isNull reports a missing or null field, which Python's dict.get returns as None.
func isNull(raw json.RawMessage) bool {
	return raw == nil || string(bytes.TrimSpace(raw)) == "null"
}

// testFacts computes test_result_event_count and completed_empty_test_session
// for a decoded payload object.
func testFacts(payload map[string]json.RawMessage) (int, bool) {
	raw, present := payload["events"]
	if !present {
		return 0, false
	}
	if first(raw) != '[' {
		return 0, false
	}
	var events []json.RawMessage
	if json.Unmarshal(raw, &events) != nil {
		return 0, false
	}
	type decodedEvent struct {
		fields  map[string]json.RawMessage
		content map[string]json.RawMessage
	}
	decoded := make([]decodedEvent, 0, len(events))
	count := 0
	for _, raw := range events {
		fields, ok := object(raw)
		if !ok {
			continue
		}
		content, contentIsObject := object(fields["content"])
		decoded = append(decoded, decodedEvent{fields, content})
		eventType := fields["type"]
		if contentIsObject && (isString(eventType, "test") ||
			(isNull(eventType) || isString(eventType, "span")) && isString(content["type"], "test")) {
			count++
		}
	}
	if count > 0 {
		return count, false
	}
	for _, event := range decoded {
		if !isString(event.fields["type"], "test_session_end") || event.content == nil {
			continue
		}
		if emptySession(event.content) {
			return 0, true
		}
	}
	return 0, false
}

// emptySession is the zero-tests check of completed_empty_test_session for
// one test_session_end content object.
func emptySession(content map[string]json.RawMessage) bool {
	meta, metaOK := optionalObject(content["meta"])
	metrics, metricsOK := optionalObject(content["metrics"])
	if !metaOK || !metricsOK {
		return false
	}
	return isString(meta["test.session.empty_reason"], "zero_tests") &&
		isString(meta["test.status"], "skip") &&
		isPositiveInteger(content["test_session_id"]) &&
		isNumericZero(metrics["test.exit_code"]) &&
		(isNumericZero(content["error"]) || string(bytes.TrimSpace(content["error"])) == "false")
}

// optionalObject is content.get(key, {}): a missing field is an empty object.
func optionalObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if raw == nil {
		return map[string]json.RawMessage{}, true
	}
	return object(raw)
}

// isPositiveInteger matches a value that Python decodes as an int above zero.
// JSON integers have no leading zeros, so any other unsigned literal is positive.
func isPositiveInteger(raw json.RawMessage) bool {
	literal := string(bytes.TrimSpace(raw))
	if literal == "" || literal == "0" || strings.ContainsAny(literal, ".eE-") {
		return false
	}
	for _, digit := range literal {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// isNumericZero matches a JSON number that Python compares equal to 0,
// whether it decodes as an int or a float.
func isNumericZero(raw json.RawMessage) bool {
	literal := string(bytes.TrimSpace(raw))
	if literal == "" || (literal[0] != '-' && (literal[0] < '0' || literal[0] > '9')) {
		return false
	}
	value, err := strconv.ParseFloat(literal, 64)
	var numberError *strconv.NumError
	if errors.As(err, &numberError) && numberError.Err == strconv.ErrRange {
		// Python's float underflows to zero and overflows to infinity, as here.
		return value == 0
	}
	return err == nil && value == 0
}
