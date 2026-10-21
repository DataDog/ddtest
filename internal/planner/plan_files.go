package planner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
)

func writePlanMetadata(ciTags map[string]string) error {
	data, err := json.Marshal(struct {
		CITags map[string]string `json:"ciTags"`
	}{CITags: ciTags})
	if err != nil {
		return fmt.Errorf("failed to encode plan metadata: %w", err)
	}
	return writePlanFile(constants.PlanMetadataPath, data)
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
