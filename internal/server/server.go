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

	"github.com/spf13/viper"
)

type Server struct {
	port int
}

type ContextData struct {
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

func (s *Server) loadContextData() (*ContextData, error) {
	contextDir := filepath.Join(".dd", "context")

	if _, err := os.Stat(contextDir); os.IsNotExist(err) {
		return &ContextData{}, nil
	}

	contextData := &ContextData{}

	settingsPath := filepath.Join(contextDir, "settings.json")
	if settings, err := s.loadJSONFile(settingsPath); err == nil {
		contextData.Settings = settings
	}

	skippableTestsPath := filepath.Join(contextDir, "skippable_tests.json")
	if skippableTests, err := s.loadJSONFile(skippableTestsPath); err == nil {
		contextData.SkippableTests = skippableTests
	}

	knownTestsPath := filepath.Join(contextDir, "known_tests.json")
	if knownTests, err := s.loadJSONFile(knownTestsPath); err == nil {
		contextData.KnownTests = knownTests
	}

	testManagementTestsPath := filepath.Join(contextDir, "test_management_tests.json")
	if testManagementTests, err := s.loadJSONFile(testManagementTestsPath); err == nil {
		contextData.TestManagementTests = testManagementTests
	}

	return contextData, nil
}

func (s *Server) loadJSONFile(path string) (interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, err
	}

	return jsonData, nil
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	contextData, err := s.loadContextData()
	if err != nil {
		slog.Error("Failed to load context data", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(contextData); err != nil {
		slog.Error("Failed to encode context data", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/context", s.handleContext)

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
		slog.Info("Starting HTTP server", "port", s.port, "endpoint", "/context")
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
