package testoptimization

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
	"github.com/DataDog/datadog-test-runner/civisibility/utils/net"
)

// SkippableTestsContext represents the structure for storing skippable tests with correlation ID
type SkippableTestsContext struct {
	CorrelationID  string                                                      `json:"correlationId"`
	SkippableTests map[string]map[string][]net.SkippableResponseDataAttributes `json:"skippableTests"`
}

// ContextManager handles creation and storage of context data for test runners
type ContextManager struct{}

// NewContextManager creates a new ContextManager instance
func NewContextManager() *ContextManager {
	return &ContextManager{}
}

// CreateContextDirectory creates the .dd/context directory for storing context data
func (cm *ContextManager) CreateContextDirectory() error {
	contextDir := filepath.Join(".dd", "context")
	return os.MkdirAll(contextDir, 0755)
}

// writeJSONToFile writes data as JSON to the specified file path
func (cm *ContextManager) writeJSONToFile(data any, filePath string) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// StoreRepositorySettings stores repository settings in .dd/context/settings.json
func (cm *ContextManager) StoreRepositorySettings(repositorySettings *net.SettingsResponseData) error {
	if err := cm.CreateContextDirectory(); err != nil {
		return fmt.Errorf("failed to create context directory: %w", err)
	}

	settingsPath := filepath.Join(".dd", "context", "settings.json")
	if err := cm.writeJSONToFile(repositorySettings, settingsPath); err != nil {
		slog.Error("Failed to write repository settings to file", "error", err, "path", settingsPath)
		return err
	}

	slog.Debug("Repository settings written to file", "path", settingsPath)
	return nil
}

// StoreSkippableTestsContext stores skippable tests with correlation ID in .dd/context/skippable_tests.json
func (cm *ContextManager) StoreSkippableTestsContext(skippableTests map[string]map[string][]net.SkippableResponseDataAttributes) error {
	if err := cm.CreateContextDirectory(); err != nil {
		return fmt.Errorf("failed to create context directory: %w", err)
	}

	// Extract correlation ID from CI tags
	ciTags := utils.GetCITags()
	correlationID := ciTags[constants.ItrCorrelationIDTag]

	// Create skippable tests context
	skippableTestsContext := SkippableTestsContext{
		CorrelationID:  correlationID,
		SkippableTests: skippableTests,
	}

	skippableTestsPath := filepath.Join(".dd", "context", "skippable_tests.json")
	if err := cm.writeJSONToFile(skippableTestsContext, skippableTestsPath); err != nil {
		slog.Error("Failed to write skippable tests to file", "error", err, "path", skippableTestsPath)
		return err
	}

	slog.Debug("Skippable tests written to file", "path", skippableTestsPath, "correlationId", correlationID)
	return nil
}
