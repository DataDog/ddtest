package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

type buildResult struct {
	onboard.BuildCommand
	Code       string `json:"code,omitempty"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code"`
	Diagnostic string `json:"diagnostic,omitempty"`
	// Reviewed marks preparation run only through --allow-reviewed-builds.
	Reviewed bool `json:"reviewed_opt_in,omitempty"`
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
		// Fact-backed prerequisites may run a repository script directly.
		localScript := build.Kind != "" && strings.HasPrefix(words[0], "./") && filepath.IsLocal(words[0])
		if strings.Contains(words[0], "=") || (filepath.Base(words[0]) != words[0] && !localScript) {
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
		if review, found := preparationNeedsReview(directory, build.BuildCommand); review && !t.allowReviewedBuilds {
			build.Status, build.Code, build.Diagnostic = "blocked", "preparation_review_required", found
			return fmt.Errorf("preparation requires separate review: %s; review customer scripts/plugins and network access, then opt in with --allow-reviewed-builds", build.Command)
		} else if review {
			// The final response reports every step run through this opt-in.
			build.Reviewed = true
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
		merged := mergeConfigurationEnvironment(onboard.WithCIProviderDefaults(build.Environment), env)
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
		if build.Kind != "" && strings.HasPrefix(words[0], "./") {
			executable = filepath.Join(directory, words[0])
		} else if build.Kind != "" {
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
			relative, relErr := filepath.Rel(workspace.CanonicalRoot(t.repositoryRoot), path)
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

// bundlerConfigs lists the files each reviewed bundler loads and executes.
// Rollup and webpack always run customer configuration; tsup and unbuild also
// run zero-config, bundling the package sources with their own defaults.
var bundlerConfigs = map[string][]string{
	"rollup":  {"rollup.config.js", "rollup.config.mjs", "rollup.config.cjs", "rollup.config.ts"},
	"webpack": {"webpack.config.js", "webpack.config.mjs", "webpack.config.cjs", "webpack.config.ts"},
	"tsup":    {"tsup.config.ts", "tsup.config.js", "tsup.config.mjs", "tsup.config.cjs", "tsup.config.json"},
	"unbuild": {"build.config.ts", "build.config.js", "build.config.mjs", "build.config.cjs"},
}

// preparationNeedsReview reports whether running build executes customer code
// that deserves review, and what review should look at.
func preparationNeedsReview(directory string, build onboard.BuildCommand) (bool, string) {
	switch build.Kind {
	case onboard.PreparationPackageBuild:
		return true, "package build lifecycle scripts"
	case onboard.PreparationDependencies:
		return true, "dependency installation, which may run install scripts and use the network"
	}
	words, err := shellquote.Split(build.Command)
	if err != nil || len(words) == 0 {
		return false, ""
	}
	configs, reviewed := bundlerConfigs[words[0]]
	if !reviewed {
		return false, ""
	}
	var found []string
	for _, name := range configs {
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && info.Mode().IsRegular() {
			found = append(found, name)
		}
	}
	var pkg map[string]json.RawMessage
	if data, err := os.ReadFile(filepath.Join(directory, "package.json")); err == nil && json.Unmarshal(data, &pkg) == nil {
		if _, ok := pkg[words[0]]; ok {
			found = append(found, `package.json "`+words[0]+`" settings`)
		}
	}
	if slices.ContainsFunc(words[1:], func(word string) bool {
		return word == "--config" || strings.HasPrefix(word, "--config=") || word == "-c"
	}) {
		found = append(found, "the --config file named by the command")
	}
	if len(found) == 0 && (words[0] == "tsup" || words[0] == "unbuild") {
		return false, ""
	}
	if len(found) == 0 {
		return true, words[0] + " command-line options"
	}
	return true, strings.Join(found, ", ")
}

// suggestPrerequisiteOutputs pre-fills each pending prerequisite's
// expected_files with the git-ignored files its package.json entry points
// name. These are suggestions from configuration, never from running the
// prerequisite; the caller still reviews them and accepts them with --facts.
func suggestPrerequisiteOutputs(ctx context.Context, root string, template *onboard.SetupFacts) {
	if template == nil {
		return
	}
	for i := range template.Configurations {
		for j := range template.Configurations[i].Prerequisites {
			fact := &template.Configurations[i].Prerequisites[j]
			if len(fact.Argv) == 0 || len(fact.ExpectedFiles) > 0 {
				continue
			}
			fact.ExpectedFiles = packageEntryOutputs(ctx, root, fact.Directory)
		}
	}
}

// packageEntryOutputs lists repository-relative files named by package.json
// entry points (main, module, browser, types, exports, bin) that git ignores.
// A tracked or unignored file is source, not a build output.
func packageEntryOutputs(ctx context.Context, root, directory string) []string {
	directory = filepath.FromSlash(directory)
	if directory == "" {
		directory = "."
	}
	if !filepath.IsLocal(directory) && directory != "." {
		return []string{}
	}
	data, err := os.ReadFile(filepath.Join(root, directory, "package.json"))
	if err != nil {
		return []string{}
	}
	var manifest map[string]any
	if json.Unmarshal(data, &manifest) != nil {
		return []string{}
	}
	var targets []string
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case string:
			targets = append(targets, value)
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				collect(value[key])
			}
		case []any:
			for _, item := range value {
				collect(item)
			}
		}
	}
	for _, key := range []string{"main", "module", "browser", "types", "typings", "exports", "bin"} {
		collect(manifest[key])
	}
	suggestions := []string{}
	for _, target := range targets {
		clean := filepath.Clean(filepath.Join(directory, filepath.FromSlash(target)))
		if !filepath.IsLocal(clean) || strings.Contains(clean, "*") || filepath.Base(clean) == "package.json" || slices.Contains(suggestions, filepath.ToSlash(clean)) {
			continue
		}
		parts := strings.Split(clean, string(filepath.Separator))
		if slices.Contains(parts, "node_modules") || slices.Contains(parts, ".git") {
			continue
		}
		if exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--no-index", "--", filepath.ToSlash(clean)).Run() != nil {
			continue
		}
		suggestions = append(suggestions, filepath.ToSlash(clean))
		if len(suggestions) == maxExpectedOutputs {
			break
		}
	}
	return suggestions
}
