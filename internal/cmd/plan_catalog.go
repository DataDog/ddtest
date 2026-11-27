package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/spf13/cobra"
)

type commandSelection struct {
	onboard.PythonCICommand
	Job               string   `json:"job"`
	Decision          string   `json:"decision"`
	Reason            string   `json:"reason"`
	RunnableFiles     []string `json:"runnable_files"`
	UnskippableSuites int      `json:"unskippable_suites,omitempty"`
}

type jobSelection struct {
	onboard.CICommandJob
	Decision string   `json:"decision"`
	Reason   string   `json:"reason"`
	Commands []string `json:"commands"`
}

type catalogSelection struct {
	SchemaVersion int                `json:"schema_version"`
	RepositoryURL string             `json:"repository_url"`
	SHA           string             `json:"sha"`
	TestLevel     string             `json:"test_level"`
	Commands      []commandSelection `json:"commands"`
	Jobs          []jobSelection     `json:"jobs"`
	Unresolved    []string           `json:"unresolved,omitempty"`
}

func addCatalogPlanFlags(cmd *cobra.Command) {
	cmd.Flags().String("ci-provider", "", "CI provider for command planning (gitlab)")
	cmd.Flags().String("ci-config", "", "Expanded CI configuration inside this repository")
	cmd.Flags().String("command-environments", "", "Exported commands with optional per-environment configurations")
	cmd.Flags().String("output", "", "Write CI command decisions as JSON")
	cmd.Flags().String("commands", "", "Plan a set of `ddtest describe` outputs grouped by job (instead of --ci-provider)")
}

func catalogFlag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

func isCatalogPlan(cmd *cobra.Command) bool {
	for _, name := range []string{"ci-provider", "ci-config", "command-environments", "output", "commands"} {
		if catalogFlag(cmd, name) != "" {
			return true
		}
	}
	return false
}

func validatePlanArguments(cmd *cobra.Command, args []string) error {
	if !isCatalogPlan(cmd) {
		return usePositionalTestPatterns(cmd, args)
	}
	required := []string{"ci-provider", "ci-config", "command-environments", "output"}
	if catalogFlag(cmd, "commands") != "" {
		for _, name := range []string{"ci-provider", "ci-config", "command-environments"} {
			if catalogFlag(cmd, name) != "" {
				return fmt.Errorf("--commands cannot be combined with --%s", name)
			}
		}
		required = []string{"output"}
	}
	for _, name := range required {
		if catalogFlag(cmd, name) == "" {
			return fmt.Errorf("CI command planning requires --%s", name)
		}
	}
	if len(args) != 0 || settings.GetCommand() != "" || settings.GetTestsLocation() != "" ||
		settings.GetTestDiscoveryCache() != "" || settings.GetForceFullTestDiscovery() {
		return fmt.Errorf("CI command planning cannot be combined with manual test selection or full discovery")
	}
	if cmd.Flags().Changed("test-skipping-mode") && settings.GetTestSkippingLevel() != settings.TestSkippingLevelSuite {
		return fmt.Errorf("CI command planning supports suite level only")
	}
	return nil
}

func runCatalogPlan(cmd *cobra.Command, telemetryClient telemetry.Client) error {
	var catalog onboard.PythonCICommandCatalog
	var err error
	if path := catalogFlag(cmd, "commands"); path != "" {
		catalog, err = loadCommandSet(path)
	} else {
		catalog, err = describeCI(cmd, catalogFlag(cmd, "ci-provider"), catalogFlag(cmd, "ci-config"), catalogFlag(cmd, "command-environments"))
	}
	if err != nil {
		return err
	}
	selection, err := selectCatalogCommands(cmd.Context(), catalog, telemetryClient)
	if err != nil {
		return err
	}
	return writeDescription(cmd, catalogFlag(cmd, "output"), selection)
}

func selectCatalogCommands(ctx context.Context, catalog onboard.PythonCICommandCatalog, telemetryClient telemetry.Client) (catalogSelection, error) {
	if err := ctx.Err(); err != nil {
		return catalogSelection{}, err
	}
	seen := map[string]bool{}
	for _, job := range catalog.Jobs {
		for _, command := range job.Commands {
			if seen[command.ID] {
				return catalogSelection{}, fmt.Errorf("duplicate command identity %q", command.ID)
			}
			seen[command.ID] = true
		}
	}
	result := catalogSelection{SchemaVersion: 1, RepositoryURL: catalog.RepositoryURL, SHA: catalog.SHA,
		TestLevel: "suite", Commands: []commandSelection{}, Jobs: []jobSelection{}, Unresolved: catalog.Unresolved}
	for _, job := range catalog.Jobs {
		planned := jobSelection{CICommandJob: job.CICommandJob, Decision: "run",
			Reason: "job contains unresolved commands", Commands: []string{}}
		allSkip := len(job.Commands) > 0 && len(job.Unresolved) == 0
		for _, command := range job.Commands {
			if err := ctx.Err(); err != nil {
				return catalogSelection{}, err
			}
			decision := commandSelection{PythonCICommand: command, Job: job.Name, Decision: "run",
				RunnableFiles: slices.Clone(command.CandidateTestFiles)}
			decision.Reason = commandPlanningGap(job, command, catalog.RepositoryURL)
			if decision.Reason == "" {
				selected, err := planner.SelectPythonSuiteFiles(ctx, command.CandidateTestFiles, command.Configurations, telemetryClient)
				switch {
				case ctx.Err() != nil:
					return catalogSelection{}, ctx.Err()
				case err != nil:
					decision.Reason = "suite planning unavailable: " + err.Error()
				default:
					decision.RunnableFiles = selected.RunnableFiles
					decision.UnskippableSuites = selected.UnskippableSuites
					decision.Reason = "runnable suites"
					if selected.UnskippableSuites > 0 {
						decision.Reason = "unskippable suites require execution"
					}
					if len(selected.RunnableFiles) == 0 {
						decision.Decision, decision.Reason = "skip", "all current suites skippable"
					}
				}
			}
			allSkip = allSkip && decision.Decision == "skip"
			planned.Commands = append(planned.Commands, command.ID)
			result.Commands = append(result.Commands, decision)
		}
		if allSkip {
			planned.Decision, planned.Reason = "skip", "all commands skippable"
		} else if len(job.Unresolved) == 0 && len(job.Commands) > 0 {
			planned.Reason = "a command requires execution"
		}
		result.Jobs = append(result.Jobs, planned)
	}
	return result, nil
}

func commandPlanningGap(job onboard.PythonCIJob, command onboard.PythonCICommand, repositoryURL string) string {
	if command.Unresolved != "" || len(job.Unresolved) > 0 {
		return "unresolved command or job"
	}
	if command.Cwd != "." || len(command.CandidateTestFiles) == 0 {
		return "no supported suite inventory"
	}
	tags := command.Configurations
	for _, key := range []string{"runtime.name", "runtime.version", "os.platform", "os.architecture", "os.version"} {
		if strings.TrimSpace(tags[key]) == "" {
			return "missing worker configuration: " + key
		}
	}
	if command.PythonVersion == "" || (tags["runtime.version"] != command.PythonVersion &&
		!strings.HasPrefix(tags["runtime.version"], command.PythonVersion+".")) {
		return "worker Python version differs from the command environment"
	}
	for key := range tags {
		if !slices.Contains([]string{"runtime.name", "runtime.version", "runtime.architecture",
			"os.platform", "os.architecture", "os.version", "test.bundle"}, key) &&
			!strings.HasPrefix(key, "test.configuration.") {
			return "unsupported worker configuration: " + key
		}
	}
	for _, key := range []string{"DD_SERVICE", "DD_ENV", "DD_TAGS"} {
		if value, declared := job.Variables[key]; declared && planningIdentityValue(key, value, repositoryURL) != planningIdentityValue(key, os.Getenv(key), repositoryURL) {
			return "worker identity override requires separate planning: " + key
		}
	}
	for key, value := range api.CustomConfigurationsFromTags(job.Variables["DD_TAGS"]) {
		if actual, exists := tags["test.configuration."+key]; !exists || actual != value {
			return "worker custom configuration differs from command metadata: " + key
		}
	}
	return ""
}

func planningIdentityValue(key, value, repositoryURL string) string {
	switch key {
	case "DD_SERVICE":
		if value == "" {
			return runmetadata.ServiceNameFromRepositoryURL(repositoryURL)
		}
	case "DD_ENV":
		return runmetadata.EnvironmentName(value)
	}
	return value
}
