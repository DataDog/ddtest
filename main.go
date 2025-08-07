package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/civisibility/integrations"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
	"github.com/spf13/cobra"
)

type Test struct {
	FQN        string `json:"fqn"`
	Name       string `json:"name"`
	Suite      string `json:"suite"`
	SourceFile string `json:"sourceFile"`
}

var rootCmd = &cobra.Command{
	Use:   "ddruntest",
	Short: "A test runner from Datadog",
	Long:  "Command line tool for running tests with Datadog Test Optimization.",
}

var testFilesCmd = &cobra.Command{
	Use:   "test-files",
	Short: "prints test files that are discovered in the project and not skipped completely by Datadog's Test Impact Analysis",
	Run: func(cmd *cobra.Command, args []string) {
		tags := make(map[string]string)
		tags[constants.RuntimeName] = "ruby"
		tags[constants.RuntimeVersion] = "3.3.0"
		tags[constants.OSPlatform] = "darwin23"
		tags[constants.OSVersion] = "24.5.0"
		tags["language"] = "ruby"

		utils.AddCITagsMap(tags)

		startTimeTestOpt := time.Now()
		integrations.EnsureCiVisibilityInitialization()

		librarySettings := *integrations.GetSettings()
		// Set of FQNs for tests that can be skipped
		ddSkippedTests := make(map[string]bool)

		if librarySettings.ItrEnabled && librarySettings.TestsSkipping {
			slog.Debug("Fetching skippable tests...")
			skippableTests := integrations.GetSkippableTests()

			// fill the storage of all tests to be skipped
			for _, suites := range skippableTests {
				for _, tests := range suites {
					for _, test := range tests {
						testFQN := testFQN(test.Suite, test.Name, test.Parameters)
						ddSkippedTests[testFQN] = true
					}
				}
			}
		}
		integrations.ExitCiVisibility()

		slog.Debug("Skipped tests", "count", len(ddSkippedTests))

		durationTestOpt := time.Since(startTimeTestOpt)
		slog.Debug("Finished fetching skippable tests!", "duration", durationTestOpt)

		filePath := "./.dd/tests-discovery/rspec.json"

		// Delete the discovery file if it exists before running
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			slog.Warn("Warning: Failed to delete existing discovery file", "filePath", filePath, "error", err)
		}

		cmdName := "bundle"
		cmdArgs := []string{"exec", "rspec", "--format", "progress", "--dry-run"}

		slog.Debug("Starting RSpec dry run...")
		startTime := time.Now()

		rspecCmd := exec.Command(cmdName, cmdArgs...)
		rspecCmd.Env = append(
			os.Environ(),
			"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1",
			"DD_TEST_OPTIMIZATION_DISCOVERY_FILE="+filePath,
		)
		output, err := rspecCmd.CombinedOutput()

		if err != nil {
			slog.Error("Failed to run RSpec dry run", "output", string(output))
			os.Exit(1)
		}

		duration := time.Since(startTime)
		slog.Debug("Finished RSpec dry run!", "duration", duration)

		// Read and parse the JSON stream file
		file, err := os.Open(filePath)
		if err != nil {
			slog.Error("Error opening JSON file", "error", err)
			os.Exit(1)
		}
		defer file.Close()

		var tests []Test
		decoder := json.NewDecoder(file)
		for decoder.More() {
			var test Test
			if err := decoder.Decode(&test); err != nil {
				slog.Error("Error parsing JSON", "error", err)
				os.Exit(1)
			}
			tests = append(tests, test)
		}

		slog.Debug("Parsed RSpec report", "examples", len(tests))

		testFiles := make(map[string]bool)
		for _, test := range tests {
			if !ddSkippedTests[test.FQN] {
				slog.Debug("Test is not skipped", "test", test.FQN, "sourceFile", test.SourceFile)
				testFiles[test.SourceFile] = true
			}
		}

		for testFile := range testFiles {
			fmt.Print(testFile + " ")
		}
		fmt.Println()
	},
}

func testFQN(suite, test, parameters string) string {
	return fmt.Sprintf("%s.%s.%s", suite, test, parameters)
}

func init() {
	rootCmd.AddCommand(testFilesCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		slog.Error("FAILURE", "error", err)
		os.Exit(1)
	}
}
