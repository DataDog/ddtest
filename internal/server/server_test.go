package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/constants"
)

func TestLoadJSONFile(t *testing.T) {
	server := &Server{port: 8080}

	t.Run("load valid JSON file", func(t *testing.T) {
		// Create temporary file with valid JSON
		tmpDir := t.TempDir()
		jsonFile := filepath.Join(tmpDir, "test.json")
		testData := map[string]any{
			"key1": "value1",
			"key2": 123,
			"key3": true,
		}
		jsonBytes, _ := json.Marshal(testData)
		err := os.WriteFile(jsonFile, jsonBytes, 0644)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		result, err := server.loadJSONFile(jsonFile)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			t.Errorf("expected map[string]any, got %T", result)
		}

		if resultMap["key1"] != "value1" {
			t.Errorf("expected key1=value1, got %v", resultMap["key1"])
		}
	})

	t.Run("load invalid JSON file", func(t *testing.T) {
		tmpDir := t.TempDir()
		jsonFile := filepath.Join(tmpDir, "invalid.json")
		err := os.WriteFile(jsonFile, []byte("{ invalid json }"), 0644)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		_, err = server.loadJSONFile(jsonFile)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("load non-existent file", func(t *testing.T) {
		_, err := server.loadJSONFile("/non/existent/file.json")
		if err == nil {
			t.Error("expected error for non-existent file, got nil")
		}
	})
}

func TestLoadContextData(t *testing.T) {
	server := &Server{port: 8080}

	t.Run("no context directory", func(t *testing.T) {
		// Change to temporary directory to avoid conflicts
		originalDir, _ := os.Getwd()
		tmpDir := t.TempDir()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change to temp directory: %v", err)
		}
		defer func() {
			if err := os.Chdir(originalDir); err != nil {
				t.Errorf("failed to restore original directory: %v", err)
			}
		}()

		contextData, err := server.loadContextData()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if contextData.Settings != nil || contextData.SkippableTests != nil ||
			contextData.KnownTests != nil || contextData.TestManagementTests != nil {
			t.Error("expected empty context data when no directory exists")
		}
	})

	t.Run("with context files", func(t *testing.T) {
		originalDir, _ := os.Getwd()
		tmpDir := t.TempDir()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change to temp directory: %v", err)
		}
		defer func() {
			if err := os.Chdir(originalDir); err != nil {
				t.Errorf("failed to restore original directory: %v", err)
			}
		}()

		contextDir := filepath.Join(constants.PlanDirectory, "context")
		err := os.MkdirAll(contextDir, 0755)
		if err != nil {
			t.Fatalf("failed to create context directory: %v", err)
		}

		// Create test files
		testFiles := map[string]any{
			"settings.json": map[string]any{
				"testOptimization": true,
				"parallelization":  false,
			},
			"skippable_tests.json": map[string]any{
				"correlationId": "test-123",
				"skippableTests": map[string]any{
					"spec": []string{"test1.rb", "test2.rb"},
				},
			},
			"known_tests.json": map[string]any{
				"tests": []string{"known1.rb", "known2.rb"},
			},
			"test_management_tests.json": map[string]any{
				"modules": []string{"module1", "module2"},
			},
		}

		for filename, data := range testFiles {
			filePath := filepath.Join(contextDir, filename)
			jsonBytes, _ := json.Marshal(data)
			err := os.WriteFile(filePath, jsonBytes, 0644)
			if err != nil {
				t.Fatalf("failed to create %s: %v", filename, err)
			}
		}

		contextData, err := server.loadContextData()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if contextData.Settings == nil {
			t.Error("expected settings to be loaded")
		}
		if contextData.SkippableTests == nil {
			t.Error("expected skippable tests to be loaded")
		}
		if contextData.KnownTests == nil {
			t.Error("expected known tests to be loaded")
		}
		if contextData.TestManagementTests == nil {
			t.Error("expected test management tests to be loaded")
		}

		// Verify settings content
		settingsMap, ok := contextData.Settings.(map[string]any)
		if !ok {
			t.Error("expected settings to be a map")
		} else {
			if settingsMap["testOptimization"] != true {
				t.Error("expected testOptimization to be true")
			}
		}
	})

	t.Run("partial files - only some exist", func(t *testing.T) {
		originalDir, _ := os.Getwd()
		tmpDir := t.TempDir()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change to temp directory: %v", err)
		}
		defer func() {
			if err := os.Chdir(originalDir); err != nil {
				t.Errorf("failed to restore original directory: %v", err)
			}
		}()

		contextDir := filepath.Join(constants.PlanDirectory, "context")
		err := os.MkdirAll(contextDir, 0755)
		if err != nil {
			t.Fatalf("failed to create context directory: %v", err)
		}

		// Create only settings.json
		settingsData := map[string]any{"testOptimization": true}
		jsonBytes, _ := json.Marshal(settingsData)
		settingsPath := filepath.Join(contextDir, "settings.json")
		err = os.WriteFile(settingsPath, jsonBytes, 0644)
		if err != nil {
			t.Fatalf("failed to create settings.json: %v", err)
		}

		contextData, err := server.loadContextData()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if contextData.Settings == nil {
			t.Error("expected settings to be loaded")
		}
		if contextData.SkippableTests != nil {
			t.Error("expected skippable tests to be nil")
		}
		if contextData.KnownTests != nil {
			t.Error("expected known tests to be nil")
		}
		if contextData.TestManagementTests != nil {
			t.Error("expected test management tests to be nil")
		}
	})
}

func TestHandleContext(t *testing.T) {
	server := &Server{port: 8080}

	t.Run("GET request success", func(t *testing.T) {
		originalDir, _ := os.Getwd()
		tmpDir := t.TempDir()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change to temp directory: %v", err)
		}
		defer func() {
			if err := os.Chdir(originalDir); err != nil {
				t.Errorf("failed to restore original directory: %v", err)
			}
		}()

		contextDir := filepath.Join(constants.PlanDirectory, "context")
		err := os.MkdirAll(contextDir, 0755)
		if err != nil {
			t.Fatalf("failed to create context directory: %v", err)
		}

		settingsData := map[string]any{"testOptimization": true}
		jsonBytes, _ := json.Marshal(settingsData)
		settingsPath := filepath.Join(contextDir, "settings.json")
		err = os.WriteFile(settingsPath, jsonBytes, 0644)
		if err != nil {
			t.Fatalf("failed to create settings.json: %v", err)
		}

		req := httptest.NewRequest("GET", "/context", nil)
		w := httptest.NewRecorder()

		server.handleContext(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		contentType := w.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", contentType)
		}

		var response ContextData
		err = json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Errorf("failed to unmarshal response: %v", err)
		}

		if response.Settings == nil {
			t.Error("expected settings in response")
		}
	})

	t.Run("POST request not allowed", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/context", nil)
		w := httptest.NewRecorder()

		server.handleContext(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("PUT request not allowed", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/context", nil)
		w := httptest.NewRecorder()

		server.handleContext(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("DELETE request not allowed", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/context", nil)
		w := httptest.NewRecorder()

		server.handleContext(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("empty context directory returns empty object", func(t *testing.T) {
		originalDir, _ := os.Getwd()
		tmpDir := t.TempDir()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change to temp directory: %v", err)
		}
		defer func() {
			if err := os.Chdir(originalDir); err != nil {
				t.Errorf("failed to restore original directory: %v", err)
			}
		}()

		req := httptest.NewRequest("GET", "/context", nil)
		w := httptest.NewRecorder()

		server.handleContext(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var response ContextData
		err := json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Errorf("failed to unmarshal response: %v", err)
		}

		if response.Settings != nil || response.SkippableTests != nil ||
			response.KnownTests != nil || response.TestManagementTests != nil {
			t.Error("expected empty context data response")
		}
	})
}

func TestContextDataJSONSerialization(t *testing.T) {
	contextData := ContextData{
		Settings: map[string]any{
			"testOptimization": true,
			"parallelization":  false,
		},
		SkippableTests: map[string]any{
			"correlationId": "test-123",
		},
		KnownTests: []string{"test1.rb", "test2.rb"},
		TestManagementTests: map[string]any{
			"modules": []string{"module1"},
		},
	}

	jsonBytes, err := json.Marshal(contextData)
	if err != nil {
		t.Errorf("failed to marshal ContextData: %v", err)
	}

	var unmarshaled ContextData
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	if err != nil {
		t.Errorf("failed to unmarshal ContextData: %v", err)
	}

	if unmarshaled.Settings == nil {
		t.Error("expected settings to be preserved")
	}
	if unmarshaled.SkippableTests == nil {
		t.Error("expected skippable tests to be preserved")
	}
	if unmarshaled.KnownTests == nil {
		t.Error("expected known tests to be preserved")
	}
	if unmarshaled.TestManagementTests == nil {
		t.Error("expected test management tests to be preserved")
	}
}

func TestContextDataOmitEmpty(t *testing.T) {
	// Test that empty fields are omitted in JSON serialization
	contextData := ContextData{
		Settings: map[string]any{
			"testOptimization": true,
		},
		// Other fields are nil, should be omitted
	}

	jsonBytes, err := json.Marshal(contextData)
	if err != nil {
		t.Errorf("failed to marshal ContextData: %v", err)
	}

	jsonStr := string(jsonBytes)

	if !strings.Contains(jsonStr, "settings") {
		t.Error("expected settings to be in JSON")
	}
	if strings.Contains(jsonStr, "skippableTests") {
		t.Error("expected skippableTests to be omitted from JSON")
	}
	if strings.Contains(jsonStr, "knownTests") {
		t.Error("expected knownTests to be omitted from JSON")
	}
	if strings.Contains(jsonStr, "testManagementTests") {
		t.Error("expected testManagementTests to be omitted from JSON")
	}
}
