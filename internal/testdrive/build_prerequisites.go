package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

type buildResult struct {
	onboard.BuildCommand
	Code       string `json:"code,omitempty"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

// Run the known build plan once, before either comparison mode. A failed build
// is missing setup, not a matching test failure. Never run builds in check-only.
func (t *Testdrive) runBuildPrerequisites(ctx context.Context, output io.Writer, item *configurationResult) error {
	for _, build := range item.Prerequisites {
		item.Preparation = append(item.Preparation, buildResult{BuildCommand: build, Status: "not exercised"})
	}
	commands := make([][]string, len(item.Prerequisites))
	for i, build := range item.Prerequisites {
		words, err := shellquote.Split(build.Command)
		_, directoryErr := localSetupDirectory(t.repositoryRoot, build.Directory)
		if err != nil || len(words) == 0 || directoryErr != nil {
			return fmt.Errorf("build prerequisite needs explicit command/directory review: [%s] %s", build.Directory, build.Command)
		}
		for _, word := range words {
			if (build.Kind != onboard.PreparationPatch && build.Kind != onboard.PreparationPackageBuild && strings.ContainsAny(word, "*?[")) || word == "--watch" || strings.HasPrefix(word, "--watch=") || word == "-w" {
				return fmt.Errorf("build prerequisite needs glob/watch review: %s", build.Command)
			}
		}
		if strings.Contains(words[0], "=") || filepath.Base(words[0]) != words[0] {
			return fmt.Errorf("build prerequisite needs environment/executable review: %s", build.Command)
		}
		commands[i] = words
	}
	if t.checkOnly {
		return nil
	}
	env := testEnvironment("http://127.0.0.1:1", "build-prerequisite")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	env["PATH"] = filepath.Join(t.repositoryRoot, "node_modules", ".bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	for i, words := range commands {
		if err := ctx.Err(); err != nil {
			return err
		}
		build := &item.Preparation[i]
		_, _ = fmt.Fprintf(output, "Preparing prerequisite: %s\n", build.Command)
		directory, err := localSetupDirectory(t.repositoryRoot, build.Directory)
		if err != nil {
			return err
		}
		if preparationNeedsReview(build.BuildCommand) && !t.allowReviewedBuilds {
			build.Status, build.Code = "blocked", "preparation_review_required"
			return fmt.Errorf("preparation requires separate review: %s; review customer scripts/plugins and network access, then opt in with --allow-reviewed-builds", build.Command)
		}
		if build.Kind == onboard.PreparationPatch {
			if err := applyLiteralPatch(t.repositoryRoot, directory, words); err != nil {
				build.Status, build.Code, build.Diagnostic = "blocked", "patch_review_required", reportText(err.Error())
				return err
			}
			code := 0
			build.ExitCode = &code
			build.Status = "passed"
			continue
		}
		args := words[1:]
		merged := mergeConfigurationEnvironment(onboard.WithGitHubActionsDefaults(build.Environment), env)
		nodeOptions := os.Getenv("NODE_OPTIONS")
		if value, exists := build.Environment["NODE_OPTIONS"]; exists {
			nodeOptions = value
		}
		merged["NODE_OPTIONS"] = stripDatadogNodeOptions(nodeOptions)
		original := t.configurationSourceRoot
		if original == "" {
			original = t.repositoryRoot
		}
		merged = isolatedCIEnvironment(merged, original, t.checkoutRoot())
		executable := filepath.Join(t.repositoryRoot, "node_modules", ".bin", words[0])
		localCompiler := filepath.Join(directory, "node_modules", ".bin", words[0])
		if info, err := os.Stat(localCompiler); err == nil && !info.IsDir() {
			executable = localCompiler
		}
		if build.Kind != "" {
			executable, err = exec.LookPath(words[0])
			if err != nil {
				build.Status, build.Code = "blocked", "tool_not_installed"
				return fmt.Errorf("preparation tool not installed: %s", words[0])
			}
		} else if info, statErr := os.Stat(executable); statErr != nil || info.IsDir() {
			executable, args, err = t.loaderPreparationTool(ctx, directory, words[0], args, merged)
			if err != nil {
				build.Status, build.Code = "blocked", "tool_not_installed"
				if errors.Is(err, context.DeadlineExceeded) {
					build.Status, build.Code = "failed", "preparation_timed_out"
				}
				return err
			}
		}
		data, err := setupOutput(ctx, t.executor, t.repositoryRoot, build.Directory, executable, args, merged)
		exitCode := commandExitCode(err)
		build.ExitCode = &exitCode
		build.Status = "passed"
		if err != nil {
			build.Status, build.Code, build.Diagnostic = "failed", "preparation_failed", commandDiagnostic(data)
			if errors.Is(err, context.DeadlineExceeded) {
				build.Code = "preparation_timed_out"
				build.Diagnostic = "Preparation timed out; review watch mode or increase --timeout."
				return fmt.Errorf("preparation timed out: %s: %w", build.Command, err)
			}
			writeCommandFailure(output, "Build prerequisite", data)
			return fmt.Errorf("build prerequisite %s: %w", build.Command, err)
		}

		for _, file := range build.ExpectedFiles {
			path, err := filepath.EvalSymlinks(filepath.Join(t.repositoryRoot, file))
			relative, relErr := filepath.Rel(canonicalWorkflowRoot(t.repositoryRoot), path)
			info, statErr := os.Stat(path)
			if err != nil || relErr != nil || !filepath.IsLocal(relative) || statErr != nil || !info.Mode().IsRegular() {
				build.Status = "failed"
				build.Diagnostic = "Declared build output was not produced as a repository-local regular file: " + file
				return fmt.Errorf("%s", build.Diagnostic)
			}
		}
	}
	return nil
}

func preparationNeedsReview(build onboard.BuildCommand) bool {
	if build.Kind == onboard.PreparationPackageBuild || build.Kind == onboard.PreparationDependencies {
		return true
	}
	words, err := shellquote.Split(build.Command)
	return err == nil && len(words) > 0 && slices.Contains([]string{"rollup", "webpack", "tsup", "unbuild"}, words[0])
}
