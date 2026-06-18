package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
)

var GitHubMatrixPath = filepath.Join(constants.PlanDirectory, "github/config")

const githubOutputEnvVar = "GITHUB_OUTPUT"

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

	if err := writeGitHubStepOutput(configContent); err != nil {
		return err
	}

	return nil
}

func writeGitHubStepOutput(configContent string) error {
	outputPath := os.Getenv(githubOutputEnvVar)
	if outputPath == "" {
		return nil
	}

	outputFile, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open GitHub output file %s: %w", outputPath, err)
	}
	defer func() {
		_ = outputFile.Close()
	}()

	if _, err := fmt.Fprintln(outputFile, configContent); err != nil {
		return fmt.Errorf("failed to write matrix configuration to GitHub output file %s: %w", outputPath, err)
	}

	return nil
}
