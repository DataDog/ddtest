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
}

// Recovery is called only after the repository workflow lock is acquired.
// No PID or filename prefix is evidence that an abandoned file belongs to us.
func probeOwnershipDirectory(root string) (string, error) {
	root = canonicalWorkflowRoot(root)
	path := filepath.Join(os.TempDir(), fmt.Sprintf("ddtest-probes-%d", os.Getuid()), fmt.Sprintf("%x", sha256.Sum256([]byte(root))))
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	for _, directory := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(directory)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("probe ownership storage must be private: %s", directory)
		}
	}
	return path, nil
}

func probeOwnershipPath(root, probe string) (string, error) {
	directory, err := probeOwnershipDirectory(root)
	return filepath.Join(directory, fmt.Sprintf("%x.json", sha256.Sum256([]byte(probe)))), err
}

func recordProbe(root, probe string, contents []byte) error {
	path, err := probeOwnershipPath(root, probe)
	if err != nil {
		return err
	}
	record := probeOwnership{Root: canonicalWorkflowRoot(root), Path: probe, Hash: fmt.Sprintf("%x", sha256.Sum256(contents))}
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
	data, err := os.ReadFile(record.Path)
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != record.Hash {
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
	return os.Remove(path)
}

func writeOwnedProbe(root, probe string, contents []byte) error {
	path, err := probeOwnershipPath(root, probe)
	if err != nil {
		return err
	}
	record, err := readProbeOwnership(path)
	if err != nil {
		return err
	}
	if _, err := ownedProbeContents(record); err != nil {
		return err
	}
	if err := recordProbe(root, probe, contents); err != nil {
		return err
	}
	return os.WriteFile(probe, contents, 0600)
}

func recoverProbes(root string) error {
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
			continue // An interrupted atomic manifest write owns no source file.
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
	return nil
}
