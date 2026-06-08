package planner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	discoveryCacheSchemaVersion = 1
	discoveryCacheMetadataKey   = "_ddtest_discovery_cache_metadata"
)

type discoveryCacheMetadata struct {
	SchemaVersion       int    `json:"schemaVersion"`
	SourceCommit        string `json:"sourceCommit"`
	Platform            string `json:"platform"`
	Framework           string `json:"framework"`
	TestsLocation       string `json:"testsLocation"`
	TestsExcludePattern string `json:"testsExcludePattern"`
}

type discoveryCacheFileRecord struct {
	Metadata *discoveryCacheMetadata `json:"_ddtest_discovery_cache_metadata,omitempty"`
	testoptimization.Test
}

type discoveryCacheMetadataRecord struct {
	Metadata discoveryCacheMetadata `json:"_ddtest_discovery_cache_metadata"`
}

type discoveryCache struct {
	platformName  string
	testFramework framework.Framework
	filePath      string
}

type discoveryCacheGitRunner interface {
	output(args ...string) ([]byte, error)
}

type defaultDiscoveryCacheGitRunner struct{}

func (defaultDiscoveryCacheGitRunner) output(args ...string) ([]byte, error) {
	return exec.Command("git", args...).Output()
}

var discoveryCacheGit discoveryCacheGitRunner = defaultDiscoveryCacheGitRunner{}

func newDiscoveryCache(platformName string, testFramework framework.Framework) discoveryCache {
	return discoveryCache{
		platformName:  platformName,
		testFramework: testFramework,
		filePath:      discovery.TestsFilePath,
	}
}

func (c discoveryCache) importExternal() {
	sourcePath := settings.GetTestDiscoveryCache()
	if sourcePath == "" {
		return
	}

	if err := copyFile(sourcePath, c.filePath); err != nil {
		slog.Warn("Failed to import test discovery cache; full discovery may be required",
			"sourcePath", sourcePath,
			"destinationPath", c.filePath,
			"error", err)
		return
	}

	slog.Info("Imported test discovery cache",
		"sourcePath", sourcePath,
		"destinationPath", c.filePath)
}

func copyFile(sourcePath, destinationPath string) error {
	same, err := sameFile(sourcePath, destinationPath)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0755); err != nil {
		return err
	}
	destination, err := os.Create(destinationPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = destination.Close()
	}()

	_, err = io.Copy(destination, source)
	return err
}

func sameFile(sourcePath, destinationPath string) (bool, error) {
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return false, err
	}
	destinationAbs, err := filepath.Abs(destinationPath)
	if err != nil {
		return false, err
	}
	if sourceAbs == destinationAbs {
		return true, nil
	}

	sourceInfo, err := os.Stat(sourceAbs)
	if err != nil {
		return false, err
	}
	destinationInfo, err := os.Stat(destinationAbs)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(sourceInfo, destinationInfo), nil
}

func parseCachedDiscoveryTests(filePath string) ([]testoptimization.Test, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	decoder := json.NewDecoder(file)
	var tests []testoptimization.Test
	for {
		var record discoveryCacheFileRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				return tests, nil
			}
			return nil, err
		}
		if record.Metadata != nil {
			continue
		}
		tests = append(tests, record.Test)
	}
}

func appendDiscoveryCacheMetadata(filePath string, metadata discoveryCacheMetadata) error {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	payload, err := json.Marshal(discoveryCacheMetadataRecord{Metadata: metadata})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "\n%s\n", payload)
	return err
}

func readDiscoveryCacheMetadata(filePath string) (discoveryCacheMetadata, error) {
	line, err := readLastNonEmptyLine(filePath)
	if err != nil {
		return discoveryCacheMetadata{}, err
	}

	var record discoveryCacheFileRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return discoveryCacheMetadata{}, err
	}
	if record.Metadata == nil {
		return discoveryCacheMetadata{}, fmt.Errorf("last discovery cache line does not contain %s", discoveryCacheMetadataKey)
	}
	return *record.Metadata, nil
}

func readLastNonEmptyLine(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	reader := bufio.NewReader(file)
	var last []byte
	for {
		line, err := reader.ReadBytes('\n')
		if candidate := bytes.TrimSpace(line); len(candidate) > 0 {
			last = append(last[:0], candidate...)
		}
		if err == nil {
			continue
		}
		if err != io.EOF {
			return nil, err
		}
		if len(last) > 0 {
			return last, nil
		}
		return nil, io.EOF
	}
}

func (c discoveryCache) restore() ([]testoptimization.Test, bool) {
	c.importExternal()

	if err := c.validate(); err != nil {
		slog.Info("Cached test discovery not usable; full discovery will run", "reason", err)
		return nil, false
	}

	startTime := time.Now()
	slog.Info("Using cached test discovery results")
	tests, err := parseCachedDiscoveryTests(c.filePath)
	if err != nil {
		slog.Warn("Cached test discovery could not be used; full discovery will run", "error", err)
		return nil, false
	}
	if err := ensureDiscoveredTests(tests); err != nil {
		slog.Warn("Cached test discovery could not be used; full discovery will run", "error", err)
		return nil, false
	}

	slog.Info("Cached test discovery succeeded", "duration", time.Since(startTime), "count", len(tests))
	return tests, true
}

func ensureDiscoveredTests(tests []testoptimization.Test) error {
	if len(tests) == 0 {
		return fmt.Errorf("test discovery returned no tests")
	}
	return nil
}

func (c discoveryCache) store() {
	sourceCommit, err := discoveryCacheCurrentHEAD()
	if err != nil {
		slog.Warn("Failed to append test discovery cache metadata", "error", err)
		return
	}

	metadata := discoveryCacheMetadata{
		SchemaVersion:       discoveryCacheSchemaVersion,
		SourceCommit:        sourceCommit,
		Platform:            c.platformName,
		Framework:           c.testFramework.Name(),
		TestsLocation:       c.testFramework.TestPattern(),
		TestsExcludePattern: settings.GetTestsExcludePattern(),
	}
	if err := appendDiscoveryCacheMetadata(c.filePath, metadata); err != nil {
		slog.Warn("Failed to append test discovery cache metadata", "error", err)
	}
}

func (c discoveryCache) validate() error {
	metadata, err := readDiscoveryCacheMetadata(c.filePath)
	if err != nil {
		return fmt.Errorf("metadata unavailable: %w", err)
	}
	if metadata.SchemaVersion != discoveryCacheSchemaVersion {
		return fmt.Errorf("schema version mismatch: %d", metadata.SchemaVersion)
	}
	if metadata.Platform != c.platformName {
		return fmt.Errorf("platform mismatch: %s", metadata.Platform)
	}
	if metadata.Framework != c.testFramework.Name() {
		return fmt.Errorf("framework mismatch: %s", metadata.Framework)
	}
	testPattern := c.testFramework.TestPattern()
	if metadata.TestsLocation != testPattern {
		return fmt.Errorf("tests location mismatch: %s", metadata.TestsLocation)
	}
	if metadata.TestsExcludePattern != settings.GetTestsExcludePattern() {
		return fmt.Errorf("tests exclude pattern mismatch: %s", metadata.TestsExcludePattern)
	}
	if metadata.SourceCommit == "" {
		return errors.New("source commit missing")
	}
	if err := discoveryCacheCommitExists(metadata.SourceCommit); err != nil {
		return fmt.Errorf("source commit unavailable: %w", err)
	}

	changedFiles, err := discoveryCacheChangedFilesSince(metadata.SourceCommit)
	if err != nil {
		return fmt.Errorf("changed files unavailable: %w", err)
	}
	if changedFile, ok := firstChangedDiscoveryFile(changedFiles, discoveryCacheRootPattern(testPattern)); ok {
		return fmt.Errorf("test discovery file changed: %s", changedFile)
	}

	return nil
}

func discoveryCacheCurrentHEAD() (string, error) {
	output, err := discoveryCacheGit.output("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func discoveryCacheCommitExists(commit string) error {
	_, err := discoveryCacheGit.output("cat-file", "-e", commit+"^{commit}")
	return err
}

func discoveryCacheChangedFilesSince(commit string) ([]string, error) {
	diffOutput, err := discoveryCacheGit.output("diff", "--name-status", "-M", "-z", commit, "HEAD")
	if err != nil {
		return nil, err
	}
	statusOutput, err := discoveryCacheGit.output("status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}

	files := discoveryCacheParseGitDiffNameStatus(diffOutput)
	files = append(files, discoveryCacheParseGitStatusPorcelain(statusOutput)...)
	return files, nil
}

func discoveryCacheRootPattern(testPattern string) string {
	normalized := normalizeDiscoveryPath(testPattern)
	root, _, ok := strings.Cut(normalized, "/")
	if !ok {
		return normalized
	}
	return root + "/**"
}

func firstChangedDiscoveryFile(changedFiles []string, pattern string) (string, bool) {
	pattern = normalizeDiscoveryPath(pattern)
	for _, changedFile := range changedFiles {
		normalized := normalizeDiscoveryPath(changedFile)
		if discoveryPathMatches(normalized, pattern) {
			return changedFile, true
		}
		stripped := normalizeDiscoveryPath(utils.StripCwdSubdirPrefix(normalized))
		if stripped != normalized && discoveryPathMatches(stripped, pattern) {
			return changedFile, true
		}
	}
	return "", false
}

func discoveryPathMatches(path, pattern string) bool {
	matched, err := doublestar.Match(pattern, path)
	if err != nil {
		slog.Warn("Invalid test discovery cache invalidation pattern", "pattern", pattern, "error", err)
		return true
	}
	return matched
}

func normalizeDiscoveryPath(path string) string {
	normalized := filepath.ToSlash(path)
	normalized = strings.TrimPrefix(normalized, "./")
	return normalized
}

func discoveryCacheParseGitDiffNameStatus(output []byte) []string {
	fields := discoveryCacheSplitNUL(output)
	files := make([]string, 0, len(fields))
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" {
			continue
		}
		if i >= len(fields) {
			break
		}
		files = append(files, fields[i])
		i++
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i >= len(fields) {
				break
			}
			files = append(files, fields[i])
			i++
		}
	}
	return files
}

func discoveryCacheParseGitStatusPorcelain(output []byte) []string {
	fields := discoveryCacheSplitNUL(output)
	files := make([]string, 0, len(fields))
	for i := 0; i < len(fields); {
		entry := fields[i]
		i++
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		path := strings.TrimSpace(entry[3:])
		if path != "" {
			files = append(files, path)
		}
		if strings.Contains(status, "R") || strings.Contains(status, "C") {
			if i >= len(fields) {
				break
			}
			if fields[i] != "" {
				files = append(files, fields[i])
			}
			i++
		}
	}
	return files
}

func discoveryCacheSplitNUL(output []byte) []string {
	return strings.FieldsFunc(string(output), func(r rune) bool {
		return r == 0
	})
}
