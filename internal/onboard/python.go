package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
)

// PythonRelease describes the exact PyPI release checked locally and in CI.
type PythonRelease struct {
	Version        string `json:"version"`
	RequiresPython string `json:"requires_python"`
}

func ResolvePythonRelease(ctx context.Context, requested string) (PythonRelease, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	address := "https://pypi.org/pypi/ddtrace/"
	if requested != "" && requested != "latest" {
		address += url.PathEscape(requested) + "/"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"json", nil)
	if err != nil {
		return PythonRelease{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return PythonRelease{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return PythonRelease{}, fmt.Errorf("read ddtrace metadata: HTTP %d", response.StatusCode)
	}
	return readPythonRelease(io.LimitReader(response.Body, 1<<20))
}

// The project endpoint includes a large releases history after info. Stop as
// soon as the bounded package metadata is decoded, without reading that history.
func readPythonRelease(reader io.Reader) (PythonRelease, error) {
	decoder := json.NewDecoder(reader)
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return PythonRelease{}, fmt.Errorf("invalid PyPI package metadata")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return PythonRelease{}, err
		}
		if key == "info" {
			var info struct {
				Version        string `json:"version"`
				RequiresPython string `json:"requires_python"`
			}
			if err := decoder.Decode(&info); err != nil {
				return PythonRelease{}, err
			}
			if info.Version == "" || info.RequiresPython == "" {
				return PythonRelease{}, fmt.Errorf("ddtrace metadata has no version or Python requirement")
			}
			return PythonRelease{info.Version, info.RequiresPython}, nil
		}
		var ignored json.RawMessage
		if err := decoder.Decode(&ignored); err != nil {
			return PythonRelease{}, err
		}
	}
	return PythonRelease{}, fmt.Errorf("PyPI package metadata has no info")
}

func pythonJobCandidate(root string, workflow ciWorkflow, job runtimeJob) bool {
	rows, err := runtimeMatrix(job.Strategy.Matrix)
	if err != nil {
		// Inspect only referenced command values to identify relevant jobs. This
		// does not expand unsupported matrices or authorize their execution.
		for _, step := range job.Steps {
			if pythonMatrixStepCandidate(root, workflow, job, step, job.Strategy.Matrix) {
				return true
			}
		}
		rows = []map[string]any{nil}
	}
	for _, row := range rows {
		for _, step := range job.Steps {
			resolved, _, err := resolveVariantStep(workflow, job, step, row, "pytest")
			if err != nil && pythonMatrixStepCandidate(root, workflow, job, step, row) {
				return true
			}
			if pythonStepCandidate(root, workflow, job, resolved) {
				return true
			}
		}
	}
	return false
}

func pythonMatrixStepCandidate(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, matrix any) bool {
	for _, match := range pythonMatrixCommandReference.FindAllStringSubmatch(step.Run, -1) {
		for _, value := range pythonMatrixCommandValues(matrix, strings.Split(match[1], ".")) {
			candidate := step
			candidate.Run = strings.ReplaceAll(step.Run, match[0], value)
			if pythonStepCandidate(root, workflow, job, candidate) {
				return true
			}
		}
	}
	return false
}

func pythonStepCandidate(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	resolution := resolvePythonStep(root, workflow, job, step)
	if resolution.Matched {
		return true
	}
	if resolution.Review || resolution.Reason == "" {
		return false
	}
	if environment.LooksLikeTestCommand(root, step.Run, "python", "pytest") || pythonTestReference.MatchString(step.Run) {
		return true // Conditional shell code remains unresolved, never replayed.
	}
	commands, _ := staticCommands(step.Run)
	for _, words := range commands {
		if _, ok := pythonRunnerArguments(words, "tox"); ok {
			return true // Unknown tox configuration is not an unrelated typing/lint job.
		}
		for i, word := range words {
			if i != 0 && (len(words) < 2 || words[1] != "run" || !slices.Contains([]string{"uv", "poetry"}, filepath.Base(words[0]))) {
				break
			}
			if index, module := PythonModule(words[i+1:]); strings.HasPrefix(filepath.Base(word), "python") && index >= 0 && slices.Contains([]string{"pytest", "tox"}, module[0]) {
				return true // Retain interpreter-like wrappers as unresolved, never native commands.
			}
		}
	}
	return false
}

var pythonMatrixCommandReference = regexp.MustCompile(`\$\{\{\s*matrix\.([a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]+)*)\s*\}\}`)

var pythonTestReference = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_])(?:pytest|tox)(?:[^a-zA-Z0-9_]|$)|-m(?:pytest|tox)(?:[^a-zA-Z0-9_]|$)`)

func pythonMatrixCommandValues(value any, path []string) []string {
	if len(path) == 0 {
		if text, ok := value.(string); ok {
			return []string{text}
		}
		return nil
	}
	var values []string
	switch value := value.(type) {
	case map[string]any:
		values = pythonMatrixCommandValues(value[path[0]], path[1:])
		values = append(values, pythonMatrixCommandValues(value["include"], path)...)
	case []any:
		for _, item := range value {
			values = append(values, pythonMatrixCommandValues(item, path)...)
		}
	}
	return values
}

// Restrict static checks to numeric minimum/maximum constraints. Other PEP 440
// expressions remain inconclusive rather than silently dropping a constraint.
func ComparePythonRequirement(python, requirement string) (string, string) {
	if strings.ContainsAny(requirement, "*~") {
		return "inconclusive", "The Python version requirement needs manual review."
	}
	version := strings.Split(python, ".")
	if len(version) < 2 || len(version) > 3 {
		return "inconclusive", "The Python runtime must select a numeric major.minor or major.minor.patch version."
	}
	for _, part := range version {
		if _, err := strconv.Atoi(part); err != nil {
			return "inconclusive", "The Python runtime must select a numeric major.minor or major.minor.patch version."
		}
	}
	constraints := strings.Split(requirement, ",")
	for i, constraint := range constraints {
		if !pythonNumericRequirement.MatchString(constraint) {
			return "inconclusive", "The Python version requirement needs manual review."
		}
		bound := pythonRequirementVersion.FindString(constraint)
		parts := strings.Split(bound, ".")
		for len(parts) < 3 {
			parts = append(parts, "0")
		}
		constraints[i] = strings.Replace(constraint, bound, strings.Join(parts, "."), 1)
	}
	normalized := strings.Join(constraints, " ")
	status, reason := CompareNodeRequirement(python, normalized)
	if len(version) == 2 && status != "inconclusive" {
		// A minor selector covers every patch, not just patch zero. Check each
		// patch boundary in the requirement and the first patch after it.
		patches := []uint64{1}
		for _, bound := range pythonRequirementVersion.FindAllString(requirement, -1) {
			parts := strings.Split(bound, ".")
			if len(parts) == 3 && parts[0] == version[0] && parts[1] == version[1] {
				patch, err := strconv.ParseUint(parts[2], 10, 64)
				if err != nil || patch == ^uint64(0) {
					return "inconclusive", "The Python patch requirement needs manual review."
				}
				patches = append(patches, patch, patch+1)
			}
		}
		for _, patch := range patches {
			candidate, _ := CompareNodeRequirement(fmt.Sprintf("%s.%d", python, patch), normalized)
			if candidate != status {
				return "inconclusive", "The Python selector " + python + " does not select a patch; compatibility depends on the resolved patch version."
			}
		}
	}
	return status, strings.NewReplacer("Node", "Python", "setup-node", "setup-python").Replace(reason)
}

var pythonRequirementVersion = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){0,2}`)

var pythonNumericRequirement = regexp.MustCompile(`^\s*(?:[<>]=?|==|!=)\s*[0-9]+(?:\.[0-9]+){0,2}\s*$`)

func pythonCommandEnvironment(words []string) ([]string, map[string]string, error) {
	words, env, err := commandEnvironment(words)
	if err != nil {
		return nil, nil, err
	}
	for _, key := range []string{"PYTHONPATH", "PYTHONHOME"} {
		if _, exists := env[key]; exists {
			return nil, nil, fmt.Errorf("command overrides the Python runtime or tracer path: %s", key)
		}
	}
	return words, env, nil
}

func pythonInvocation(words []string) bool {
	args, ok := pythonRunnerArguments(words, "pytest")
	return ok && !pythonMetadataQuery(args)
}

// PytestArguments returns only pytest's arguments, preserving the supported
// wrapper and interpreter boundaries for controlled local execution.
func PytestArguments(words []string) ([]string, bool) {
	return pythonRunnerArguments(words, "pytest")
}

func pythonMetadataQuery(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "--version", "--help", "-h", "-V", "--collect-only", "--co":
			return true
		case "-k", "-m", "-o", "-p", "-c", "--override-ini", "--config-file", "--rootdir":
			i++ // A value is not a pytest metadata flag.
		}
	}
	return false
}

func pythonPathReference(value string) bool {
	return strings.Join(strings.Fields(value), "") == "${{env.PYTHONPATH}}"
}

// Replace the action's CI-only path, and the tracer path a provider's setup
// adds, with the application's declared paths. Local validation supplies its
// own tracer path. Empty reference expansions
// must not introduce a new current-directory component into PYTHONPATH.
func pythonApplicationPath(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) string {
	separator := ":"
	if runnerDefaultShell(job, row) == "pwsh" {
		separator = ";"
	}
	var result string
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		value, declared := layer["PYTHONPATH"]
		if !declared {
			continue
		}
		var parts []string
		afterTracer := false
		for _, part := range strings.Split(value, separator) {
			switch {
			case pythonPathReference(part):
				if result != "" {
					parts = append(parts, result)
				}
			case part == pythonTracerPath, part == "" && afterTracer:
				// The installer's or CI plugin's tracer path, and the empty
				// entry the plugin's trailing separator adds, are CI's own.
			default:
				parts = append(parts, part)
			}
			afterTracer = part == pythonTracerPath
		}
		result = strings.Join(parts, separator)
	}
	return result
}

// PythonModule identifies -m among interpreter options, returning its index and
// the module followed by its arguments. Attached -mmodule is normalized.
// Parsing stops at a script, -c, stdin or --; later -m belongs to that target.
// Unknown interpreter options remain unresolved rather than being skipped.
func PythonModule(args []string) (int, []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-m":
			if i+1 < len(args) {
				return i, args[i+1:]
			}
			return -1, nil
		case strings.HasPrefix(arg, "-m"):
			return i, append([]string{arg[2:]}, args[i+1:]...)
		case arg == "-W" || arg == "-X" || arg == "--check-hash-based-pycs":
			i++
			if i >= len(args) {
				return -1, nil
			}
		case strings.HasPrefix(arg, "-W") || strings.HasPrefix(arg, "-X"):
		case len(arg) > 1 && arg[0] == '-' && strings.Trim(arg[1:], "bBdEiIOPqRsSuvx") == "":
		default:
			return -1, nil
		}
	}
	return -1, nil
}

func pythonRunnerArguments(words []string, runner string) ([]string, bool) {
	if len(words) == 0 {
		return nil, false
	}
	base := strings.TrimSuffix(filepath.Base(words[0]), ".exe")
	if base == runner {
		return words[1:], true
	}
	if platform.IsPythonExecutable(words[0]) {
		index, module := PythonModule(words[1:])
		if index >= 0 && module[0] == runner {
			return module[1:], true
		}
		return nil, false
	}
	if (base == "uv" || base == "poetry") && len(words) > 2 && words[1] == "run" {
		for i := 2; i < len(words); i++ {
			arg := words[i]
			if args, ok := pythonRunnerArguments(words[i:], runner); ok {
				return args, true
			}
			option, _, assigned := strings.Cut(arg, "=")
			switch option {
			case "--no-sync", "--locked", "--frozen", "--offline", "--active", "--no-python-downloads", "--no-default-groups", "--all-groups", "--no-dev", "--only-dev", "--all-extras", "--":
			case "--project", "--directory", "--package", "--python", "-p", "--group", "--no-group", "--only-group", "--extra", "--no-extra":
				if !assigned {
					i++
				}
			default:
				return nil, false
			}
		}
	}
	return nil, false
}

// ErrPytestRuntimeValidation marks options the static checker cannot certify.
// A local paired run can resolve them through pytest itself.
var ErrPytestRuntimeValidation = errors.New("pytest options require runtime validation")

// PytestCommandOptions inspects environment and command options, including the
// last inline addopts override (pytest's -o/--override-ini precedence). defaults
// contains the selected config file's addopts. Wrapper separators and interpreter
// options are not pytest arguments. iniAddopts is
// returned separately so controlled local runs can reject nested disable flags
// while still replacing direct tracer flags in their paired executions.
func PytestCommandOptions(addopts string, words, defaults []string) (options, iniAddopts []string, err error) {
	args, ok := pythonRunnerArguments(words, "pytest")
	if !ok {
		return nil, nil, fmt.Errorf("cannot identify pytest arguments in %q", words)
	}
	options, err = shellquote.Split(addopts)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot parse PYTEST_ADDOPTS: %w", err)
	}
	options = append(options, args...)
	if slices.ContainsFunc(options, func(option string) bool { return strings.HasPrefix(option, "@") }) {
		return nil, nil, fmt.Errorf("%w: argument files need separate inspection", ErrPytestRuntimeValidation)
	}
	var override string
	overridden := false
	for i := 0; i < len(options); i++ {
		option := options[i]
		if option == "--" {
			options = options[:i]
			break
		}
		var value string
		switch {
		case option == "-o" || option == "--override-ini":
			i++
			if i >= len(options) {
				return nil, nil, fmt.Errorf("%s requires an ini override", option)
			}
			value = options[i]
		case strings.HasPrefix(option, "--override-ini="):
			value = strings.TrimPrefix(option, "--override-ini=")
		case strings.HasPrefix(option, "-o"):
			value = strings.TrimPrefix(strings.TrimPrefix(option, "-o"), "=")
		default:
			continue
		}
		key, value, _ := strings.Cut(value, "=")
		if key == "ddtrace" || key == "no-ddtrace" {
			return nil, nil, fmt.Errorf("%w: config-based Datadog activation", ErrPytestRuntimeValidation)
		}
		if key == "addopts" {
			override, overridden = value, true
		}
	}
	iniAddopts = defaults
	if overridden {
		iniAddopts, err = shellquote.Split(override)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot parse inline pytest addopts: %w", err)
		}
	}
	if slices.ContainsFunc(iniAddopts, func(option string) bool { return strings.HasPrefix(option, "@") }) {
		return nil, nil, fmt.Errorf("%w: argument files need separate inspection", ErrPytestRuntimeValidation)
	}
	return append(slices.Clone(iniAddopts), options...), iniAddopts, nil
}

// A setup-python version does not prove the interpreter chosen by a wrapper or
// a different versioned executable. Keep those commands unverified in static CI
// analysis; local preflight inspects the environment that actually executes.
func pythonRuntimeOverride(words []string, python string, environment map[string]string) bool {
	if len(words) == 0 {
		return false
	}
	if environment["PYTHONHOME"] != "" {
		return true
	}
	base := strings.TrimSuffix(filepath.Base(words[0]), ".exe")
	if base == "pytest" && strings.ContainsAny(words[0], "/\\") {
		return true
	}
	if platform.IsPythonExecutable(words[0]) {
		return strings.ContainsAny(words[0], "/\\") || base != "python" && base != "python3" && !strings.HasPrefix("python"+python+".", base+".")
	}
	if (base == "uv" || base == "poetry") && len(words) > 2 && words[1] == "run" {
		if base == "uv" && (environment["UV_PYTHON"] != "" || environment["UV_PROJECT_ENVIRONMENT"] != "") {
			return true
		}
		for i := 2; i < len(words); i++ {
			if pythonInvocation(words[i:]) {
				return true
			}
			if _, ok := pythonRunnerArguments(words[i:], "tox"); ok {
				return false
			}
			option, _, assigned := strings.Cut(words[i], "=")
			if option == "--python" || option == "-p" || option == "--project" || option == "--directory" {
				return true
			}
			if !assigned && slices.Contains([]string{"--package", "--group", "--no-group", "--only-group", "--extra", "--no-extra"}, option) {
				i++
			}
		}
	}
	return false
}

// Do not inspect the checkout's tox configuration when a wrapper selects a
// different project. Discovery retains that invocation as unresolved.
func pythonWrapperChangesDirectory(words []string) bool {
	if len(words) < 3 || words[1] != "run" || !slices.Contains([]string{"uv", "poetry"}, filepath.Base(words[0])) {
		return false
	}
	for i := 2; i < len(words); i++ {
		if _, ok := pythonRunnerArguments(words[i:], "tox"); ok {
			return false
		}
		option, _, assigned := strings.Cut(words[i], "=")
		if option == "--directory" || option == "--project" {
			return true
		}
		if !assigned && slices.Contains([]string{"--python", "-p", "--package", "--group", "--no-group", "--only-group", "--extra", "--no-extra"}, option) {
			i++
		}
	}
	return false
}

func pythonIgnoresEnvironment(words []string) bool {
	if len(words) == 0 || !platform.IsPythonExecutable(words[0]) {
		return false
	}
	index, _ := PythonModule(words[1:])
	for i := 1; i <= index; i++ {
		switch option := words[i]; {
		case option == "-W" || option == "-X" || option == "--check-hash-based-pycs":
			i++
		case strings.HasPrefix(option, "-W") || strings.HasPrefix(option, "-X"):
		case strings.HasPrefix(option, "-") && strings.ContainsAny(option[1:], "EI"):
			return true
		}
	}
	return false
}

// pythonNonTestTools are linters, type checkers, formatters, builders and
// uploaders Python CI runs beside pytest, which never start it.
var pythonNonTestTools = []string{"flake8", "black", "isort", "mypy", "ruff", "pylint", "bandit", "safety", "pyright", "pydocstyle", "pre-commit", "twine", "sphinx-build", "mkdocs", "codecov", "coveralls", "check-manifest", "pip-audit", "build"}

// pythonSetupCommands are setup.py commands that build or check a
// distribution; setup.py test runs tests.
var pythonSetupCommands = []string{"sdist", "bdist", "bdist_wheel", "bdist_egg", "build", "build_py", "build_ext", "egg_info", "check"}

// pythonNonTestTool reports a command that runs one of those tools, or
// coverage's reporting commands, which read results a test run left.
func pythonNonTestTool(words []string) bool {
	if len(words) == 0 {
		return false
	}
	if platform.IsPythonExecutable(words[0]) && len(words) > 2 && filepath.Base(words[1]) == "setup.py" {
		return !slices.ContainsFunc(words[2:], func(command string) bool {
			return !strings.HasPrefix(command, "-") && !slices.Contains(pythonSetupCommands, command)
		})
	}
	name := filepath.Base(words[0])
	if platform.IsPythonExecutable(words[0]) {
		index, module := PythonModule(words[1:])
		if index < 0 || len(module) == 0 {
			return false
		}
		name, words = module[0], module
	}
	if name == "coverage" {
		return len(words) > 1 && slices.Contains([]string{"report", "xml", "html", "json", "lcov", "combine", "erase", "debug"}, words[1])
	}
	return slices.Contains(pythonNonTestTools, name)
}

// pythonStaticCommands is staticCommands, except that a dependency
// installation may name extras (pip install .[dev]), an unquoted bracket the
// shell leaves as written unless a file matches it, and a tool that runs no
// tests may take patterns (twine upload dist/*): neither is replayed.
func pythonStaticCommands(command string) ([][]string, error) {
	fragments, err := discoverCommands(command)
	if err != nil {
		return nil, err
	}
	var commands [][]string
	for _, fragment := range fragments {
		if fragment.Reason != "" {
			return nil, fmt.Errorf("%s", fragment.Reason)
		}
		if len(fragment.Patterns) > 0 && !pythonInstallation(fragment.Words) && !pythonNonTestTool(fragment.Words) {
			return nil, fmt.Errorf("%s", "Unvalidated setup or command with dynamic shell syntax: "+fragment.Text)
		}
		commands = append(commands, fragment.Words)
	}
	return commands, nil
}

// pythonInstallation reports a pip or uv pip command that installs
// dependencies.
func pythonInstallation(words []string) bool {
	words, _, err := commandEnvironment(words)
	if err != nil || len(words) < 2 {
		return false
	}
	switch base := filepath.Base(words[0]); {
	case base == "pip" || base == "pip3":
		return words[1] == "install"
	case base == "uv":
		return len(words) > 2 && words[1] == "pip" && words[2] == "install"
	case platform.IsPythonExecutable(words[0]):
		index, module := PythonModule(words[1:])
		return index >= 0 && len(module) > 1 && module[0] == "pip" && module[1] == "install"
	}
	return false
}

func resolvePythonStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) commandResolution {
	if step.ResolutionError != "" {
		return unresolvedCommand(step.ResolutionError)
	}
	if strings.TrimSpace(step.Run) == "" {
		return commandResolution{}
	}
	// Activating a virtual environment selects where later commands run;
	// the CI check follows which Python it runs (see pythonVenvs).
	run, activated := pythonWithoutActivations(step.Run)
	if len(activated) > 0 && strings.TrimSpace(run) == "" {
		return commandResolution{Review: true, Reason: pythonActivationReview}
	}
	commands, err := pythonStaticCommands(run)
	if err != nil {
		return unresolvedCommand("Python shell command requires review: " + err.Error())
	}
	directory := stepDirectory(workflow, job, step)
	if directory == "" {
		directory = "."
	}
	if !filepath.IsLocal(directory) || strings.ContainsAny(directory, "$`~") {
		return unresolvedCommand("Python working-directory requires review: " + directory)
	}
	shell := workflow.Defaults.Run.Shell
	if job.Defaults.Run.Shell != "" {
		shell = job.Defaults.Run.Shell
	}
	if step.Shell != "" {
		shell = step.Shell
	}
	// A literal invocation has the same argument boundaries in PowerShell.
	// Quoting, expansions and shell operators still require separate review.
	literalPowerShell := shell == "pwsh" && len(commands) == 1 && shellMatrixScalar.MatchString(step.Run)
	if !supportedCIShell(shell) && !literalPowerShell {
		return unresolvedCommand("Unsupported Python CI shell: " + shell)
	}
	result := commandResolution{}
	for _, words := range commands {
		if len(words) == 0 {
			continue
		}
		words, assignments, err := pythonCommandEnvironment(words)
		if err != nil {
			return unresolvedCommand(err.Error())
		}
		if literalPowerShell && len(assignments) > 0 {
			return unresolvedCommand("POSIX environment assignments are not supported in PowerShell.")
		}
		if args, ok := pythonRunnerArguments(words, "pytest"); ok && pythonMetadataQuery(args) {
			result.ReviewReason = joinDiagnostic(result.ReviewReason, "pytest metadata query does not run tests.")
			continue
		}
		if pythonInvocation(words) {
			result.Matched = true
			result.Tests = append(result.Tests, TestCommand{Directory: filepath.ToSlash(filepath.Clean(directory)), Command: shellquote.Join(words...), Environment: assignments})
			continue
		}
		if args, ok := pythonRunnerArguments(words, "tox"); ok {
			if pythonWrapperChangesDirectory(words) {
				return unresolvedCommand("The wrapper selects a different tox project directory; inspect that environment separately.")
			}
			tox, err := readPythonTox(filepath.Join(root, directory), args)
			if err != nil {
				return unresolvedCommand("Cannot resolve tox pytest environment: " + err.Error())
			}
			if len(tox.Commands) == 0 {
				result.ReviewReason = joinDiagnostic(result.ReviewReason, "Selected tox environment does not run pytest.")
				continue
			}
			result.Matched = true
			const unresolvedTox = "Local tox execution is not supported; activate the selected environment and pass its Python/pytest command with --command."
			result.Setup = append(result.Setup, unresolvedTox)
			result.Tests = append(result.Tests, TestCommand{Directory: filepath.ToSlash(filepath.Clean(directory)), Command: shellquote.Join(words...), Environment: assignments, Resolution: "Native tox pytest environment " + tox.Name,
				UnvalidatedSetup: []string{unresolvedTox}})
			continue
		}
		base := filepath.Base(words[0])
		if len(words) == 2 && words[0] == "cd" && filepath.IsLocal(words[1]) {
			directory = filepath.Join(directory, words[1])
			continue
		}
		setup := base == "pip" || base == "pip3" || base == "virtualenv" || (base == "uv" && len(words) > 1 && slices.Contains([]string{"sync", "pip", "venv"}, words[1]))
		if platform.IsPythonExecutable(words[0]) {
			if index, module := PythonModule(words[1:]); index >= 0 && slices.Contains([]string{"pip", "venv"}, module[0]) {
				setup = true
			}
		}
		if setup {
			result.ReviewReason = "Prepare the same Python dependencies as CI before local validation; dependency installation is not replayed."
			continue
		}
		if platform.IsPythonExecutable(words[0]) && len(words) == 2 && slices.Contains([]string{"--version", "-V", "-VV"}, words[1]) {
			result.ReviewReason = joinDiagnostic(result.ReviewReason, "Prints the Python version; it runs no tests.")
			continue
		}
		if checkOnlyStep(shellquote.Join(words...)) {
			continue
		}
		if pythonNonTestTool(words) {
			result.ReviewReason = joinDiagnostic(result.ReviewReason, words[0]+" runs no tests; it is outside this validation.")
			continue
		}
		if !environment.LooksLikeTestCommand(root, shellquote.Join(words...), "python", "pytest") && (slices.Contains(words, "tox") || slices.Contains(words, "nox")) {
			return commandResolution{Review: true, Reason: "This wrapper does not identify a pytest test environment; review it separately."}
		}
		return unresolvedCommand("Unresolved Python entry point: " + shellquote.Join(words...) + ". For tox/nox or other wrappers, activate the selected test environment and pass its Python/pytest command explicitly.")
	}
	if !result.Matched {
		result.Review, result.Reason = true, result.ReviewReason
	}
	return result
}

// CheckPythonCIRuntimes inventories every static matrix entry independently of
// the evaluator's Python. It never executes workflows or installs dependencies.
func CheckPythonCIRuntimes(ctx context.Context, root, localVersion string) RuntimeCheck {
	return CheckPythonCIRuntimesForSite(ctx, root, localVersion, "")
}

// CheckPythonCIRuntimesForSite is CheckPythonCIRuntimes that also requires,
// outside GitHub Actions, each instrumented entry's setup to report to the
// selected site (empty: any site).
func CheckPythonCIRuntimesForSite(ctx context.Context, root, localVersion, site string) RuntimeCheck {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkPythonCIRuntimes(ctx, root, localVersion, site, ResolvePythonRelease, pythonActionVersion)
}

func pythonActionVersion(ctx context.Context, action string) (string, error) {
	_, ref, ok := strings.Cut(action, "@")
	if !ok || ref == "" {
		return "", fmt.Errorf("datadog action has no ref")
	}
	data, err := fetchRuntimeMetadata(ctx, &http.Client{Timeout: 10 * time.Second}, "https://raw.githubusercontent.com/DataDog/test-visibility-github-action/"+url.PathEscape(ref)+"/action.yml")
	if err != nil {
		return "", err
	}
	var definition struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
	}
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return "", err
	}
	return definition.Inputs["python-tracer-version"].Default, nil
}

// pythonProviderSetup is what a provider's setup (outside GitHub Actions)
// selects for Python on one matrix row: its step, its inputs resolved for the
// row, and the requested ddtrace release ("latest" for a setup that cannot
// pin one).
type pythonProviderSetup struct {
	setup     ciSetup
	inputs    map[string]string
	requested string
}

func checkPythonCIRuntimes(ctx context.Context, root, localVersion, site string, resolve func(context.Context, string) (PythonRelease, error), actionDefault func(context.Context, string) (string, error)) RuntimeCheck {
	if err := CheckCIProviderSupport(root, "python", "pytest"); err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	provider, err := currentCIProvider(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	result := RuntimeCheck{Status: "not applicable", Reason: "No " + provider.displayName() + " pytest commands found; CI was not exercised."}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	shared := provider.sharedSyntax()
	cache := map[string]PythonRelease{}
	selectRelease := func(requested string) (PythonRelease, error) {
		if selection, ok := cache[requested]; ok {
			return selection, nil
		}
		selection, err := resolve(ctx, requested)
		if err == nil {
			cache[requested] = selection
		}
		return selection, err
	}
	defaults := map[string]string{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if !pythonJobCandidate(root, workflow, job) {
				continue
			}
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive", Reason: err.Error()})
				continue
			}
			for _, row := range rows {
				active, conditionErr := possibleJobCondition(job.If, row)
				if conditionErr == nil && !active {
					continue
				}
				var venvs pythonVenvs
				var python, action, requested, engine, host string
				var pythonErr, actionErr error
				var selected *pythonProviderSetup
				skipped, stated := false, false
				if !shared {
					// Outside GitHub Actions the job's image selects its Python,
					// or the user states its host's.
					python, engine = pythonImageRuntime(job.Image)
					if pythonImageSelection(job) == "" {
						host = runtimeHost(job, row, "Python")
						if version := job.Stated["python"]; host != "" && version != "" {
							python, stated = version, true
						}
					}
				}
				for i, step := range job.Steps {
					uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
					current, isSetup := datadogSetup(step)
					isSetup = isSetup && !shared
					active, stepErr := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
					if stepErr == nil && !active {
						if chosen, err := setupSelectsLanguage(current, row, "python"); isSetup && current.Pinned && err == nil && chosen {
							skipped = true
						}
						continue
					}
					if uses == "actions/setup-python" {
						action, requested = "", ""
						actionErr = nil
						python, pythonErr = runtimeValue(step.With["python-version"], row)
						if update, err := runtimeValue(step.With["update-environment"], row); err != nil {
							pythonErr = err
						} else if update != "" && !strings.EqualFold(update, "true") {
							pythonErr = fmt.Errorf("setup-python does not establish the active Python environment; validate the runtime separately")
						}
						if stepErr != nil {
							pythonErr = stepErr
						}
					}
					if uses == githubAction {
						languages, e := runtimeValue(step.With["languages"], row)
						if e != nil {
							actionErr = e
						}
						if languages == "all" || slices.Contains(strings.Fields(languages), "python") {
							action = step.Uses
							actionErr = stepErr
							requested, err = runtimeValue(step.With["python-tracer-version"], row)
							if err == nil && requested == "" {
								var known bool
								requested, known = defaults[action]
								if !known {
									requested, err = actionDefault(ctx, action)
									if err == nil {
										defaults[action] = requested
									}
								}
							}
							if err != nil {
								actionErr = err
							}
						}
					}
					if isSetup {
						// A setup for other languages leaves Python's in place.
						chosen, err := setupSelectsLanguage(current, row, "python")
						if err != nil || chosen {
							action, requested, actionErr, selected, skipped = "", "", err, nil, false
						}
						if err == nil && chosen && stepErr != nil {
							actionErr = stepErr
						}
						if err == nil && chosen && stepErr == nil && current.Pinned {
							action, selected = current.Reference, &pythonProviderSetup{setup: current, inputs: map[string]string{}}
							for key, value := range current.Inputs {
								selected.inputs[key], _ = runtimeValue(value, row)
								if selected.inputs[key] == "" && strings.Contains(value, "${{") {
									// A secret or other expression is set even
									// though its value is unknown statically.
									selected.inputs[key] = value
								}
							}
							requested = selected.inputs["python-tracer-version"]
							if _, set := current.Inputs["python-tracer-version"]; !set && current.Floating {
								requested = "latest"
							}
							selected.requested = requested
						}
					}
					resolved, environment, envErr := resolveVariantStep(workflow, job, step, row, "pytest")
					venvs.step(step.Run, python, stepDirectory(workflow, job, step), environment)
					r := resolvePythonStep(root, workflow, job, resolved)
					if !r.Matched && (r.Review || r.Reason == "" || checkOnlyStep(resolved.Run)) {
						continue
					}
					finding := RuntimeFinding{Location: (commandLocation{Workflow: workflow.Path, Job: name, Matrix: row, Step: stepNumber(step, i), Command: step.Run}).String(), Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Runtime: python, Action: action, Status: "inconclusive"}
					if !shared {
						finding.setupProvider = provider
						if host != "" {
							finding.RuntimeHost, finding.UserStated = host, stated
						}
					}
					commands, _ := staticCommands(resolved.Run)
					switch {
					case conditionErr != nil:
						finding.Reason = conditionErr.Error()
					case stepErr != nil:
						finding.Reason = stepErr.Error()
					case pythonErr != nil:
						finding.Reason = pythonErr.Error()
					case actionErr != nil:
						finding.Reason = actionErr.Error()
					case envErr != nil:
						finding.Reason = envErr.Error()
					case r.Reason != "":
						finding.Reason = r.Reason
					case action == "" && !shared && (skipped || engine != ""):
						finding.Status, finding.Code = "excluded", CodeExcludedRuntime
						finding.Reason = "The " + provider.setupName() + " is explicitly skipped for this matrix entry. Its tests still run without instrumentation; this runtime is not validated with the tracer."
						if !skipped {
							finding.Reason = "This job runs " + engine + " (image " + job.Image + "), which the tracer cannot instrument, without the " + provider.setupName() + ". Its tests still run without instrumentation; this runtime is not validated with the tracer."
						}
					case action == "" && !shared && python != "" && pythonUnsupportedRuntime(python, localVersion, selectRelease):
						selection, _ := selectRelease(localVersion)
						finding.Status, finding.Code = "excluded", CodeExcludedRuntime
						finding.Reason = "This job runs Python " + python + ", which ddtrace@" + selection.Version + " does not support (" + selection.RequiresPython + "), without the " + provider.setupName() + ". Its tests still run without instrumentation; this runtime is not validated with the tracer."
					case action == "":
						finding.Status, finding.Code, finding.Reason = "incompatible", "missing_instrumentation", pythonMissingSetupReason(provider)
					case !shared && engine != "":
						finding.Status = "incompatible"
						finding.Reason = "This job runs " + engine + " (image " + job.Image + "), which the tracer cannot instrument; keep its tests in a job without the " + provider.setupName() + "."
					case selected != nil && pythonSetupProblem(provider, selected.setup, selected.inputs, requested, site) != "":
						finding.Code, finding.Reason = CodeIncompleteCISetup, pythonSetupProblem(provider, selected.setup, selected.inputs, requested, site)
					case selected != nil && pythonConfirmationProblem(provider, selected.setup) != "":
						// Ask for every answer the user owes at once.
						finding.Code, finding.Reason = "ci_setup_confirmation_required", pythonConfirmationProblem(provider, selected.setup)
						if python == "" {
							finding.Reason += " " + pythonUnknownRuntimeReason(provider, job, host)
						}
					case !shared && python == "":
						finding.Reason = pythonUnknownRuntimeReason(provider, job, host)
					case venvs.reason() != "" && (selected == nil || !slices.Contains(selected.setup.Confirmations, jenkinsRuntimeMatches)):
						// The user confirmed that a plugin's toolchain is the
						// tests' Python, which covers the environment they run in.
						finding.Reason = venvs.reason()
					case slices.ContainsFunc(commands, func(words []string) bool {
						words, assignments, err := pythonCommandEnvironment(words)
						return err == nil && pythonRuntimeOverride(words, python, mergeEnvironment(environment, assignments))
					}):
						finding.Reason = "The CI command selects its own Python interpreter; setup-python does not establish that runtime's compatibility. Validate the selected environment separately."
						if !shared {
							finding.Reason = "The CI command selects its own Python interpreter; the job's Python does not establish that runtime's compatibility. Validate the selected environment separately."
						}
					default:
						selection, selectionErr := selectRelease(requested)
						if selectionErr != nil {
							finding.Reason = selectionErr.Error()
						} else {
							finding.Tracer = "ddtrace@" + selection.Version
							if selected != nil {
								finding.TracerRequested = requested
							}
							finding.Requirement = selection.RequiresPython
							finding.Status, finding.Reason = ComparePythonRequirement(python, selection.RequiresPython)
							if selected != nil && selected.setup.Floating {
								finding.TracerFloating = true
								finding.SetupCaveats = pythonFloatingCaveat(selected.setup, selection.Version)
							}
							if localVersion != "" && selection.Version != localVersion {
								pythonFindingProblem(&finding, "incompatible", fmt.Sprintf("CI selects ddtrace@%s; local validation selected ddtrace@%s. Align selections or validate the CI release separately.", selection.Version, localVersion))
							}
							plugin := ""
							if selected != nil && provider.setupTerms().Plugin {
								plugin = pythonPluginEnvironmentProblem(provider, workflow, job, step)
							}
							if plugin != "" {
								pythonFindingProblem(&finding, "incompatible", plugin)
							} else if value, ok := step.Env["PYTHONPATH"]; ok && !pythonKeepsTracerPath(value) {
								reason := "The test step replaces the action's PYTHONPATH; preserve the injected tracer package path."
								if !shared {
									reason = "This test command's PYTHONPATH replaces the one the " + provider.setupName() + " exported, which carries the tracer. Extend it instead, with " + provider.setupTerms().Persist(`export PYTHONPATH="$PYTHONPATH:<paths>"`) + "."
								}
								pythonFindingProblem(&finding, "incompatible", reason)
							}
							if status, reason := pythonInstrumentationOptions(root, workflow, job, resolved, row, python); status != "" {
								if !shared {
									reason = strings.ReplaceAll(reason, "the action's", "the "+provider.setupName()+"'s")
								}
								pythonFindingProblem(&finding, status, reason)
							}
						}
					}
					result.Jobs = append(result.Jobs, finding)
				}
			}
		}
	}
	if len(result.Jobs) > 0 {
		result.Status = "compatible"
		result.Reason = "Every identified pytest CI entry selects the validated ddtrace release and satisfies its Python requirement. CI execution and environment provisioning remain untested."
		for _, finding := range result.Jobs {
			if finding.Status != "compatible" && finding.Status != "excluded" {
				result.Status = finding.Status
				result.Reason = "Python CI configuration is incomplete or incompatible; inspect each entry and preserve the test matrix."
				if finding.Status == "incompatible" {
					break
				}
			}
		}
		if result.Status == "compatible" && !slices.ContainsFunc(result.Jobs, func(finding RuntimeFinding) bool { return finding.Status == "compatible" }) {
			result.Status, result.Reason = "incompatible", "Every identified pytest CI entry is excluded from instrumentation; at least one must run the Datadog setup."
		}
	}
	return result
}

// pythonUnsupportedRuntime reports a static Python the release local
// validation selected does not support, such as 3.15 for ddtrace 4.16.
func pythonUnsupportedRuntime(python, localVersion string, selectRelease func(string) (PythonRelease, error)) bool {
	if localVersion == "" {
		return false
	}
	selection, err := selectRelease(localVersion)
	if err != nil {
		return false
	}
	status, _ := ComparePythonRequirement(python, selection.RequiresPython)
	return status == "incompatible"
}

func pythonFindingProblem(finding *RuntimeFinding, status, reason string) {
	if finding.Status == "compatible" {
		finding.Reason = reason
	} else {
		finding.Reason = joinDiagnostic(finding.Reason, reason)
	}
	if finding.Status != "incompatible" {
		finding.Status = status
	}
}

func pythonInstrumentationOptions(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, python string) (string, string) {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	value, err := pythonAddopts(workflow, job, step, row)
	if err != nil {
		return "inconclusive", err.Error()
	}
	type invocation struct {
		options     []string
		environment map[string]string
	}
	var invocations []invocation
	commands, _ := staticCommands(step.Run)
	directory := stepDirectory(workflow, job, step)
	for _, words := range commands {
		words, assignments, err := pythonCommandEnvironment(words)
		if err != nil {
			return "inconclusive", err.Error()
		}
		commandEnv := mergeEnvironment(env, assignments)
		commandValue := value
		if override, exists := assignments["PYTEST_ADDOPTS"]; exists {
			commandValue = override
		}
		if len(words) == 2 && words[0] == "cd" && filepath.IsLocal(words[1]) {
			directory = filepath.Join(directory, words[1])
		}
		if pythonInvocation(words) {
			if pythonIgnoresEnvironment(words) {
				return "inconclusive", "The Python interpreter ignores PYTHONPATH; the action's tracer installation cannot be verified in this mode."
			}
			defaults, err := PytestConfigOptions(root, filepath.Join(root, directory), commandValue, words)
			if err != nil {
				return "inconclusive", err.Error()
			}
			commandOptions, _, err := PytestCommandOptions(commandValue, words, defaults)
			if err != nil {
				return "inconclusive", err.Error()
			}
			invocations = append(invocations, invocation{commandOptions, commandEnv})
		}
		if args, ok := pythonRunnerArguments(words, "tox"); ok {
			if pythonWrapperChangesDirectory(words) {
				return "inconclusive", "The wrapper selects a different tox project directory; inspect that environment separately."
			}
			if _, overridden := commandEnv["TOX_OVERRIDE"]; overridden {
				return "inconclusive", "TOX_OVERRIDE changes the effective tox configuration; inspect it inside the selected environment."
			}
			tox, err := readPythonTox(filepath.Join(root, directory), args)
			if err != nil {
				return "inconclusive", err.Error()
			}
			if len(tox.Commands) == 0 {
				continue
			}
			if status, reason := tox.runtime(python); status != "" {
				return status, reason
			}
			if status, reason := tox.instrumentation(); status != "" {
				return status, reason
			}
			effective := tox.environment(commandEnv)
			for _, command := range tox.Commands {
				if pythonIgnoresEnvironment(command) {
					return "inconclusive", "The tox Python interpreter ignores PYTHONPATH; the action's tracer installation cannot be verified in this mode."
				}
				if pythonRuntimeOverride(command, python, effective) {
					return "inconclusive", "The tox pytest command selects its own Python interpreter; validate the selected environment separately."
				}
				defaults, err := PytestConfigOptions(root, filepath.Join(root, directory), commandValue, command)
				if err != nil {
					return "inconclusive", err.Error()
				}
				commandOptions, _, err := PytestCommandOptions(commandValue, command, defaults)
				if err != nil {
					return "inconclusive", err.Error()
				}
				invocations = append(invocations, invocation{commandOptions, effective})
			}
		}
	}
	for _, invocation := range invocations {
		options := invocation.options
		enabled, err := runtimeValue(invocation.environment["DD_CIVISIBILITY_ENABLED"], row)
		if err != nil {
			return "inconclusive", err.Error()
		}
		if strings.EqualFold(enabled, "false") || enabled == "0" {
			return "incompatible", "DD_CIVISIBILITY_ENABLED explicitly disables Test Optimization reporting for this pytest invocation."
		}
		autoload, err := runtimeValue(invocation.environment["PYTEST_DISABLE_PLUGIN_AUTOLOAD"], row)
		if err != nil {
			return "inconclusive", err.Error()
		}
		if slices.Contains(options, "--no-ddtrace") {
			return "incompatible", "The pytest command or effective PYTEST_ADDOPTS explicitly disables Datadog instrumentation."
		}
		if !slices.Contains(options, "--ddtrace") {
			return "incompatible", "The test step replaces the action's PYTEST_ADDOPTS; preserve --ddtrace and existing options."
		}
		plugins := []string{"ddtrace", "ddtrace.contrib.pytest.plugin", "ddtrace.contrib.internal.pytest.plugin", "ddtrace.testing.internal.pytest.entry_point"}
		declared, err := runtimeValue(invocation.environment["PYTEST_PLUGINS"], row)
		if err != nil {
			return "inconclusive", err.Error()
		}
		// Older pytest versions resolve PYTEST_PLUGINS as modules only.
		loaded, alias := false, false
		for _, name := range strings.Split(declared, ",") {
			name = strings.TrimSpace(name)
			loaded = loaded || slices.Contains(plugins[1:], name)
			alias = alias || name == "ddtrace"
		}
		blocked := map[string]bool{}
		for index := 0; index < len(options); index++ {
			option := options[index]
			plugin := strings.TrimPrefix(option, "-p")
			if option == "-p" && index+1 < len(options) {
				index++
				plugin = options[index]
			} else if !strings.HasPrefix(option, "-p") {
				continue
			}
			name := strings.TrimPrefix(plugin, "no:")
			if slices.Contains(plugins, name) {
				blocked[name] = strings.HasPrefix(plugin, "no:")
				if !blocked[name] {
					loaded = true
				}
			}
		}
		if slices.ContainsFunc(plugins, func(name string) bool { return blocked[name] }) {
			return "incompatible", "The pytest options explicitly block the Datadog plugin."
		}
		if (autoload != "" || slices.Contains(options, "--disable-plugin-autoload")) && !loaded {
			if alias {
				return "inconclusive", "PYTEST_PLUGINS entry-point aliases depend on the pytest version; use a Datadog plugin module or -p ddtrace to verify explicit loading."
			}
			return "incompatible", "pytest plugin autoload is disabled; explicitly load the Datadog plugin with -p ddtrace in CI. Local plugin loading does not validate CI setup."
		}
	}
	return "", ""
}

var pythonAddoptsReference = regexp.MustCompile(`\$\{\{\s*env\.PYTEST_ADDOPTS\s*\}\}`)

// The action prepends --ddtrace to inherited options through GITHUB_ENV.
// Test-step declarations override that export in both CI checks and replay.
func pythonAddopts(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) (string, error) {
	value := ""
	for _, layer := range []map[string]string{workflow.Env, job.Env} {
		declared, ok := layer["PYTEST_ADDOPTS"]
		if !ok {
			continue
		}
		parent := value
		value = pythonAddoptsReference.ReplaceAllStringFunc(declared, func(string) string { return parent })
	}
	value = strings.TrimSpace("--ddtrace " + value)
	if declared, exists := step.Env["PYTEST_ADDOPTS"]; exists {
		parent := value
		value = pythonAddoptsReference.ReplaceAllStringFunc(declared, func(string) string { return parent })
	}
	return runtimeValue(value, row)
}
