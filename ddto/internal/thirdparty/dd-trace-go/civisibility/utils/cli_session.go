// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"bufio"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv/crashout"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// ddto owns one test session per go test command: it sends the session event,
// which lasts the command's wall time and has go test's exit status. It hands
// the session to its test processes in a directory, through goenv; each
// process creates its modules, suites and tests in that session and sends
// them itself. Each process also appends to its own record in the directory
// the identities of its modules and suites, when they start and end, and the
// session tags it sets, once it closes CI Visibility. The record holds no test
// event. When a process crashes before it closes, ddto finds there the
// containers that the process left open, and the crash output that
// crashout sends to the file next to the record.
const (
	// CLISessionFile holds the CLISession.
	CLISessionFile = "session.json"
	// CLISessionRecordExt is the extension of a process's record, whose name
	// is otherwise its crash output's.
	CLISessionRecordExt = ".jsonl"
)

// CLISession is the session that ddto hands to its test processes.
type CLISession struct {
	// ID is the session's ID, its events' test_session_id.
	ID uint64 `json:"id,string"`
	// Command is ddto's command, the session's test.command.
	Command string `json:"command"`
	// WorkingDirectory is the absolute directory where ddto runs go test.
	WorkingDirectory string `json:"working_directory"`
}

// CLISessionRecordLine is a line of a process's record.
type CLISessionRecordLine struct {
	// Kind is module, module_end, suite, suite_end or closed.
	Kind string `json:"kind"`
	// ID is the module's or the suite's ID; Module is a suite's module ID.
	ID     uint64 `json:"id,string,omitempty"`
	Module uint64 `json:"module,string,omitempty"`
	// Name, Service and Start describe a module or a suite.
	Name    string    `json:"name,omitempty"`
	Service string    `json:"service,omitempty"`
	Start   time.Time `json:"start,omitzero"`
	// ExitCode and Tags are the closed session's.
	ExitCode int            `json:"exit_code,omitempty"`
	Tags     map[string]any `json:"tags,omitempty"`
}

// WriteCLISession writes the session that ddto hands to its test processes
// in dir.
func WriteCLISession(dir string, session CLISession) error {
	return WriteCLIGitStateFile(dir, CLISessionFile, session)
}

// CLISessionRecord appends to this process's record. Its callers hold the
// locks of the modules, suites and session that they record, so it logs
// nothing, since a client can replace the logger: Stats reports to a caller
// that holds none.
type CLISessionRecord struct {
	mu       sync.Mutex
	file     *os.File
	lines    int
	failures int
	firstErr error
}

var (
	cliSessionOnce   sync.Once
	cliSession       *CLISession
	cliSessionRecord *CLISessionRecord
)

// HandedCLISession returns the session that ddto handed to this test process
// and the record where the process writes, or nil when ddto handed none or
// the process cannot join it: the process then owns its session, as without
// ddto.
func HandedCLISession() (*CLISession, *CLISessionRecord) {
	cliSessionOnce.Do(func() {
		dir, ok := goenv.TestSession()
		if !ok {
			return
		}
		var session CLISession
		if err := ReadCLIGitStateFile(dir, CLISessionFile, &session); err != nil || session.ID == 0 {
			log.Warn("civisibility: ddto's test session in %s is unusable; this process reports its own session: %v", dir, err)
			return
		}
		stem, err := crashout.Stem()
		if err != nil {
			// The record still lets ddto close the containers of a process
			// that dies, without its crash output.
			log.Warn("civisibility: no crash output for ddto's test session: %s", err.Error())
		}
		var file *os.File
		if stem != "" {
			file, err = openUnlogged(stem+CLISessionRecordExt, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
		} else {
			file, err = createUniqueUnlogged(dir, "process-", CLISessionRecordExt)
		}
		if err != nil {
			log.Warn("civisibility: cannot record this process in ddto's test session; it reports its own session: %s", err.Error())
			return
		}
		cliSession, cliSessionRecord = &session, &CLISessionRecord{file: file}
		crash := "none"
		if stem != "" {
			crash = stem + ".crash"
		}
		log.Debug("civisibility: joined ddto's test session id=%d record=%s crash_output=%s", session.ID, file.Name(), crash)
	})
	return cliSession, cliSessionRecord
}

// createUniqueUnlogged creates a new file for appending in dir, named
// prefix, a random part and suffix, as os.CreateTemp does, without recording
// it in Go's test log.
func createUniqueUnlogged(dir, prefix, suffix string) (*os.File, error) {
	for range 100 {
		name := filepath.Join(dir, prefix+strconv.FormatUint(rand.Uint64(), 36)+suffix)
		file, err := openUnlogged(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
		if !errors.Is(err, os.ErrExist) {
			return file, err
		}
	}
	return nil, errors.New("cannot create a unique file in " + dir)
}

// Append writes line to the record. A failure only loses what ddto would
// close if the process crashed.
func (r *CLISessionRecord) Append(line CLISessionRecordLine) {
	if r == nil {
		return
	}
	data, err := json.Marshal(line)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		_, err = r.file.Write(append(data, '\n'))
	}
	if err != nil {
		r.failures++
		if r.firstErr == nil {
			r.firstErr = err
		}
		return
	}
	r.lines++
}

// NewCLISessionRecordForTesting returns a record that appends to file.
func NewCLISessionRecordForTesting(file *os.File) *CLISessionRecord {
	return &CLISessionRecord{file: file}
}

// Stats returns how many lines the record holds, how many it could not write,
// and the first error.
func (r *CLISessionRecord) Stats() (lines, failures int, first error) {
	if r == nil {
		return 0, 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lines, r.failures, r.firstErr
}

// ReadCLISessionRecord reads a process's record. A last line that the
// process did not finish writing is left out.
func ReadCLISessionRecord(path string) ([]CLISessionRecordLine, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []CLISessionRecordLine
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 64<<20)
	for scanner.Scan() {
		var line CLISessionRecordLine
		if json.Unmarshal(scanner.Bytes(), &line) == nil {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}
