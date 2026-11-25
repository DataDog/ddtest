package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/kballard/go-shellquote"
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
	var id, output, interpreter string
	var roots []string
	command := &cobra.Command{
		Use:   "describe --id ID --test-root DIRECTORY [--test-root DIRECTORY ...] -- pytest [arguments ...]",
		Short: "Export an experimental pytest command catalog without running tests",
		Long:  "Temporary exploration command. Exports candidate pytest files and the TIA request context as JSON. It does not collect tests, contact Datadog, or change a saved plan. Run it from the repository root. The format is experimental.",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 0 || len(args) == 0 {
				return fmt.Errorf("supply the test command after --")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
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
			metadata, err := git.GetLocalGitData()
			if err != nil {
				return fmt.Errorf("read checkout metadata: %w", err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			sourceRoot, err := filepath.EvalSymlinks(metadata.SourceRoot)
			if err != nil {
				return err
			}
			cwd, err = filepath.EvalSymlinks(cwd)
			if err != nil {
				return err
			}
			if cwd != sourceRoot {
				return fmt.Errorf("run describe from the repository root")
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
				Command:       describedCommand{ID: id, Argv: argv, Cwd: ".", TestRoots: normalizedRoots},
				TIARequest: describedRequest{Service: os.Getenv("DD_SERVICE"), Env: env,
					RepositoryURL: metadata.RepositoryURL, SHA: metadata.CommitSha,
					TestLevel: level, Configurations: configurations},
				CandidateTestFiles: files,
			}
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
		},
	}
	command.Flags().StringVar(&id, "id", "", "Caller-provided command and environment ID")
	command.Flags().StringArrayVar(&roots, "test-root", nil, "Candidate directory relative to the repository root (repeatable)")
	command.Flags().StringVar(&interpreter, "python", "", "Worker Python interpreter for pytest entry points (or supply complete --runtime-tags)")
	command.Flags().StringVar(&output, "output", "", "Also write the JSON catalog to this file")
	_ = command.MarkFlagRequired("id")
	_ = command.MarkFlagRequired("test-root")
	return command
}

func describePytestArgv(args []string, addopts string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("describe requires one pytest invocation")
	}
	insert := 1
	if filepath.Base(args[0]) != "pytest" {
		if len(args) < 3 || (filepath.Base(args[0]) != "python" && filepath.Base(args[0]) != "python3") || args[1] != "-m" || args[2] != "pytest" {
			return nil, fmt.Errorf("describe requires one pytest or python -m pytest invocation")
		}
		insert = 3
	}
	for _, arg := range args {
		if slices.Contains([]string{"&&", "||", ";", "|", ">", "<"}, arg) {
			return nil, fmt.Errorf("describe requires one pytest invocation")
		}
	}
	options, err := shellquote.Split(addopts)
	if err != nil {
		return nil, fmt.Errorf("parse PYTEST_ADDOPTS: %w", err)
	}
	argv := slices.Clone(args[:insert])
	argv = append(argv, options...)
	return append(argv, args[insert:]...), nil
}

func describePytestFiles(ctx context.Context, roots []string) ([]string, []string, error) {
	var normalizedRoots []string
	for _, root := range roots {
		if root == "" || !filepath.IsLocal(root) {
			return nil, nil, fmt.Errorf("test roots must be directories inside the repository")
		}
		root = filepath.Clean(root)
		info, err := os.Stat(root)
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("test root %q is not a directory", root)
		}
		normalizedRoots = append(normalizedRoots, filepath.ToSlash(root))
	}
	fw := framework.NewPytest(platform.NewPython())
	matcher, err := utils.NewPathMatcher(fw.TestPatternForRoots(normalizedRoots))
	if err != nil {
		return nil, nil, err
	}
	var files []string
	for _, root := range normalizedRoots {
		discovered, err := fw.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: filepath.ToSlash(filepath.Join(root, "**", "*.py"))})
		if err != nil {
			return nil, nil, err
		}
		for _, file := range discovered {
			if matcher.Match(file) {
				files = append(files, file)
			}
		}
	}
	slices.Sort(files)
	files = slices.Compact(files)
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("the candidate test file inventory is empty")
	}
	return files, normalizedRoots, nil
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
