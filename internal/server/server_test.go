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

func TestLoadCacheData(t *testing.T) {
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

		cacheData, err := server.loadCacheData()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if cacheData.Settings != nil || cacheData.SkippableTests != nil ||
			cacheData.KnownTests != nil || cacheData.TestManagementTests != nil {
			t.Error("expected empty cache data when no directory exists")
		}
	})

	t.Run("with cache files", func(t *testing.T) {
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

		cacheDir := filepath.Join(constants.PlanDirectory, "cache")
		err := os.MkdirAll(cacheDir, 0755)
		if err != nil {
			t.Fatalf("failed to create cache directory: %v", err)
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
			filePath := filepath.Join(cacheDir, filename)
			jsonBytes, _ := json.Marshal(data)
			err := os.WriteFile(filePath, jsonBytes, 0644)
			if err != nil {
				t.Fatalf("failed to create %s: %v", filename, err)
			}
		}

		cacheData, err := server.loadCacheData()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if cacheData.Settings == nil {
			t.Error("expected settings to be loaded")
		}
		if cacheData.SkippableTests == nil {
			t.Error("expected skippable tests to be loaded")
		}
		if cacheData.KnownTests == nil {
			t.Error("expected known tests to be loaded")
		}
		if cacheData.TestManagementTests == nil {
			t.Error("expected test management tests to be loaded")
		}

		// Verify settings content
		settingsMap, ok := cacheData.Settings.(map[string]any)
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

		cacheDir := filepath.Join(constants.PlanDirectory, "cache")
		err := os.MkdirAll(cacheDir, 0755)
		if err != nil {
			t.Fatalf("failed to create cache directory: %v", err)
		}

		// Create only settings.json
		settingsData := map[string]any{"testOptimization": true}
		jsonBytes, _ := json.Marshal(settingsData)
		settingsPath := filepath.Join(cacheDir, "settings.json")
		err = os.WriteFile(settingsPath, jsonBytes, 0644)
		if err != nil {
			t.Fatalf("failed to create settings.json: %v", err)
		}

		cacheData, err := server.loadCacheData()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if cacheData.Settings == nil {
			t.Error("expected settings to be loaded")
		}
		if cacheData.SkippableTests != nil {
			t.Error("expected skippable tests to be nil")
		}
		if cacheData.KnownTests != nil {
			t.Error("expected known tests to be nil")
		}
		if cacheData.TestManagementTests != nil {
			t.Error("expected test management tests to be nil")
		}
	})
}

func TestHandleCache(t *testing.T) {
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

		cacheDir := filepath.Join(constants.PlanDirectory, "cache")
		err := os.MkdirAll(cacheDir, 0755)
		if err != nil {
			t.Fatalf("failed to create cache directory: %v", err)
		}

		settingsData := map[string]any{"testOptimization": true}
		jsonBytes, _ := json.Marshal(settingsData)
		settingsPath := filepath.Join(cacheDir, "settings.json")
		err = os.WriteFile(settingsPath, jsonBytes, 0644)
		if err != nil {
			t.Fatalf("failed to create settings.json: %v", err)
		}

		req := httptest.NewRequest("GET", "/cache", nil)
		w := httptest.NewRecorder()

		server.handleCache(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		contentType := w.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", contentType)
		}

		var response CacheData
		err = json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Errorf("failed to unmarshal response: %v", err)
		}

		if response.Settings == nil {
			t.Error("expected settings in response")
		}
	})

	t.Run("POST request not allowed", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cache", nil)
		w := httptest.NewRecorder()

		server.handleCache(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("PUT request not allowed", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/cache", nil)
		w := httptest.NewRecorder()

		server.handleCache(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("DELETE request not allowed", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/cache", nil)
		w := httptest.NewRecorder()

		server.handleCache(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("empty cache directory returns empty object", func(t *testing.T) {
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

		req := httptest.NewRequest("GET", "/cache", nil)
		w := httptest.NewRecorder()

		server.handleCache(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var response CacheData
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

func TestCacheDataJSONSerialization(t *testing.T) {
	cacheData := CacheData{
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

	jsonBytes, err := json.Marshal(cacheData)
	if err != nil {
		t.Errorf("failed to marshal CacheData: %v", err)
	}

	var unmarshaled CacheData
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	if err != nil {
		t.Errorf("failed to unmarshal CacheData: %v", err)
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

func TestCacheDataOmitEmpty(t *testing.T) {
	// Test that empty fields are omitted in JSON serialization
	cacheData := CacheData{
		Settings: map[string]any{
			"testOptimization": true,
		},
		// Other fields are nil, should be omitted
	}

	jsonBytes, err := json.Marshal(cacheData)
	if err != nil {
		t.Errorf("failed to marshal CacheData: %v", err)
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
