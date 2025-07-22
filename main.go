package main

import (
	"encoding/json"
	"fmt"
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
		tags[constants.RuntimeVersion] = "3.3.3"
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
			fmt.Println("Fetching skippable tests...")
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

		fmt.Printf("Skipped tests: %d\n", len(ddSkippedTests))
		// for testFQN := range ddSkippedTests {
		// 	fmt.Println(testFQN)
		// }

		durationTestOpt := time.Since(startTimeTestOpt)
		fmt.Printf("Finished fetching skippable tests! (took %v)\n", durationTestOpt)

		filePath := "./.dd/tests-discovery/rspec.json"
		cmdName := "bundle"
		cmdArgs := []string{"exec", "rspec", "--format", "progress", "--dry-run"}

		fmt.Println("Starting RSpec dry run...")
		startTime := time.Now()

		rspecCmd := exec.Command(cmdName, cmdArgs...)
		rspecCmd.Env = append(
			os.Environ(),
			"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1",
			"DD_TEST_OPTIMIZATION_DISCOVERY_FILE="+filePath,
		)
		output, err := rspecCmd.CombinedOutput()

		if err != nil {
			fmt.Printf("Failed to run RSpec dry run with output: %s\n", output)
			os.Exit(1)
		}

		duration := time.Since(startTime)
		fmt.Printf("Finished RSpec dry run! (took %v)\n", duration)

		// Read and parse the JSON stream file
		file, err := os.Open(filePath)
		if err != nil {
			fmt.Printf("Error opening JSON file: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()

		var tests []Test
		decoder := json.NewDecoder(file)
		for decoder.More() {
			var test Test
			if err := decoder.Decode(&test); err != nil {
				fmt.Printf("Error parsing JSON: %v\n", err)
				os.Exit(1)
			}
			tests = append(tests, test)
		}

		fmt.Printf("Parsed RSpec report with %d examples\n", len(tests))

		testFiles := make(map[string]bool)
		for _, test := range tests {
			if !ddSkippedTests[test.FQN] {
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

// func printSettings(settings net.SettingsResponseData) {
// 	fmt.Printf("Library Settings:\n")
// 	fmt.Printf("  ItrEnabled: %v\n", settings.ItrEnabled)
// 	fmt.Printf("  TestsSkipping: %v\n", settings.TestsSkipping)
// 	fmt.Printf("  CodeCoverage: %v\n", settings.CodeCoverage)
// 	fmt.Printf("  RequireGit: %v\n", settings.RequireGit)
// 	fmt.Printf("  FlakyTestRetriesEnabled: %v\n", settings.FlakyTestRetriesEnabled)
// 	fmt.Printf("  KnownTestsEnabled: %v\n", settings.KnownTestsEnabled)
// 	fmt.Printf("  ImpactedTestsEnabled: %v\n", settings.ImpactedTestsEnabled)
// 	fmt.Printf("  EarlyFlakeDetection.Enabled: %v\n", settings.EarlyFlakeDetection.Enabled)
// 	fmt.Printf("  EarlyFlakeDetection.FaultySessionThreshold: %d\n", settings.EarlyFlakeDetection.FaultySessionThreshold)
// 	fmt.Printf("  TestManagement.Enabled: %v\n", settings.TestManagement.Enabled)
// 	fmt.Printf("  TestManagement.AttemptToFixRetries: %d\n", settings.TestManagement.AttemptToFixRetries)
// 	fmt.Println()
// }

// func printSkippableTests(skippableTests map[string]map[string][]net.SkippableResponseDataAttributes) {
// 	fmt.Printf("Skippable Tests:\n")
// 	fmt.Printf("  Total Suites: %d\n", len(skippableTests))

// 	totalTests := 0
// 	for suite, tests := range skippableTests {
// 		totalTests += len(tests)
// 		fmt.Printf("  Suite: %s (%d tests)\n", suite, len(tests))
// 		for testName, testInstances := range tests {
// 			fmt.Printf("    Test: %s (%d instances)\n", testName, len(testInstances))
// 			for i, instance := range testInstances {
// 				fmt.Printf("      Instance %d:\n", i+1)
// 				fmt.Printf("        Parameters: %s\n", instance.Parameters)
// 				fmt.Printf("        Configurations: OsPlatform=%s, OsArchitecture=%s, RuntimeName=%s, RuntimeVersion=%s\n",
// 					instance.Configurations.OsPlatform, instance.Configurations.OsArchitecture,
// 					instance.Configurations.RuntimeName, instance.Configurations.RuntimeVersion)
// 			}
// 		}
// 	}
// 	fmt.Printf("  Total Tests: %d\n", totalTests)
// 	fmt.Println()
// }

func init() {
	rootCmd.AddCommand(testFilesCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
