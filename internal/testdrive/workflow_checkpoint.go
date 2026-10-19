package testdrive

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

var workflowRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}$`)

// Checkpoints retain only a bounded plan/preflight receipt, never test results,
// telemetry, service processes or installations. Every resume executes new pairs.
type workflowCheckpoint struct {
	Engine      string                 `json:"engine"`
	RunID       string                 `json:"run_id"`
	Root        string                 `json:"root"`
	Framework   string                 `json:"framework"`
	Apply       bool                   `json:"apply"`
	All         bool                   `json:"all"`
	Command     string                 `json:"command"`
	Expires     time.Time              `json:"expires"`
	Fingerprint string                 `json:"input_fingerprint,omitempty"`
	Node        string                 `json:"node,omitempty"`
	FactsSHA256 string                 `json:"facts_sha256,omitempty"`
	Tracer      string                 `json:"tracer,omitempty"`
	Plan        *onboard.ExecutionPlan `json:"plan,omitempty"`
	Failure     string                 `json:"failure,omitempty"`
	Attempts    int                    `json:"attempts,omitempty"`
}

func checkpointPath(id string) (string, error) {
	if !workflowRunID.MatchString(id) {
		return "", fmt.Errorf("invalid workflow run ID")
	}
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("ddtest-workflows-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("workflow storage must be a private real directory")
	}
	return filepath.Join(directory, id+".json"), nil
}

func openCheckpoint(id, root, framework, command string, apply, all bool) (*workflowCheckpoint, error) {
	if id == "" {
		return &workflowCheckpoint{RunID: rand.Text(), Root: root, Framework: framework, Command: command, Apply: apply, All: all, Expires: time.Now().Add(24 * time.Hour)}, nil
	}
	checkpoint, err := readCheckpoint(id)
	if err != nil {
		return nil, err
	}
	if checkpoint.RunID != id || checkpoint.Root != root || checkpoint.Framework != framework || checkpoint.Command != command || checkpoint.Apply != apply || checkpoint.All != all {
		return nil, fmt.Errorf("workflow checkpoint does not match this repository, framework or scope")
	}
	return checkpoint, nil
}

func readCheckpoint(id string) (*workflowCheckpoint, error) {
	path, err := checkpointPath(id)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, fmt.Errorf("invalid workflow checkpoint")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var checkpoint workflowCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, err
	}
	if time.Now().After(checkpoint.Expires) {
		_ = os.Remove(path)
		return nil, fmt.Errorf("workflow checkpoint expired; start a fresh workflow")
	}

	return &checkpoint, nil
}

func (c *workflowCheckpoint) save() error {
	path, err := checkpointPath(c.RunID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("workflow checkpoint exceeds 1 MiB")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func (c *workflowCheckpoint) remove() error {
	path, err := checkpointPath(c.RunID)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

var engineIdentity = sync.OnceValue(func() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
})

func (c *workflowCheckpoint) reusable(fingerprint, node, facts, selector string, explicit bool) bool {
	return c.Tracer != "" && c.Fingerprint == fingerprint && c.Node == node && c.FactsSHA256 == facts && c.Engine != "" && c.Engine == engineIdentity() && (!explicit || c.Tracer == selector)
}
