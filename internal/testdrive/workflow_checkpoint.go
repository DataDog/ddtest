package testdrive

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

var workflowRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}$`)

// Checkpoints retain only a bounded plan/preflight receipt, never test results,
// telemetry, service processes or installations. Every resume executes new pairs.
type workflowCheckpoint struct {
	Engine                 string                 `json:"engine"`
	RunID                  string                 `json:"run_id"`
	Root                   string                 `json:"root"`
	Framework              string                 `json:"framework"`
	Apply                  bool                   `json:"apply"`
	All                    bool                   `json:"all"`
	Command                string                 `json:"command"`
	Expires                time.Time              `json:"expires"`
	Fingerprint            string                 `json:"input_fingerprint,omitempty"`
	Node                   string                 `json:"node,omitempty"`
	FactsFingerprint       string                 `json:"facts_input_fingerprint,omitempty"`
	FactsDependencyHandoff bool                   `json:"facts_dependency_handoff,omitempty"`
	FactsSHA256            string                 `json:"facts_sha256,omitempty"`
	Tracer                 string                 `json:"tracer,omitempty"`
	Plan                   *onboard.ExecutionPlan `json:"plan,omitempty"`
	Failure                string                 `json:"failure,omitempty"`
	Attempts               int                    `json:"attempts,omitempty"`
	// Failures keeps the codes of earlier stopped attempts for this input, so
	// a later passing run still discloses that it took retries.
	Failures []string `json:"failures,omitempty"`

	// The dev dependencies a setup_dependencies_missing handoff prescribed; their
	// manifest and lockfile changes may be extended without --allow-dirty.
	DependencyPackages []string `json:"dependency_packages,omitempty"`
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
	if !workspace.PrivateOwnedDirectory(info) {
		return "", fmt.Errorf("workflow storage must be a private real directory")
	}
	return filepath.Join(directory, id+".json"), nil
}

// errCheckpointNotFound means a --resume run ID has no checkpoint: the run
// finished (its checkpoint is removed) or never existed here.
var errCheckpointNotFound = errors.New("no resumable workflow with this run ID; it finished or expired")

func openCheckpoint(id, root, framework, command string, apply, all bool) (*workflowCheckpoint, error) {
	if id == "" {
		return &workflowCheckpoint{RunID: rand.Text(), Root: root, Framework: framework, Command: command, Apply: apply, All: all, Expires: time.Now().Add(workflowCheckpointLifetime)}, nil
	}
	checkpoint, err := readCheckpoint(id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errCheckpointNotFound
	}
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
	if !info.Mode().IsRegular() || !workspace.OwnedByCurrentUser(info) || !privateFilePermissions(info) || info.Size() > 1<<20 {
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

// A facts binding is a private copy of the checkpoint, keyed by the snapshot
// ddtest's own setup edit produced. A fresh run with the same facts, such as
// the onboard hand-out, can then reuse them without --resume.
func factsBindingID(root, framework, command, fingerprint, facts string) string {
	key := fmt.Sprintf("facts\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", root, framework, command, fingerprint, facts, engineIdentity())
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))[:32]
}

// saveFactsBinding records accepted facts only once an owned edit has moved the
// checkout away from the snapshot they were written for.
func (c *workflowCheckpoint) saveFactsBinding() error {
	if c.FactsSHA256 == "" || c.FactsFingerprint == "" || c.FactsFingerprint == c.Fingerprint || c.Plan == nil {
		return nil
	}
	binding := *c
	binding.RunID = factsBindingID(c.Root, c.Framework, c.Command, c.Fingerprint, c.FactsSHA256)
	binding.Failure, binding.Attempts = "", 0
	binding.Expires = time.Now().Add(workflowCheckpointLifetime)
	return binding.save()
}

// restoreFactsBinding attaches a recorded binding for these exact facts and
// inputs; anything else leaves the fresh checkpoint untouched.
func (c *workflowCheckpoint) restoreFactsBinding(fingerprint, facts string) {
	binding, err := readCheckpoint(factsBindingID(c.Root, c.Framework, c.Command, fingerprint, facts))
	if err != nil || binding.Root != c.Root || binding.Framework != c.Framework || binding.Command != c.Command ||
		binding.Fingerprint != fingerprint || binding.FactsSHA256 != facts || binding.Engine == "" || binding.Engine != engineIdentity() || binding.Plan == nil {
		return
	}
	c.Engine, c.Fingerprint, c.FactsSHA256, c.FactsFingerprint, c.Plan = binding.Engine, binding.Fingerprint, binding.FactsSHA256, binding.FactsFingerprint, binding.Plan
}

func (c *workflowCheckpoint) reusable(fingerprint, node, facts, selector string, explicit bool) bool {
	return c.Tracer != "" && c.Fingerprint == fingerprint && c.Node == node && c.FactsSHA256 == facts && c.Engine != "" && c.Engine == engineIdentity() && (!explicit || c.Tracer == selector)
}
