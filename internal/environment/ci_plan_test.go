package environment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestCircleCIContinuationParameters(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := newCIProvider("circleci")
	// Replanning must overwrite a previous gate in either direction.
	for _, hasTests := range []bool{true, false, true} {
		if err := provider.Configure(CIPlan{ParallelRunners: 3, HasTests: hasTests}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(CircleCIParametersPath)
		if err != nil {
			t.Fatal(err)
		}
		var parameters map[string]any
		if err := json.Unmarshal(data, &parameters); err != nil {
			t.Fatal(err)
		}
		if len(parameters) != 2 || parameters["parallelism"] != float64(3) || parameters["has_tests"] != hasTests {
			t.Fatalf("unexpected continuation parameters: %s", data)
		}
	}
	for _, count := range []int{0, -1} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			if err := provider.Configure(CIPlan{ParallelRunners: count}); err == nil {
				t.Fatal("expected invalid parallelism to fail")
			}
		})
	}
}

func TestCIOutputWriteFailures(t *testing.T) {
	for _, name := range []string{"github", "circleci"} {
		for _, blocked := range []string{"directory", "file"} {
			t.Run(name+"/"+blocked, func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Setenv(githubOutputEnvVar, "")
				if blocked == "directory" {
					if err := os.WriteFile(constants.PlanDirectory, nil, 0644); err != nil {
						t.Fatal(err)
					}
				} else {
					path := GitHubMatrixPath
					if name == "circleci" {
						path = CircleCIParametersPath
					}
					if err := os.MkdirAll(path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := newCIProvider(name).Configure(CIPlan{ParallelRunners: 1, HasTests: true}); err == nil {
					t.Fatal("expected CI artifact write to fail")
				}
			})
		}
	}
	t.Run("github step output", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv(githubOutputEnvVar, filepath.Join("missing-parent", "output"))
		if err := NewGitHub().Configure(CIPlan{ParallelRunners: 1, HasTests: true}); err == nil {
			t.Fatal("expected GitHub step output write to fail")
		}
	})
}
