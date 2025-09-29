package ciprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DataDog/datadog-test-runner/internal/constants"
)

var GitHubMatrixPath = filepath.Join(constants.PlanDirectory, "github/config")

type GitHub struct{}

type matrixEntry struct {
	CINodeIndex int `json:"ci_node_index"`
	CINodeTotal int `json:"ci_node_total"`
}

type matrixConfig struct {
	Include []matrixEntry `json:"include"`
}

func NewGitHub() *GitHub {
	return &GitHub{}
}

func (g *GitHub) Name() string {
	return "github"
}

func (g *GitHub) Configure(parallelRunners int) error {
	if parallelRunners <= 0 {
		return fmt.Errorf("parallelRunners must be greater than 0, got %d", parallelRunners)
	}

	// Create matrix configuration
	matrix := matrixConfig{
		Include: make([]matrixEntry, parallelRunners),
	}

	for i := range parallelRunners {
		matrix.Include[i] = matrixEntry{
			CINodeIndex: i,
			CINodeTotal: parallelRunners,
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(matrix)
	if err != nil {
		return fmt.Errorf("failed to marshal matrix configuration: %w", err)
	}

	// Create directory structure
	dir := filepath.Dir(GitHubMatrixPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write matrix configuration to file
	configContent := fmt.Sprintf("matrix=%s", jsonData)
	if err := os.WriteFile(GitHubMatrixPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write matrix configuration to %s: %w", GitHubMatrixPath, err)
	}

	return nil
}
