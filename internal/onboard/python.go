package onboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/environment"
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
		rows = []map[string]any{nil}
	}
	for _, row := range rows {
		for _, step := range job.Steps {
			resolved, _, _ := resolveVariantStep(workflow, job, step, row, "pytest")
			if resolvePythonStep(root, workflow, job, resolved).Matched || environment.LooksLikeTestCommand(root, resolved.Run, "python", "pytest") {
				return true
			}
		}
	}
	return false
}

// Restrict static checks to numeric minimum/maximum constraints. Other PEP 440
// expressions remain inconclusive rather than silently dropping a constraint.
func ComparePythonRequirement(python, requirement string) (string, string) {
	parts := strings.Split(requirement, ",")
	slices.SortFunc(parts, func(a, b string) int { return -strings.Compare(strings.TrimSpace(a), strings.TrimSpace(b)) })
	status, reason := CompareNodeRequirement(python, strings.Join(parts, " "))
	return status, strings.NewReplacer("Node", "Python", "setup-node", "setup-python").Replace(reason)
}

func pythonInvocation(words []string) bool {
	if len(words) == 0 {
		return false
	}
	base := strings.TrimSuffix(filepath.Base(words[0]), ".exe")
	if base == "pytest" {
		return true
	}
	if strings.HasPrefix(base, "python") {
		index := slices.Index(words, "-m")
		return index > 0 && index+1 < len(words) && words[index+1] == "pytest"
	}
	if (base == "uv" || base == "poetry") && len(words) > 2 && words[1] == "run" {
		for i := 2; i < len(words); i++ {
			arg := words[i]
			if pythonInvocation(words[i:]) {
				return true
			}
			option, _, assigned := strings.Cut(arg, "=")
			switch option {
			case "--no-sync", "--locked", "--frozen", "--offline", "--active", "--no-python-downloads", "--no-default-groups", "--":
			case "--project", "--directory", "--package", "--python", "-p", "--group", "--extra":
				if !assigned {
					i++
				}
			default:
				return false
			}
		}
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
	commands, err := staticCommands(step.Run)
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
	if !supportedCIShell(shell) {
		return unresolvedCommand("Unsupported Python CI shell: " + shell)
	}
	result := commandResolution{}
	for _, words := range commands {
		if len(words) == 0 {
			continue
		}
		if pythonInvocation(words) {
			result.Matched = true
			result.Tests = append(result.Tests, TestCommand{Directory: filepath.ToSlash(filepath.Clean(directory)), Command: shellquote.Join(words...)})
			continue
		}
		base := filepath.Base(words[0])
		if len(words) == 2 && base == "cd" && filepath.IsLocal(words[1]) {
			directory = filepath.Join(directory, words[1])
			continue
		}
		setup := base == "pip" || base == "pip3" || (base == "uv" && len(words) > 1 && slices.Contains([]string{"sync", "pip"}, words[1]))
		if strings.HasPrefix(base, "python") && len(words) > 2 && words[1] == "-m" && slices.Contains([]string{"pip", "venv"}, words[2]) {
			setup = true
		}
		if setup {
			result.ReviewReason = "Prepare the same Python dependencies as CI before local validation; dependency installation is not replayed."
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkPythonCIRuntimes(ctx, root, localVersion, ResolvePythonRelease, pythonActionVersion)
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

func checkPythonCIRuntimes(ctx context.Context, root, localVersion string, resolve func(context.Context, string) (PythonRelease, error), actionDefault func(context.Context, string) (string, error)) RuntimeCheck {
	result := RuntimeCheck{Status: "not applicable", Reason: "No GitHub Actions pytest commands found; CI was not exercised."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	cache := map[string]PythonRelease{}
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
				var python, action, requested string
				var setupErr error
				for i, step := range job.Steps {
					uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
					active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
					if err == nil && !active {
						continue
					}
					if err != nil {
						setupErr = err
					}
					if uses == "actions/setup-python" {
						action, requested = "", ""
						python, err = runtimeValue(step.With["python-version"], row)
						if err != nil {
							setupErr = err
						}
					}
					if uses == githubAction {
						languages, e := runtimeValue(step.With["languages"], row)
						if e != nil {
							setupErr = e
						}
						if languages == "all" || slices.Contains(strings.Fields(languages), "python") {
							action = step.Uses
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
								setupErr = err
							}
						}
					}
					resolved, _, envErr := resolveVariantStep(workflow, job, step, row, "pytest")
					r := resolvePythonStep(root, workflow, job, resolved)
					if !r.Matched && r.Reason == "" {
						continue
					}
					finding := RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Python: python, Action: action, Status: "inconclusive"}
					switch {
					case conditionErr != nil:
						finding.Reason = conditionErr.Error()
					case setupErr != nil:
						finding.Reason = setupErr.Error()
					case envErr != nil:
						finding.Reason = envErr.Error()
					case r.Reason != "":
						finding.Reason = r.Reason
					case action == "":
						finding.Status, finding.Code, finding.Reason = "incompatible", "missing_instrumentation", "No Datadog Python action runs before this pytest step. Preserve the test command and add instrumentation."
					default:
						selection, ok := cache[requested]
						if !ok {
							selection, err = resolve(ctx, requested)
							if err == nil {
								cache[requested] = selection
							}
						}
						if err != nil {
							finding.Reason = err.Error()
						} else {
							finding.Tracer = "ddtrace@" + selection.Version
							finding.Requirement = selection.RequiresPython
							finding.Status, finding.Reason = ComparePythonRequirement(python, selection.RequiresPython)
							if selection.Version != localVersion {
								finding.Status, finding.Reason = "incompatible", fmt.Sprintf("CI selects ddtrace@%s; local validation selected ddtrace@%s. Align selections or validate the CI release separately.", selection.Version, localVersion)
							}
							if value, ok := step.Env["PYTHONPATH"]; ok && !strings.Contains(value, "${{ env.PYTHONPATH }}") {
								finding.Status, finding.Reason = "incompatible", "The test step replaces the action's PYTHONPATH; preserve the injected tracer package path."
							}
							if status, reason := pythonInstrumentationOptions(workflow, job, resolved, row); status != "" {
								finding.Status, finding.Reason = status, reason
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
			if finding.Status != "compatible" {
				result.Status = finding.Status
				result.Reason = "Python CI configuration is incomplete or incompatible; inspect each entry and preserve the test matrix."
				if finding.Status == "incompatible" {
					break
				}
			}
		}
	}
	return result
}

func pythonInstrumentationOptions(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) (string, string) {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	// The action supplies --ddtrace unless declared options replace its value.
	value, declared := env["PYTEST_ADDOPTS"]
	if !declared {
		value = "--ddtrace"
	}
	value = strings.ReplaceAll(value, "${{ env.PYTEST_ADDOPTS }}", "--ddtrace")
	value, err := runtimeValue(value, row)
	if err != nil {
		return "inconclusive", err.Error()
	}
	options, err := shellquote.Split(value)
	if err != nil {
		return "inconclusive", "Cannot parse CI PYTEST_ADDOPTS: " + err.Error()
	}
	commands, _ := staticCommands(step.Run)
	for _, words := range commands {
		if pythonInvocation(words) {
			options = append(options, words...)
		}
	}
	if slices.Contains(options, "--no-ddtrace") {
		return "incompatible", "The pytest command or effective PYTEST_ADDOPTS explicitly disables Datadog instrumentation."
	}
	if !slices.Contains(options, "--ddtrace") {
		return "incompatible", "The test step replaces the action's PYTEST_ADDOPTS; preserve --ddtrace and existing options."
	}
	loaded := false
	for index, option := range options {
		plugin := strings.TrimPrefix(option, "-p")
		if option == "-p" && index+1 < len(options) {
			plugin = options[index+1]
		} else if !strings.HasPrefix(option, "-p") {
			continue
		}
		if slices.Contains([]string{"ddtrace", "ddtrace.contrib.pytest.plugin", "ddtrace.contrib.internal.pytest.plugin"}, strings.TrimPrefix(plugin, "no:")) {
			if strings.HasPrefix(plugin, "no:") {
				return "incompatible", "The pytest options explicitly block the Datadog plugin."
			}
			loaded = true
		}
	}
	autoload, err := runtimeValue(env["PYTEST_DISABLE_PLUGIN_AUTOLOAD"], row)
	if err != nil {
		return "inconclusive", err.Error()
	}
	if (autoload != "" || slices.Contains(options, "--disable-plugin-autoload")) && !loaded {
		return "incompatible", "pytest plugin autoload is disabled; explicitly load the Datadog plugin with -p ddtrace in CI. Local plugin loading does not validate CI setup."
	}
	return "", ""
}
