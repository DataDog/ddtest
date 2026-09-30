package testdrive

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

type buildResult struct {
	onboard.BuildCommand
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
		if err != nil || len(words) == 0 || filepath.Clean(build.Directory) != "." {
			return fmt.Errorf("build prerequisite needs explicit command/directory review: [%s] %s", build.Directory, build.Command)
		}
		for _, word := range words {
			if (build.Kind != "patch" && build.Kind != "package-build" && strings.ContainsAny(word, "*?[")) || word == "--watch" || strings.HasPrefix(word, "--watch=") || word == "-w" {
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
		_, _ = fmt.Fprintf(output, "Running build prerequisite: %s\n", build.Command)
		executable := filepath.Join(t.repositoryRoot, "node_modules", ".bin", words[0])
		if build.Kind != "" {
			var err error
			executable, err = exec.LookPath(words[0])
			if err != nil {
				return err
			}
		}
		args := words[1:]
		if build.Kind == "patch" {
			matches, err := filepath.Glob(filepath.Join(t.repositoryRoot, words[len(words)-1]))
			if err != nil || len(matches) == 0 {
				return fmt.Errorf("patch prerequisite has no local matches: %s", build.Command)
			}
			args = append(append([]string{}, args[:len(args)-1]...), matches...)
		}
		stepEnv := maps.Clone(build.Environment)
		if stepEnv == nil {
			stepEnv = map[string]string{}
		}
		maps.Copy(stepEnv, env)
		data, err := t.executor.CombinedOutput(ctx, executable, args, stepEnv)
		exitCode := commandExitCode(err)
		build.ExitCode = &exitCode
		build.Status = "passed"
		if err != nil {
			build.Status, build.Diagnostic = "failed", commandDiagnostic(data)
			writeCommandFailure(output, "Build prerequisite", data)
			return fmt.Errorf("build prerequisite %s: %w", build.Command, err)
		}
	}
	return nil
}
