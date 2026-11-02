package offlineagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/tinylib/msgp/msgp"
)

type payloadKind string

const (
	kindTests     payloadKind = "tests"
	kindCoverage  payloadKind = "coverage"
	kindTelemetry payloadKind = "telemetry"
)

// payloadFiles writes payload files with the names the libraries' Bazel
// payload-files mode uses.
type payloadFiles struct {
	dir string
	pid int
	now func() time.Time
	seq atomic.Uint64
}

func newPayloadFiles(dir string) *payloadFiles {
	return &payloadFiles{dir: dir, pid: os.Getpid(), now: time.Now}
}

// writeMsgpack converts a msgpack payload to JSON the way the libraries do
// before writing payload files.
func (f *payloadFiles) writeMsgpack(kind payloadKind, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%s payload is empty", kind)
	}
	var converted bytes.Buffer
	if _, err := msgp.CopyToJSON(&converted, bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("convert %s payload from msgpack to JSON: %w", kind, err)
	}
	// CopyToJSON reports no error when a payload ends between two map
	// entries, and returns the JSON so far.
	if !json.Valid(converted.Bytes()) {
		return fmt.Errorf("%s payload is truncated msgpack", kind)
	}
	return f.write(kind, converted.Bytes())
}

func (f *payloadFiles) writeJSON(kind payloadKind, payload []byte) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return fmt.Errorf("invalid %s JSON payload: %w", kind, err)
	}
	return f.write(kind, compact.Bytes())
}

// writeTelemetry keeps telemetry bodies as they arrive.
func (f *payloadFiles) writeTelemetry(body []byte) error {
	if len(body) == 0 {
		return errors.New("telemetry payload is empty")
	}
	return f.write(kindTelemetry, body)
}

// write saves data under a temporary name and renames it, so whoever collects
// the files never reads a partial one. Telemetry names sort by arrival,
// because replaying telemetry depends on its order.
func (f *payloadFiles) write(kind payloadKind, data []byte) error {
	dir := filepath.Join(f.dir, string(kind))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the %s payload directory: %w", kind, err)
	}
	seq := f.seq.Add(1)
	name := fmt.Sprintf("%s-%d-%d-%d.json", kind, f.now().UnixNano(), f.pid, seq)
	if kind == kindTelemetry {
		name = fmt.Sprintf("%s-%020d-%d.json", kind, seq, f.pid)
	}
	temp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create the %s payload file: %w", kind, err)
	}
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return errors.Join(fmt.Errorf("write the %s payload file: %w", kind, err), os.Remove(temp.Name()))
	}
	if err := os.Rename(temp.Name(), filepath.Join(dir, name)); err != nil {
		return errors.Join(fmt.Errorf("save the %s payload file: %w", kind, err), os.Remove(temp.Name()))
	}
	return nil
}
