package onboard

import (
	"fmt"
	"path/filepath"
	"strings"
)

// VitestRunnerCommand is the reproducible, local ddtest run command shape.
// Separate saved plans and CI-node assignments need their own validation.
type VitestRunnerCommand struct {
	Config  string
	Options []string
	Files   []string
}

func IsDDTestCommand(command string) bool { return filepath.Base(command) == "ddtest" }

func ParseVitestRunnerCommand(args []string) (VitestRunnerCommand, error) {
	var result VitestRunnerCommand
	run := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "run" && !run {
			run = true
			continue
		}
		if arg == "--" {
			result.Files = append(result.Files, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if !run {
				return result, fmt.Errorf("expected ddtest run, got %q", arg)
			}
			result.Files = append(result.Files, arg)
			continue
		}
		key, value, inline := strings.Cut(arg, "=")
		switch key {
		case "--platform", "--framework", "--vitest-config", "--min-parallelism", "--max-parallelism", "--ci-job-overhead", "--target-time", "--tests-location", "--tests-exclude-pattern":
		default:
			return result, fmt.Errorf("ddtest run option %s requires separate validation; saved plans, CI-node mode and custom worker environments are not reproduced", key)
		}
		if !inline {
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return result, fmt.Errorf("missing value for %s", key)
			}
			value = args[i]
		}
		if key == "--platform" && value != "javascript" || key == "--framework" && value != "vitest" {
			return result, fmt.Errorf("this validation requires JavaScript and Vitest")
		}
		if key == "--vitest-config" {
			result.Config = value
		}
		if (key == "--vitest-config" || key == "--tests-location" || key == "--tests-exclude-pattern") && value != "" && !filepath.IsLocal(value) {
			return result, fmt.Errorf("%s must use a repository-relative path for isolated validation", key)
		}
		result.Options = append(result.Options, key+"="+value)
	}
	if !run {
		return result, fmt.Errorf("expected ddtest run")
	}
	for _, file := range result.Files {
		if !filepath.IsLocal(file) {
			return result, fmt.Errorf("test file selections must use repository-relative paths for isolated validation")
		}
	}
	return result, nil
}
