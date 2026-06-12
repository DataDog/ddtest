package planner

import (
	"bufio"
	"bytes"
	"context"
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
	discoveryCacheSchemaVersion          = 1
	discoveryCacheDebugGitOutputMaxBytes = 4096
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

var discoveryCacheGitOutput = func(args ...string) ([]byte, error) {
	return exec.Command("git", args...).Output()
}

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
		slog.Info("Failed to import test discovery cache; full discovery may be required",
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
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	sourceInfo, err := source.Stat()
	if err != nil {
		return err
	}
	if destinationInfo, err := os.Stat(destinationPath); err == nil {
		if os.SameFile(sourceInfo, destinationInfo) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}

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
		return discoveryCacheMetadata{}, errors.New("last discovery cache line does not contain _ddtest_discovery_cache_metadata")
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
		if settings.GetTestDiscoveryCache() != "" {
			slog.Info("Cached test discovery not usable; full discovery will run", "reason", err)
		} else {
			slog.Debug("Cached test discovery not usable; full discovery will run", "reason", err)
		}
		return nil, false
	}

	startTime := time.Now()
	tests, err := parseCachedDiscoveryTests(c.filePath)
	if err != nil {
		slog.Info("Cached test discovery could not be used; full discovery will run", "error", err)
		return nil, false
	}
	if err := ensureDiscoveredTests(tests); err != nil {
		slog.Info("Cached test discovery could not be used; full discovery will run", "error", err)
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
	output, err := discoveryCacheGitOutputDebug("rev-parse", "HEAD")
	if err != nil {
		slog.Warn("Failed to append test discovery cache metadata", "error", err)
		return
	}

	metadata := discoveryCacheMetadata{
		SchemaVersion:       discoveryCacheSchemaVersion,
		SourceCommit:        strings.TrimSpace(string(output)),
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
	testPattern := c.testFramework.TestPattern()
	for _, check := range []struct {
		name string
		got  string
		want string
	}{
		{"platform", metadata.Platform, c.platformName},
		{"framework", metadata.Framework, c.testFramework.Name()},
		{"tests location", metadata.TestsLocation, testPattern},
		{"tests exclude pattern", metadata.TestsExcludePattern, settings.GetTestsExcludePattern()},
	} {
		if check.got != check.want {
			return fmt.Errorf("%s mismatch: %s", check.name, check.got)
		}
	}
	if metadata.SourceCommit == "" {
		return errors.New("source commit missing")
	}
	if _, err := discoveryCacheGitOutputDebug("cat-file", "-e", metadata.SourceCommit+"^{commit}"); err != nil {
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

func discoveryCacheChangedFilesSince(commit string) ([]string, error) {
	diffOutput, err := discoveryCacheGitOutputDebug("diff", "--name-status", "-M", "-z", commit, "HEAD")
	if err != nil {
		return nil, err
	}
	statusOutput, err := discoveryCacheGitOutputDebug("status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}

	return append(discoveryCacheParseGitDiffNameStatus(diffOutput), discoveryCacheParseGitStatusPorcelain(statusOutput)...), nil
}

func discoveryCacheGitOutputDebug(args ...string) ([]byte, error) {
	output, err := discoveryCacheGitOutput(args...)
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return output, err
	}

	attrs := []any{
		"args", args,
		"outputBytes", len(output),
		"outputTruncated", len(output) > discoveryCacheDebugGitOutputMaxBytes,
		"output", discoveryCacheDebugGitOutput(output),
	}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	slog.Debug("Test discovery cache git command result", attrs...)
	return output, err
}

func discoveryCacheDebugGitOutput(output []byte) string {
	if len(output) <= discoveryCacheDebugGitOutputMaxBytes {
		return string(output)
	}
	return string(output[:discoveryCacheDebugGitOutputMaxBytes])
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
		if discoveryPathMatches(pattern, normalized) {
			return changedFile, true
		}
		stripped := normalizeDiscoveryPath(utils.StripCwdSubdirPrefix(normalized))
		if stripped != normalized && discoveryPathMatches(pattern, stripped) {
			return changedFile, true
		}
	}
	return "", false
}

func discoveryPathMatches(pattern, path string) bool {
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
			files = append(files, fields[i])
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
