package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
)

func runnerSplitPath(ciNode int) string {
	return filepath.Join(constants.TestsSplitDir, fmt.Sprintf("runner-%d", ciNode))
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

func writePlanFileCopies(data []byte, paths ...string) error {
	for _, path := range paths {
		if err := writePlanFile(path, data); err != nil {
			return err
		}
	}
	return nil
}
