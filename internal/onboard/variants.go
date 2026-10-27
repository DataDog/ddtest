package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Resolve only declared scalar environment values and simple parameter reads.
// Never evaluate a command substitution or inherit the evaluator's environment.
func resolveVariantStep(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, frameworks ...string) (runtimeStep, map[string]string, error) {
	// An action's environment is not inherited by subsequent run steps. Keep
	// opaque actions outside local validation; do not demand their CI secrets.
	cypressAction := len(frameworks) > 0 && frameworks[0] == "cypress" && strings.HasPrefix(strings.ToLower(step.Uses), "cypress-io/github-action@")
	if step.Run == "" && step.Uses != "" && step.ResolutionError == "" && !cypressAction {
		return step, nil, nil
	}
	var err error
	step.Run, err = interpolateMatrix(step.Run, row)
	if err != nil {
		return step, nil, err
	}
	// GitHub evaluates expressions before the shell runs, even inside single
	// quotes. Anything left after matrix substitution cannot be reproduced.
	if expression := workflowExpression.FindString(step.Run); expression != "" {
		return step, nil, fmt.Errorf("unresolved CI workflow expression requires review: %s", expression)
	}
	if directory := stepDirectory(workflow, job, step); strings.Contains(directory, "${{") {
		step.WorkingDirectory, err = interpolateMatrix(directory, row)
		if err != nil {
			return step, nil, err
		}
	}
	if step.Shell == "" && job.Defaults.Run.Shell == "" && workflow.Defaults.Run.Shell == "" {
		step.Shell = runnerDefaultShell(job, row)
	}

	if len(frameworks) > 0 && frameworks[0] == "webdriverio" {
		step.WorkingDirectory, err = runtimeValue(stepDirectory(workflow, job, step), row)
		if err != nil {
			return step, nil, err
		}
	}
	if len(frameworks) > 0 && frameworks[0] == "cypress" && strings.HasPrefix(strings.ToLower(step.Uses), "cypress-io/github-action@") {
		step.With = maps.Clone(step.With)
		for key, value := range step.With {
			if !slices.Contains(cypressResolvedInputs, key) {
				continue
			}
			resolved, e := runtimeValue(value, row)
			if e != nil {
				return step, nil, e
			}
			step.With[key] = resolved
		}
	}
	// Only Bash conditions with fully known operands may remove an inactive
	// branch. Leave opaque shell control flow and all other shells unchanged.
	shell := workflow.Defaults.Run.Shell
	for _, candidate := range []string{job.Defaults.Run.Shell, step.Shell} {
		if candidate != "" {
			shell = candidate
		}
	}
	shellWords, _ := shellquote.Split(shell)
	if supportedCIShell(shell) && (shell == "" || len(shellWords) > 0 && shellWords[0] == "bash") {
		step.Run = resolveStaticShellBranches(step.Run)
	}
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		value := env[key]
		if len(frameworks) > 0 {
			if language := discoveryLanguageFor(frameworks[0]); language != nil {
				rewritten, handled, err := language.variantValue(workflow, job, step, row, key, value)
				if err != nil {
					return step, nil, err
				}
				if handled {
					value = rewritten
				}
			}
		}
		// Local Cypress validation disables Cloud recording and uses the
		// evaluator's private binary cache. Never resolve or forward CI keys
		// and runner-specific cache paths for these managed settings.
		if len(frameworks) > 0 && frameworks[0] == "cypress" &&
			slices.Contains([]string{"CYPRESS_RECORD_KEY", "CYPRESS_CLOUD_TOKEN", "CYPRESS_RECORD", "CYPRESS_PARALLEL", "CYPRESS_CACHE_FOLDER"}, key) {
			delete(env, key)
			continue
		}
		// A remote browser grid's credentials are CI secrets that never exist
		// locally. The step still runs in CI under the tracer; local validation
		// reports it instead of demanding the secret.
		if len(frameworks) > 0 && frameworks[0] == "webdriverio" {
			if provider := remoteGridSecret(key, value); provider != "" {
				step.RemoteGrid = provider
				delete(env, key)
				continue
			}
		}
		if len(frameworks) > 0 && frameworks[0] == "vitest" && strings.HasPrefix(key, "DD_TEST_OPTIMIZATION_RUNNER_") {
			if _, ok := VitestRunnerEnvironmentFlag(key); !ok {
				return step, nil, fmt.Errorf("CI ddtest runner setting requires explicit command-based validation; not forwarded or silently dropped: %s", key)
			}
			resolved, err := runtimeValue(value, row)
			if err != nil {
				return step, nil, err
			}
			env[key] = resolved
			continue
		}

		if key == "PATH" || key == "NODE_PATH" {
			return step, nil, fmt.Errorf("CI environment overrides executable/module selection: %s", key)
		}
		cucumber := len(frameworks) > 0 && frameworks[0] == "cucumber"
		if key == "NODE_OPTIONS" || (cucumber && key == "DD_CUCUMBER_NODE_OPTIONS") {
			var modules []string
			if cucumber {
				modules = []string{"@istanbuljs/esm-loader-hook"}
			}
			ordinary, err := resolveNodeOptions(value, row, conditionEnvironment(workflow, job, step, row), modules...)
			if err != nil && len(frameworks) > 0 && frameworks[0] == "cypress" {
				ordinary, err = cypressLocalNodeOptions(value, workflow, job, step, row)
			}
			if err != nil {
				return step, nil, err
			}
			// An explicit empty value must override the caller's Node options.
			env[key] = ordinary
			continue
		}
		applicationCoverage := len(frameworks) > 0 && frameworks[0] == "cypress" && key == "DD_TEST_COVERAGE"
		// Other DD_* settings configure CI's agent, intake or session, which local
		// validation replaces. Plugin settings change test behavior and are kept.
		if strings.HasPrefix(key, "DD_") && !applicationCoverage && !TracerPluginSetting(key) {
			delete(env, key)
			continue
		}
		if workflow.Provider == "gitlab" || workflow.Provider == "buildkite" {
			if literal, ok := step.LiteralEnv[key]; ok && literal == value {
				// Quoted shell exports are values, not another CI expression.
				env[key] = literal
				continue
			}
			var err error
			value, err = nativeEnvironmentValue(value, env)
			if err != nil {
				return step, nil, fmt.Errorf("CI environment variable %s requires review: %w", key, err)
			}
		}
		resolved, err := runtimeValue(value, row)
		if err != nil && len(frameworks) > 0 && frameworks[0] == "cypress" {
			// Local comparison installs its own tracer and uses the same app
			// coverage flag in both modes. CI installation/preload conditions
			// are checked separately; action paths must never become local env.
			known := conditionEnvironment(workflow, job, step, row)
			known["DD_TRACE_PACKAGE"], known["DD_TRACE_ESM_IMPORT"] = actionPreload, "\x00DD_ACTION_IMPORT\x00"
			resolved, err = bootstrapOptions(value, row, known)
			if err == nil && strings.ContainsRune(resolved, 0) {
				err = fmt.Errorf("cypress application environment %s forwards a CI-only action path", key)
			}
		}

		if err != nil {
			return step, nil, fmt.Errorf("CI environment variable %s requires review: %w", key, err)
		}
		env[key] = resolved
	}
	if len(env) == 0 {
		env = nil
	}
	if len(env) == 0 || !strings.Contains(step.Run, "$") {
		return nativeInlineControls(step, env, workflow.Provider)
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(step.Run), "")
	if err != nil {
		return step, env, nil
	}
	var expansionErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		if word, ok := node.(*syntax.Word); ok {
			if unquotedGlob(word) {
				expansionErr = fmt.Errorf("unquoted shell pathname expansion requires review")
			}
			if len(word.Parts) > 0 {
				if literal, ok := word.Parts[0].(*syntax.Lit); ok && strings.HasPrefix(literal.Value, "~") {
					expansionErr = fmt.Errorf("shell tilde expansion requires review")
				}
			}
		}
		if parameter, ok := node.(*syntax.ParamExp); ok {
			value, exists := env[parameter.Param.Value]
			if !exists || parameter.Index != nil || parameter.Slice != nil || parameter.Repl != nil || parameter.Exp != nil || parameter.Excl || parameter.Length || strings.ContainsAny(value, " \t\n*?[") {
				expansionErr = fmt.Errorf("CI parameter %s requires explicit environment review", parameter.Param.Value)
			}
		}
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ArithmExp:
			expansionErr = fmt.Errorf("dynamic CI shell setup requires review")
		}
		return true
	})
	if expansionErr != nil {
		return step, env, expansionErr
	}
	pairs := []string{}
	for key, value := range env {
		pairs = append(pairs, key+"="+value)
	}
	var commands []string
	var visit func(*syntax.Stmt) bool
	visit = func(stmt *syntax.Stmt) bool {
		if len(stmt.Redirs) > 0 || stmt.Background || stmt.Negated || stmt.Coprocess {
			return false
		}
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
			return binary.Op == syntax.AndStmt && visit(binary.X) && visit(binary.Y)
		}
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
			return false
		}
		words, err := expand.Fields(&expand.Config{Env: expand.ListEnviron(pairs...)}, call.Args...)
		if err != nil {
			return false
		}
		commands = append(commands, shellquote.Join(words...))
		return true
	}
	var statements []string
	for _, stmt := range file.Stmts {
		commands = nil
		if !visit(stmt) {
			return step, env, fmt.Errorf("CI shell control flow requires review")
		}
		statements = append(statements, strings.Join(commands, " && "))
	}
	// Independent statements must stay independent. Replacing ; or a newline
	// with && changes whether later tests run after a failed preparation.
	step.Run = strings.Join(statements, "\n")
	return nativeInlineControls(step, env, workflow.Provider)
}

func localPreparation(root, directory string, words []string) (BuildCommand, bool) {
	if len(words) == 0 {
		return BuildCommand{}, false
	}
	var kind PreparationKind
	if len(words) >= 4 && words[0] == "pnpm" && words[1] == "add" && (words[2] == "-D" || words[2] == "--save-dev") {
		for _, dependency := range words[3:] {
			name, version, ok := strings.Cut(dependency, "@")
			if strings.HasPrefix(dependency, "@") {
				name, version, ok = strings.Cut(dependency[1:], "@")
				name = "@" + name
			}
			if !ok || name == "" || version == "" || strings.ContainsAny(dependency, "$` :\\\n*?[") || strings.HasPrefix(name, "-") || strings.Contains(version, "/") {
				return BuildCommand{}, false
			}
		}
		kind = "dependencies"
	}
	if npmDependencyPreparation(words) {
		kind = PreparationDependencies
	}
	if len(words) == 4 && words[0] == "sed" && strings.HasPrefix(words[1], "-i") && !strings.ContainsAny(words[1], "/\\") {
		script := strings.TrimPrefix(words[2], "1")
		if literalSedSubstitution(script) && filepath.IsLocal(words[3]) && !strings.HasPrefix(words[3], "-") && !strings.ContainsAny(words[3], "$`\n:") {
			kind = "patch"
		}
	}
	if kind == "" {
		return BuildCommand{}, false
	}
	relative := repositoryPath(root, directory)
	return BuildCommand{Directory: filepath.ToSlash(relative), Command: shellquote.Join(words...), Kind: kind}, true
}

// Recognize pnpm's bounded build-script selector while keeping the original
// command so its ordering, prebuild and postbuild behavior are replayed intact.
func orchestratedBuild(root, directory, script string, scripts map[string]string, stack []string, remaining *int, frameworks ...string) bool {
	commands, err := staticCommands(script)
	if err != nil {
		return false
	}
	selected := false
	for _, words := range commands {
		if len(words) != 3 || words[0] != "pnpm" || words[1] != "run" {
			return false
		}
		name := words[2]
		if name == "prebuild" || name == "postbuild" {
			if scripts[name] == "" {
				return false
			}
			continue
		}
		if len(name) < 3 || name[0] != '/' || name[len(name)-1] != '/' || scripts[name] != "" {
			return false
		}
		pattern, err := regexp.Compile(name[1 : len(name)-1])
		if err != nil {
			return false
		}
		count := 0
		for candidate, body := range scripts {
			if !pattern.MatchString(candidate) {
				continue
			}
			if !strings.HasPrefix(candidate, "build:") {
				return false
			}
			resolution := resolveJestSequence(root, directory, body, stack, remaining, frameworks...)
			if resolution.Matched || resolution.Reason != "" || len(resolution.Builds) == 0 {
				return false
			}
			count++
		}
		if count == 0 {
			return false
		}
		selected = true
	}
	return selected
}

// EqualPreparation compares executable preparation, including its step-local environment.
func EqualPreparation(a, b BuildCommand) bool {
	return a.Kind == b.Kind && a.Directory == b.Directory && a.Command == b.Command && maps.Equal(a.Environment, b.Environment)
}

var npmDependencyName = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var npmPinnedDependency = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*@[0-9]+\.[0-9]+\.[0-9]+$`)

// Record literal matrix dependency variants as reviewed preparation. They run
// only in isolated per-configuration workspaces; never mutate the source tree.
func npmDependencyPreparation(words []string) bool {
	if len(words) < 3 || words[0] != "npm" || (!slices.Contains(words, "--save-dev") && !slices.Contains(words, "-D")) || !slices.Contains([]string{"install", "i", "rm", "remove", "uninstall"}, words[1]) {
		return false
	}
	args := words[2:]
	for len(args) > 0 && slices.Contains([]string{"--silent", "--save-dev", "-D"}, args[0]) {
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if words[1] == "install" || words[1] == "i" {
			if !npmPinnedDependency.MatchString(arg) {
				return false
			}
		} else if !npmDependencyName.MatchString(arg) {
			return false
		}
	}
	return true
}

// TracerPluginSetting reports whether a DD_* variable selects which tracer
// integrations load, such as disabling a plugin that conflicts with the
// project. These are customer behavior, not intake or session configuration.
func TracerPluginSetting(key string) bool {
	switch key {
	case "DD_TRACE_DISABLED_INSTRUMENTATIONS", "DD_TRACE_DISABLED_PLUGINS":
		return true
	}
	name, ok := strings.CutPrefix(key, "DD_TRACE_")
	plugin, enabled := strings.CutSuffix(name, "_ENABLED")
	// DD_TRACE_ENABLED itself switches the whole tracer; local runs control it.
	return ok && enabled && plugin != ""
}

// remoteGridProviders maps credential variable prefixes to remote browser
// grids whose sessions run outside the local machine.
var remoteGridProviders = []struct{ prefix, name string }{
	{"SAUCE_", "Sauce Labs"},
	{"BROWSERSTACK_", "BrowserStack"},
	{"LT_", "LambdaTest"},
	{"LAMBDATEST_", "LambdaTest"},
	{"TESTINGBOT_", "TestingBot"},
}

var secretReference = regexp.MustCompile(`^\$\{\{\s*secrets\.[A-Za-z_][A-Za-z0-9_]*\s*\}\}$`)

// remoteGridSecret returns the remote browser grid a CI variable authenticates
// to, when its value is only a secret reference, or "".
func remoteGridSecret(key, value string) string {
	if !secretReference.MatchString(strings.TrimSpace(value)) {
		return ""
	}
	for _, provider := range remoteGridProviders {
		if strings.HasPrefix(key, provider.prefix) {
			return provider.name
		}
	}
	return ""
}
