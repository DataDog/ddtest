package planner

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
)

// Preserve the complete selected file set before TIA removes skippable suites,
// so customers can compare discovery independently of backend skip decisions.
func writeDiscoveredTestFilesArtifact(files map[string]struct{}) error {
	names := slices.Sorted(maps.Keys(files))
	content := strings.Join(names, "\n")
	if len(names) > 0 {
		content += "\n"
	}
	return writePlanFile(constants.DiscoveredTestFilesOutputPath, []byte(content))
}

func writeTestFilesArtifact(testFileWeights map[string]int) error {
	testFileNames := make([]string, 0, len(testFileWeights))
	for testFile := range testFileWeights {
		testFileNames = append(testFileNames, testFile)
	}
	slices.Sort(testFileNames)

	content := strings.Join(testFileNames, "\n")
	if len(testFileNames) > 0 {
		content += "\n"
	}

	if err := writePlanFile(constants.TestFilesOutputPath, []byte(content)); err != nil {
		return fmt.Errorf("failed to write test files: %w", err)
	}
	return nil
}

func writePlanFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create output directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}
