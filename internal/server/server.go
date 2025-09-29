package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/DataDog/datadog-test-runner/internal/constants"
	"github.com/spf13/viper"
)

type Server struct {
	port int
}

type CacheData struct {
	Settings            any `json:"settings,omitempty"`
	SkippableTests      any `json:"skippableTests,omitempty"`
	KnownTests          any `json:"knownTests,omitempty"`
	TestManagementTests any `json:"testManagementTests,omitempty"`
}

func New(port int) *Server {
	if port == 0 {
		if envPort := os.Getenv("DD_TEST_OPTIMIZATION_RUNNER_PORT"); envPort != "" {
			if parsedPort, err := strconv.Atoi(envPort); err == nil {
				port = parsedPort
			}
		}
		if port == 0 {
			port = viper.GetInt("port")
		}
		if port == 0 {
			port = 7890
		}
	}
	return &Server{port: port}
}

func (s *Server) loadCacheData() (*CacheData, error) {
	cacheDir := filepath.Join(constants.PlanDirectory, "cache")

	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return &CacheData{}, nil
	}

	cacheData := &CacheData{}

	settingsPath := filepath.Join(cacheDir, "settings.json")
	if settings, err := s.loadJSONFile(settingsPath); err == nil {
		cacheData.Settings = settings
	}

	skippableTestsPath := filepath.Join(cacheDir, "skippable_tests.json")
	if skippableTests, err := s.loadJSONFile(skippableTestsPath); err == nil {
		cacheData.SkippableTests = skippableTests
	}

	knownTestsPath := filepath.Join(cacheDir, "known_tests.json")
	if knownTests, err := s.loadJSONFile(knownTestsPath); err == nil {
		cacheData.KnownTests = knownTests
	}

	testManagementTestsPath := filepath.Join(cacheDir, "test_management_tests.json")
	if testManagementTests, err := s.loadJSONFile(testManagementTestsPath); err == nil {
		cacheData.TestManagementTests = testManagementTests
	}

	return cacheData, nil
}

func (s *Server) loadJSONFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var jsonData any
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, err
	}

	return jsonData, nil
}

func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cacheData, err := s.loadCacheData()
	if err != nil {
		slog.Error("Failed to load cache data", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cacheData); err != nil {
		slog.Error("Failed to encode cache data", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/cache", s.handleCache)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Channel to receive server errors
	serverErr := make(chan error, 1)

	// Start server in a goroutine
	go func() {
		slog.Info("Starting HTTP server", "port", s.port, "endpoint", "/cache")
		serverErr <- server.ListenAndServe()
	}()

	// Wait for either context cancellation or server error
	select {
	case <-ctx.Done():
		slog.Info("Shutting down server...")
		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown failed", "error", err)
			return err
		}
		slog.Info("Server shutdown complete")
		return nil
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}
