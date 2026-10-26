package testdrive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/maven"
)

const mavenFailureJSON = "maven-previous-failure.json"
const mavenFailureHTML = "maven-previous-failure.html"

// One previous failed comparison, never a chain of reports or a reused verdict.
// The archive embeds the old JSON and checksums its companion HTML.
type mavenFailure struct {
	Kind        string            `json:"kind"`
	Session     string            `json:"session"`
	Framework   string            `json:"framework"`
	CompletedAt time.Time         `json:"completed_at"`
	Status      string            `json:"status"`
	JSONReport  string            `json:"json_report"`
	HTMLReport  string            `json:"html_report,omitempty"`
	HTMLSHA256  string            `json:"html_sha256,omitempty"`
	Result      *validationResult `json:"result,omitempty"`
}

func readMavenArtifact(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("maven evidence must be a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("maven evidence exceeds %d bytes: %s", limit, path)
	}
	return data, nil
}

func preserveMavenFailure(root string, checkOnly bool) (*mavenFailure, error) {
	directory := filepath.Dir(validationPath(root))
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("maven evidence directory must not be a symlink: %s", directory)
	}
	jsonPath, htmlPath := filepath.Join(directory, mavenFailureJSON), filepath.Join(directory, mavenFailureHTML)
	var history *mavenFailure
	data, err := readMavenArtifact(jsonPath, 16<<20)
	if err == nil {
		history = &mavenFailure{}
		if err := json.Unmarshal(data, history); err != nil {
			return nil, err
		}
		if history.Kind != "ddtest-maven-failed-comparison" || history.JSONReport != mavenFailureJSON || (history.HTMLReport != "" && history.HTMLReport != mavenFailureHTML) || history.Result == nil || history.Session != history.Result.Session || history.Framework != history.Result.Framework || history.Result.Compatibility.Status != "suspected regression" || !slices.Contains(maven.FrameworkNames, history.Framework) {
			return nil, fmt.Errorf("unrecognized previous Maven comparison archive: %s", jsonPath)
		}
		if history.HTMLReport != "" {
			html, err := readMavenArtifact(htmlPath, 32<<20)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(html)
			if hex.EncodeToString(sum[:]) != history.HTMLSHA256 {
				return nil, fmt.Errorf("previous Maven HTML does not match its JSON archive")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !checkOnly {
		// Refuse symlinked current reports before reading or overwriting evidence.
		current, err := readMavenArtifact(validationPath(root), 8<<20)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		var previous *validationResult
		if err == nil {
			previous = &validationResult{}
			if err := json.Unmarshal(current, previous); err != nil {
				return nil, fmt.Errorf("decode previous Maven report: %w", err)
			}
		}
		if previous != nil && !hasValidationExecution(*previous) && previous.Retained != nil {
			previous = previous.Retained.Result
		}
		if previous != nil && slices.Contains(maven.FrameworkNames, previous.Framework) && previous.Compatibility.Status == "suspected regression" && hasValidationPair(*previous) {
			if history == nil {
				if _, err := os.Lstat(htmlPath); err == nil {
					return nil, fmt.Errorf("unowned Maven history HTML exists: %s", htmlPath)
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
			}
			previous.PreviousFailure, previous.Retained = nil, nil
			history = &mavenFailure{Kind: "ddtest-maven-failed-comparison", Session: previous.Session, Framework: previous.Framework, CompletedAt: previous.CompletedAt, Status: "historical; not resolved by a later matching run", JSONReport: mavenFailureJSON, Result: previous}
			if previous.HTMLReportCurrent {
				html, err := readMavenArtifact(htmlReportPath(root), 32<<20)
				if err != nil {
					return nil, err
				}
				// Keep the archived HTML's navigation paired with its historical JSON.
				html = bytes.ReplaceAll(html, []byte(`href="testdrive.json"`), []byte(`href="maven-previous-failure.json"`))
				sum := sha256.Sum256(html)
				history.HTMLReport, history.HTMLSHA256 = mavenFailureHTML, hex.EncodeToString(sum[:])
				if err := writeValidation(htmlPath, html); err != nil {
					return nil, err
				}
			}
			data, err := json.MarshalIndent(history, "", "  ")
			if err != nil {
				return nil, err
			}
			if err := writeValidation(jsonPath, append(data, '\n')); err != nil {
				return nil, err
			}
		}
	}
	if history != nil {
		history.Result = nil
	}
	return history, nil
}
