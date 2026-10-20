package testdrive

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type probeOwnership struct {
	Root string `json:"root"`
	Path string `json:"path"`
	Hash string `json:"sha256"`
	Peer string `json:"peer,omitempty"`
}

// Recovery is called only after the repository workflow lock is acquired.
// No PID or filename prefix is evidence that an abandoned file belongs to us.
func probeOwnershipDirectory(root string) (string, error) {
	root = canonicalWorkflowRoot(root)
	reports := filepath.Join(root, ".testoptimization")
	if err := os.MkdirAll(reports, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(reports)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("probe ownership storage must stay inside the repository")
	}
	path := filepath.Join(reports, ".probes")
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	info, err = os.Lstat(path)
	if err != nil {
		return "", err
	}
	// Git does not preserve private directory modes. Never trust recovery records
	// delivered in a checkout or through a symlink as locally created ownership.
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("probe ownership storage must be private: %s", path)
	}
	return path, nil
}

func probeOwnershipPath(root, probe string) (string, error) {
	directory, err := probeOwnershipDirectory(root)
	return filepath.Join(directory, fmt.Sprintf("%x.json", sha256.Sum256([]byte(probe)))), err
}

func recordProbe(root, probe string, contents []byte) error {
	record := probeOwnership{Root: canonicalWorkflowRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256(contents))}
	return writeProbeRecord(record)
}

func writeProbeRecord(record probeOwnership) error {
	path, err := probeOwnershipPath(record.Root, record.Path)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "record-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func readProbeOwnership(path string) (probeOwnership, error) {
	var record probeOwnership
	info, err := os.Lstat(path)
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return record, fmt.Errorf("invalid probe ownership record: %s", path)
	}
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &record)
	}
	return record, err
}

func ownedProbeContents(record probeOwnership) ([]byte, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(record.Path))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(record.Root, filepath.Join(parent, filepath.Base(record.Path)))
	if err != nil || !filepath.IsLocal(relative) {
		return nil, fmt.Errorf("owned probe path escapes its repository: %s", record.Path)
	}
	info, err := os.Lstat(record.Path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("owned probe is no longer a regular file: %s", record.Path)
	}
	if record.Peer != "" {
		directory, err := probeOwnershipDirectory(record.Root)
		if err != nil {
			return nil, err
		}
		if filepath.Dir(record.Peer) != directory {
			return nil, fmt.Errorf("invalid probe ownership peer")
		}
		peer, err := os.Lstat(record.Peer)
		if err != nil {
			return nil, err
		}
		if !peer.Mode().IsRegular() || !os.SameFile(info, peer) {
			return nil, fmt.Errorf("probe was replaced or never published; preserve it: %s", record.Path)
		}
	}
	data, err := os.ReadFile(record.Path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if hash != record.Hash {
		return nil, fmt.Errorf("owned probe was modified; preserve and review it before retrying: %s", record.Path)
	}
	return data, nil
}

func removeProbe(root, probe string) error {
	path, err := probeOwnershipPath(root, probe)
	if err != nil {
		return err
	}
	record, err := readProbeOwnership(path)
	if err != nil {
		return err
	}
	if record.Root != canonicalWorkflowRoot(root) || record.Path != probe {
		return fmt.Errorf("probe ownership does not match this repository: %s", path)
	}
	_, err = ownedProbeContents(record)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := os.Remove(probe); err != nil {
			return err
		}
	}
	if record.Peer != "" {
		if filepath.Dir(record.Peer) != filepath.Dir(path) {
			return fmt.Errorf("invalid probe ownership peer")
		}
		if err := os.Remove(record.Peer); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(path)) // Only remove an empty ownership directory.
	return nil
}

// Publish only complete probe contents from private staging storage. Probes
// are immutable; scenario selection uses the runner environment/configuration.
func probeContentsFile(root string, contents []byte) (string, error) {
	directory, err := probeOwnershipDirectory(root)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, "contents-")
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(contents)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func createOwnedProbe(root, probe string, contents []byte) error {
	temporary, err := probeContentsFile(root, contents)
	if err != nil {
		return err
	}
	record := probeOwnership{Root: canonicalWorkflowRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256(contents)), Peer: temporary}
	if err := writeProbeRecord(record); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	// Link is atomic and refuses to replace an existing customer file.
	// Retain the private peer until cleanup: even a crash before Link cannot
	// make a same-content pre-existing customer file count as our published probe.
	if err := os.Link(temporary, probe); err != nil {
		record, _ := probeOwnershipPath(root, probe)
		_ = os.Remove(record)
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func recoverProbes(root string) error {
	if err := recoverLegacyProbes(root); err != nil {
		return err
	}
	directory, err := probeOwnershipDirectory(root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		record, err := readProbeOwnership(path)
		if err != nil {
			return err
		}
		expected, err := probeOwnershipPath(root, record.Path)
		if err != nil || expected != path || record.Root != canonicalWorkflowRoot(root) {
			return fmt.Errorf("invalid probe ownership record: %s", path)
		}
		if err := removeProbe(root, record.Path); err != nil {
			return err
		}
	}
	// Process manifests first; their private peers prove source-file identity.
	// Only then remove unpublished staging files inside this owned directory.
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "record-") || strings.HasPrefix(entry.Name(), "contents-") {
			path := filepath.Join(directory, entry.Name())
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unexpected probe staging file: %s", path)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	_ = os.Remove(directory)
	return nil
}

// Older releases kept ownership in TMPDIR. Recover those records when still
// available, but write new records beside the reports so reboot cleanup is safe.
func recoverLegacyProbes(root string) error {
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("ddtest-probes-%d", os.Getuid()), fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalWorkflowRoot(root)))))
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("legacy probe ownership storage must be private")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		record, err := readProbeOwnership(path)
		if err != nil {
			return err
		}
		if record.Root != canonicalWorkflowRoot(root) || entry.Name() != fmt.Sprintf("%x.json", sha256.Sum256([]byte(record.Path))) {
			return fmt.Errorf("invalid legacy probe ownership record")
		}
		_, err = ownedProbeContents(record)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if err := os.Remove(record.Path); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	_ = os.Remove(directory)
	return nil
}
