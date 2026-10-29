package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/version"
)

// pep440PreReleaseRe matches PEP 440 pre-release suffixes (a/b/rc + digits)
// embedded directly in a Python package version, preserving optional local
// version metadata, e.g. "4.12.0rc1+gabc123".
var pep440PreReleaseRe = regexp.MustCompile(`^(\d+\.\d+(?:\.\d+)*)(a|b|rc)(\d+)(\+.*)?$`)

// normalizePyVersion converts PEP 440 version strings to semver-compatible ones.
// "4.12.0rc1+gabc123" -> "4.12.0-rc1+gabc123"; "4.10.3" is unchanged.
func normalizePyVersion(v string) string {
	return pep440PreReleaseRe.ReplaceAllString(v, "$1-$2$3$4")
}

//go:embed scripts/python_env.py
var pythonEnvScript string

const (
	requiredPackageName    = "ddtrace"
	requiredPackageVersion = "4.11.0"
	pytestAddOptsEnvVar    = "PYTEST_ADDOPTS"
	pytestDefaultAddOpts   = "--ddtrace"
)

type Python struct {
	frameworkEnv      map[string]string
	executor          commandExecutor
	testSkippingLevel settings.TestSkippingLevel
}

func NewPython() *Python {
	return &Python{
		executor:          &ext.DefaultCommandExecutor{},
		testSkippingLevel: settings.GetTestSkippingLevel(),
	}
}

func (p *Python) Name() string {
	return "python"
}

// Detect recognizes Python project markers without parsing dependencies or running Python.
func (p *Python) Detect(root string) (bool, error) {
	found, err := detectAnyFile(root, "pyproject.toml", "setup.py", "requirements.txt", "tox.ini", "pytest.ini", ".pytest.ini", "conftest.py")
	if err != nil || found {
		return found, err
	}
	// setup.cfg is a generic name: require a Python-specific section.
	data, err := os.ReadFile(filepath.Join(root, "setup.cfg"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read setup.cfg: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		section, _, ok := strings.Cut(line, "]")
		if ok && slices.Contains([]string{"[options", "[options.packages.find", "[options.extras_require", "[tool:pytest", "[flake8", "[isort", "[mypy", "[coverage:run", "[coverage:report"}, section) {
			return true, nil
		}
	}
	return false, nil
}

// Pytest is the only supported Python framework and is the platform default.
func (p *Python) DetectFramework() (framework.Framework, error) {
	hint := settings.GetFramework()
	fw, err := selectFramework(p.Name(), hint, []framework.Framework{framework.NewPytest(p)})
	if err != nil {
		return nil, err
	}
	p.frameworkEnv = p.baseEnv()
	return fw, nil
}

func (p *Python) TestSkippingLevel() settings.TestSkippingLevel {
	return settings.NormalizeTestSkippingLevel(p.testSkippingLevel)
}

// baseEnv returns environment variables required for Python commands.
// It appends --ddtrace to PYTEST_ADDOPTS to load the ddtrace pytest plugin.
func (p *Python) baseEnv() map[string]string {
	envMap := make(map[string]string)

	// Get existing PYTEST_ADDOPTS if set, then append --ddtrace
	existingOpts := os.Getenv(pytestAddOptsEnvVar)
	if existingOpts != "" {
		envMap[pytestAddOptsEnvVar] = existingOpts + " " + pytestDefaultAddOpts
	} else {
		envMap[pytestAddOptsEnvVar] = pytestDefaultAddOpts
	}

	return envMap
}

func (p *Python) CreateTagsMap(ctx context.Context) (map[string]string, error) {
	tags := make(map[string]string)
	tags["language"] = p.Name()

	// Create plan directory if it doesn't exist
	if err := os.MkdirAll(constants.PlanDirectory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Create a temporary file for the Python script output
	tempFile := constants.PythonEnvOutputPath
	defer func() { _ = os.Remove(tempFile) }()

	// Execute the embedded Python script to get runtime tags
	args := []string{"-c", pythonEnvScript, tempFile}
	if output, err := p.executor.CombinedOutput(ctx, "python", args, nil); err != nil {
		return nil, runtimeTagProbeError("failed to execute Python script", output, err)
	}

	// Read the JSON output from the temp file
	fileContent, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read Python script output file: %w", err)
	}

	// Parse the JSON output
	var pythonTags map[string]string
	if err := json.Unmarshal(fileContent, &pythonTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w, tried to parse: %s", err, string(fileContent))
	}

	// Merge the tags from the Python output
	maps.Copy(tags, pythonTags)

	return tags, nil
}

func (p *Python) SanityCheck(ctx context.Context) error {
	output, err := p.DetectTracer(ctx, TracerOptions{Command: "python"})
	if err != nil {
		return fmt.Errorf("detect ddtrace: %w", err)
	}
	if output == "" {
		return fmt.Errorf("ddtrace is not installed")
	}

	versionStr := normalizePyVersion(output)
	pkgVersion, err := version.Parse(versionStr)
	if err != nil {
		return fmt.Errorf("failed to parse %s version %q: %w", requiredPackageName, versionStr, err)
	}

	requiredVersion, err := version.Parse(requiredPackageVersion)
	if err != nil {
		return err
	}

	if pkgVersion.Compare(requiredVersion) < 0 {
		return fmt.Errorf("%s version %s is lower than required >= %s", requiredPackageName, pkgVersion.String(), requiredVersion.String())
	}

	return nil
}

// DetectTracer returns the installed version using the test runner's interpreter.
func (p *Python) DetectTracer(ctx context.Context, options TracerOptions) (string, error) {
	command, prefix := pythonInterpreter(options.Command, options.Args)
	args := append(append([]string{}, prefix...), "-c", detectPythonTracer, requiredPackageName)
	return tracerProbe(ctx, p.executor, command, args, nil)
}

const detectPythonTracer = "import importlib.metadata, pathlib, sys; pathlib.Path(sys.argv[2]).write_text(importlib.metadata.version(sys.argv[1]))"

func pythonInterpreter(command string, args []string) (string, []string) {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	if IsPythonExecutable(base) {
		return command, nil
	}
	if (base == "uv" || base == "poetry") && len(args) > 0 && args[0] == "run" {
		return command, []string{"run", "python"}
	}
	if strings.HasPrefix(base, "pytest") && filepath.Dir(command) != "." {
		interpreter := filepath.Join(filepath.Dir(command), "python")
		if strings.HasSuffix(base, ".exe") {
			interpreter += ".exe"
		}
		return interpreter, nil
	}
	if _, err := exec.LookPath("python"); err == nil {
		return "python", nil
	}
	return "python3", nil
}

var pythonExecutableName = regexp.MustCompile(`^python(?:[0-9]+(?:\.[0-9]+)*[dt]?)?(?:_d)?(?:\.exe)?$`)

// IsPythonExecutable recognizes interpreters, including free-threaded builds
// such as python3.13t and debug builds such as python3.13d and python_d.exe,
// without accepting custom wrappers.
func IsPythonExecutable(command string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	return pythonExecutableName.MatchString(base)
}

func (p *Python) InstallTestdriveTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	command, args, err := p.TracerInstallCommand(options)
	if err != nil {
		return TracerInstallation{}, err
	}
	version, err := p.DetectTracer(ctx, options)
	if err == nil && version != "" {
		return TracerInstallation{Project: true}, nil
	}

	// Match CI auto-instrumentation: install into a separate venv and expose its
	// site-packages through PYTHONPATH. Testdrive owns and removes the directory.
	executor := boundedExecutor(p.executor, options.Timeout)
	interpreter, prefix := pythonInterpreter(options.Command, options.Args)
	venvArgs := append(prefix, "-m", "venv", filepath.Join(options.Directory, "python"))
	if output, err := executor.CombinedOutput(ctx, interpreter, venvArgs, nil); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("create Python tracer environment", output, err)
	}
	if output, err := executor.CombinedOutput(ctx, command, args, map[string]string{"DD_FAST_BUILD": "1"}); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("install ddtrace", output, err)
	}
	// Ask the new interpreter for its platform-specific package location. Isolated
	// mode keeps project startup hooks and PYTHONPATH out of this setup probe.
	stdout, stderr, err := executor.Output(ctx, command, []string{"-I", "-c", "import sysconfig; print(sysconfig.get_path('purelib'))"}, nil)
	if err != nil {
		return TracerInstallation{}, runtimeTagProbeError("locate Python tracer packages", append(stdout, stderr...), err)
	}
	path := strings.TrimSpace(string(stdout))
	if path == "" {
		return TracerInstallation{}, fmt.Errorf("Python tracer package location is empty")
	}
	return TracerInstallation{Path: path}, nil
}

func (p *Python) TracerInstallCommand(options TracerOptions) (string, []string, error) {
	command := filepath.Join(options.Directory, "python", "bin", "python")
	if runtime.GOOS == "windows" {
		command = filepath.Join(options.Directory, "python", "Scripts", "python.exe")
	}
	packageName := "ddtrace"
	if ref, ok := strings.CutPrefix(options.Version, "git:"); ok {
		if ref == "" {
			return "", nil, fmt.Errorf("tracer git ref must not be empty")
		}
		packageName += " @ git+https://github.com/DataDog/dd-trace-py.git@" + ref
	} else if options.Version != "" && options.Version != "latest" {
		packageName += "==" + options.Version
	}
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", packageName}
	return command, args, nil
}

func (p *Python) RunEnv(options framework.RuntimeOptions) (map[string]string, error) {
	if len(options.PreloadFiles) != 0 {
		return nil, fmt.Errorf("Python framework preloads are not supported")
	}
	env := maps.Clone(p.frameworkEnv)
	if env == nil {
		env = p.baseEnv()
	}
	maps.Copy(env, options.Env)
	// Keep tracer coverage/skipping granularity aligned with the backend
	// requests and plan, even when the process or worker env sets another mode.
	env["_DD_CIVISIBILITY_ITR_SUITE_MODE"] = strconv.FormatBool(p.TestSkippingLevel() == settings.TestSkippingLevelSuite)
	if p.TestSkippingLevel() == settings.TestSkippingLevelSuite {
		// The plan owns suite skipping. The tracer must execute files retained
		// for missing source mappings or conservative unskippable checks,
		// while continuing to collect coverage and report test results.
		env["_DD_CIVISIBILITY_ITR_PREVENT_TEST_SKIPPING"] = "true"
	}
	return env, nil
}

func (p *Python) DiscoveryEnv(_ context.Context, kind framework.DiscoveryKind, options framework.RuntimeOptions) (map[string]string, error) {
	switch kind {
	case framework.FileDiscovery:
		return maps.Clone(options.Env), nil
	case framework.FullDiscovery:
		return p.RunEnv(options)
	default:
		return nil, fmt.Errorf("unknown discovery kind: %d", kind)
	}
}
