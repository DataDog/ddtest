package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"
)

type commandDescription struct {
	SchemaVersion      int              `json:"schema_version"`
	Command            describedCommand `json:"command"`
	TIARequest         describedRequest `json:"tia_request"`
	CandidateTestFiles []string         `json:"candidate_test_files"`
}

type describedCommand struct {
	ID        string   `json:"id"`
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd"`
	TestRoots []string `json:"test_roots"`
	// TestsLocation is the --tests-location glob that narrowed the candidates.
	TestsLocation string `json:"tests_location,omitempty"`
}

type describedRequest struct {
	Service        string                     `json:"service"`
	Env            string                     `json:"env"`
	RepositoryURL  string                     `json:"repository_url"`
	SHA            string                     `json:"sha"`
	TestLevel      settings.TestSkippingLevel `json:"test_level"`
	Configurations map[string]string          `json:"configurations"`
}

func newDescribeCommand() *cobra.Command {
	var id, output, interpreter, ciProvider, ciConfig, environments string
	var roots []string
	command := &cobra.Command{
		Use:   "describe [options] [-- pytest arguments ...]",
		Short: "Export an experimental pytest command catalog without running tests",
		Long:  "Temporary exploration command. Exports a manual pytest command or a catalog from expanded GitLab jobs as JSON. It does not collect tests, contact Datadog, or change a saved plan. Run it from the repository root. The format is experimental.",
		Args: func(cmd *cobra.Command, args []string) error {
			if ciConfig != "" {
				if len(args) != 0 || id != "" || len(roots) != 0 || interpreter != "" {
					return fmt.Errorf("CI discovery cannot be combined with a manual command")
				}
				if environments == "" {
					return fmt.Errorf("CI discovery requires --command-environments")
				}
				return nil
			}
			if ciProvider != "" || environments != "" {
				return fmt.Errorf("CI discovery requires --ci-config")
			}
			if id == "" || len(roots) == 0 {
				return fmt.Errorf("supply --id and --test-root for a manual command")
			}
			if cmd.ArgsLenAtDash() != 0 || len(args) == 0 {
				return fmt.Errorf("supply the test command after --")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if ciConfig != "" {
				if cmd.Flags().Changed("test-skipping-mode") && settings.GetTestSkippingLevel() != settings.TestSkippingLevelSuite {
					return fmt.Errorf("CI catalog discovery supports suite level only")
				}
				catalog, err := describeCI(cmd, ciProvider, ciConfig, environments)
				if err != nil {
					return err
				}
				return writeDescription(cmd, output, catalog)
			}
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("command ID must not be empty")
			}
			if selected := settings.GetPlatform(); selected != "" && selected != "python" {
				return fmt.Errorf("describe currently supports Python only")
			}
			if selected := settings.GetFramework(); selected != "" && selected != "pytest" {
				return fmt.Errorf("describe currently supports pytest only")
			}
			argv, err := describePytestArgv(args, os.Getenv("PYTEST_ADDOPTS"))
			if err != nil {
				return err
			}
			files, normalizedRoots, err := describePytestFiles(cmd.Context(), roots)
			if err != nil {
				return err
			}
			// Narrow candidates to the global --tests-location glob.
			location := settings.GetTestsLocation()
			if files, err = filterTestsLocation(files, location); err != nil {
				return err
			}
			metadata, err := describeCheckout()
			if err != nil {
				return err
			}
			configurations, err := describeConfigurations(cmd.Context(), interpreter, args)
			if err != nil {
				return err
			}
			level := settings.GetTestSkippingLevel()
			if !cmd.Flags().Changed("test-skipping-mode") {
				if value, exists := os.LookupEnv("_DD_CIVISIBILITY_ITR_SUITE_MODE"); exists {
					level = settings.TestSkippingLevelTest
					if strings.EqualFold(value, "true") || value == "1" {
						level = settings.TestSkippingLevelSuite
					}
				}
			}
			env, exists := os.LookupEnv("_CI_DD_ENV")
			if !exists {
				env = os.Getenv("DD_ENV")
			}
			description := commandDescription{
				SchemaVersion: 2,
				Command:       describedCommand{ID: id, Argv: argv, Cwd: ".", TestRoots: normalizedRoots, TestsLocation: location},
				TIARequest: describedRequest{Service: os.Getenv("DD_SERVICE"), Env: env,
					RepositoryURL: metadata.RepositoryURL, SHA: metadata.CommitSha,
					TestLevel: level, Configurations: configurations},
				CandidateTestFiles: files,
			}
			return writeDescription(cmd, output, description)
		},
	}
	command.Flags().StringVar(&id, "id", "", "Caller-provided command and environment ID")
	command.Flags().StringArrayVar(&roots, "test-root", nil, "Candidate directory relative to the repository root (repeatable)")
	command.Flags().StringVar(&interpreter, "python", "", "Worker Python interpreter for pytest entry points (or supply complete --runtime-tags)")
	command.Flags().StringVar(&output, "output", "", "Also write the JSON catalog to this file")
	command.Flags().StringVar(&ciProvider, "ci-provider", "", "CI provider for catalog discovery (gitlab)")
	command.Flags().StringVar(&ciConfig, "ci-config", "", "Expanded CI configuration inside this repository")
	command.Flags().StringVar(&environments, "command-environments", "", "Repository-exported environment-to-command metadata")
	return command
}

func describePytestArgv(args []string, addopts string) ([]string, error) {
	return framework.PytestCommandArguments(args, addopts)
}
func describePytestFiles(ctx context.Context, roots []string) ([]string, []string, error) {
	return framework.NewPytest(platform.NewPython()).CandidateFiles(ctx, roots)
}

// filterTestsLocation keeps candidate files matching a doublestar glob.
func filterTestsLocation(files []string, location string) ([]string, error) {
	if location == "" {
		return files, nil
	}
	if !doublestar.ValidatePattern(location) {
		return nil, fmt.Errorf("invalid --tests-location pattern %q", location)
	}
	kept := []string{}
	for _, file := range files {
		if matched, _ := doublestar.Match(location, file); matched {
			kept = append(kept, file)
		}
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("no candidate test files match --tests-location %q", location)
	}
	return kept, nil
}

func describeConfigurations(ctx context.Context, interpreter string, argv []string) (map[string]string, error) {
	tags, err := settings.GetRuntimeTagsMap()
	if err != nil {
		return nil, err
	}
	if settings.GetRuntimeTags() == "" {
		if interpreter == "" {
			if filepath.Base(argv[0]) == "pytest" {
				return nil, fmt.Errorf("supply --python or complete --runtime-tags for a pytest entry point")
			}
			interpreter = argv[0]
		}
		tags, err = platform.NewPython().CreateTagsMapForInterpreter(ctx, interpreter)
		if err != nil {
			return nil, err
		}
	}
	configurations := make(map[string]string)
	for _, key := range []string{"runtime.name", "runtime.version", "os.platform", "os.architecture", "os.version"} {
		if tags[key] == "" {
			return nil, fmt.Errorf("describe requires a complete runtime configuration: missing %s", key)
		}
		configurations[key] = tags[key]
	}
	return configurations, nil
}

func describeCheckout() (git.LocalGitData, error) {
	metadata, err := git.GetLocalGitData()
	if err != nil {
		return git.LocalGitData{}, fmt.Errorf("read checkout metadata: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return git.LocalGitData{}, err
	}
	sourceRoot, err := filepath.EvalSymlinks(metadata.SourceRoot)
	if err != nil {
		return git.LocalGitData{}, err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return git.LocalGitData{}, err
	}
	if cwd != sourceRoot {
		return git.LocalGitData{}, fmt.Errorf("run describe from the repository root")
	}

	return metadata, nil
}

func writeDescription(cmd *cobra.Command, output string, description any) error {
	data, err := json.MarshalIndent(description, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if output != "" {
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(output, data, 0644); err != nil {
			return err
		}
	}
	_, err = cmd.OutOrStdout().Write(data)
	return err
}
