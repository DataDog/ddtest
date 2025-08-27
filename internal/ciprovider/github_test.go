package ciprovider

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGitHub_Name(t *testing.T) {
	g := NewGitHub()
	if got := g.Name(); got != "github" {
		t.Errorf("GitHub.Name() = %v, want %v", got, "github")
	}
}

func TestGitHub_Configure(t *testing.T) {
	g := NewGitHub()

	tests := []struct {
		name            string
		parallelRunners int
		wantErr         bool
	}{
		{
			name:            "valid 2 runners",
			parallelRunners: 2,
			wantErr:         false,
		},
		{
			name:            "valid 1 runner",
			parallelRunners: 1,
			wantErr:         false,
		},
		{
			name:            "valid 5 runners",
			parallelRunners: 5,
			wantErr:         false,
		},
		{
			name:            "invalid 0 runners",
			parallelRunners: 0,
			wantErr:         true,
		},
		{
			name:            "invalid negative runners",
			parallelRunners: -1,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up before each test
			_ = os.RemoveAll(".dd")

			err := g.Configure(tt.parallelRunners)
			if (err != nil) != tt.wantErr {
				t.Errorf("GitHub.Configure() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify the file was created
				if _, err := os.Stat(GitHubMatrixPath); os.IsNotExist(err) {
					t.Errorf("Expected matrix file to be created at %s", GitHubMatrixPath)
					return
				}

				// Read and verify the content
				data, err := os.ReadFile(GitHubMatrixPath)
				if err != nil {
					t.Errorf("Failed to read matrix file: %v", err)
					return
				}

				var matrix matrixConfig
				if err := json.Unmarshal(data, &matrix); err != nil {
					t.Errorf("Failed to unmarshal matrix JSON: %v", err)
					return
				}

				if len(matrix.Include) != tt.parallelRunners {
					t.Errorf("Expected %d matrix entries, got %d", tt.parallelRunners, len(matrix.Include))
				}

				for i, entry := range matrix.Include {
					if entry.CINodeIndex != i {
						t.Errorf("Expected ci_node_index %d, got %d", i, entry.CINodeIndex)
					}
					if entry.CINodeTotal != tt.parallelRunners {
						t.Errorf("Expected ci_node_total %d, got %d", tt.parallelRunners, entry.CINodeTotal)
					}
				}
			}

			// Clean up after each test
			_ = os.RemoveAll(".dd")
		})
	}
}

func TestGitHub_ConfigureJSONFormat(t *testing.T) {
	g := NewGitHub()

	// Clean up before test
	_ = os.RemoveAll(".dd")
	defer func() { _ = os.RemoveAll(".dd") }()

	err := g.Configure(2)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Read the generated file
	data, err := os.ReadFile(GitHubMatrixPath)
	if err != nil {
		t.Fatalf("Failed to read matrix file: %v", err)
	}

	expectedJSON := `{"include":[{"ci_node_index":0,"ci_node_total":2},{"ci_node_index":1,"ci_node_total":2}]}`
	actualJSON := string(data)

	if actualJSON != expectedJSON {
		t.Errorf("Expected JSON:\n%s\nGot JSON:\n%s", expectedJSON, actualJSON)
	}
}
