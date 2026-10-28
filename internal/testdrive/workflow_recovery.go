package testdrive

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

const maxWorkflowAttempts = 3

// transientWorkflowFailure reports whether rerunning unchanged is the
// expected next step, so an identical repeat is not a reason to stop.
func transientWorkflowFailure(code string) bool {
	return code == "workflow_busy" || code == fileDescriptorsExhaustedCode
}

const workflowCheckpointLifetime = 24 * time.Hour

// Keep one failure checkpoint for the same inputs, even without --resume.
// The failure code and reason are deliberately excluded: a flaky cause that
// alternates codes, or temporary paths in a reason, must not reset retries.
// maxRecordedFailures bounds the attempt history a checkpoint keeps.
const maxRecordedFailures = 5

// The attempt counter ignores the reason, but an attempt that repeats the
// previous failure exactly (same code and reason, same inputs) is marked
// repeated: rerunning a deterministic failure unchanged cannot progress.
func (c *workflowCheckpoint) recordFailure(fingerprint, factsHash, code, reason string) error {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%t\x00%t\x00%s\x00%s\x00%s", c.Root, c.Framework, c.Command, c.Apply, c.All, fingerprint, factsHash, engineIdentity())
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))[:32]
	previousID := c.RunID
	attempts := 1
	failures := c.Failures
	signature := fmt.Sprintf("%x", sha256.Sum256([]byte(code+"\x00"+reason)))
	repeated := false
	previous, err := readCheckpoint(id)
	if err == nil {
		attempts = previous.Attempts + 1
		failures = previous.Failures
		repeated = previous.FailureSignature == signature
	} else if !os.IsNotExist(err) {
		// An expired checkpoint is removed by readCheckpoint. Corruption must not
		// silently overwrite evidence or disable the bounded-attempt contract.
		path, pathErr := checkpointPath(id)
		if pathErr != nil {
			return pathErr
		}
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			return err
		}
	}
	failures = append(failures, code)
	if len(failures) > maxRecordedFailures {
		failures = failures[len(failures)-maxRecordedFailures:]
	}
	c.RunID, c.Attempts, c.Failure, c.Failures = id, attempts, code, failures
	c.FailureSignature, c.repeated = signature, repeated
	if err := c.save(); err != nil {
		return err
	}
	if previousID != id {
		path, err := checkpointPath(previousID)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Only expired, private, recognizable coordinator files are reclaimed. Test
// binaries isolate this whole namespace through the lower stack's TestMain.
func cleanupExpiredCheckpoints() error {
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("ddtest-workflows-%d", os.Getuid()))
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !workspace.PrivateOwnedDirectory(info) {
		return fmt.Errorf("workflow storage must be a private directory owned by the current user")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		partial := strings.HasPrefix(entry.Name(), ".checkpoint-")
		if !partial && (!strings.HasSuffix(entry.Name(), ".json") || !workflowRunID.MatchString(id)) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !info.Mode().IsRegular() || !workspace.OwnedByCurrentUser(info) || !privateFilePermissions(info) || info.Size() > 1<<20 {
			continue
		}
		expired := partial && time.Since(info.ModTime()) > workflowCheckpointLifetime
		if !partial {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var checkpoint workflowCheckpoint
			expired = json.Unmarshal(data, &checkpoint) == nil && checkpoint.RunID == id && filepath.IsAbs(checkpoint.Root) && !checkpoint.Expires.IsZero() && time.Now().After(checkpoint.Expires)
		}
		if !expired {
			continue
		}
		current, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if os.SameFile(info, current) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func privateFilePermissions(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0
}
