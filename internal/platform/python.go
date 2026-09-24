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
	"slices"
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
	executor commandExecutor
}

func NewPython() *Python {
	return &Python{
		executor: &ext.DefaultCommandExecutor{},
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
	fw, err := selectFramework(p.Name(), hint, []framework.Framework{framework.NewPytest()})
	if err != nil {
		return nil, err
	}
	fw.SetPlatformEnv(p.GetPlatformEnv())
	return fw, nil
}

func (p *Python) TestSkippingLevel() settings.TestSkippingLevel {
	return settings.TestSkippingLevelTest
}

// GetPlatformEnv returns environment variables required for Python commands.
// It appends --ddtrace to PYTEST_ADDOPTS to load the ddtrace pytest plugin.
func (p *Python) GetPlatformEnv() map[string]string {
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
	args := append(append([]string{}, prefix...), "-c", "import importlib.metadata, sys; print(importlib.metadata.version(sys.argv[1]))", requiredPackageName)
	return tracerProbe(ctx, p.executor, command, args, nil)
}

func pythonInterpreter(command string, args []string) (string, []string) {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	if isPythonExecutable(base) {
		return command, nil
	}
	if (base == "uv" || base == "poetry") && len(args) > 0 && args[0] == "run" {
		return command, pythonRunPrefix(args)
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

// Keep launcher options that select the environment, replacing only the test command.
func pythonRunPrefix(args []string) []string {
	valueOptions := strings.Fields(`--extra --no-extra --group --no-group --only-group
		--env-file --with --with-editable --with-requirements --package
		--index --default-index -i --index-url --extra-index-url -f --find-links
		--index-strategy --keyring-provider -P --upgrade-package --resolution
		--prerelease --fork-strategy --exclude-newer --reinstall-package --link-mode
		-C --config-setting --no-build-isolation-package --no-build-package
		--no-binary-package --cache-dir --refresh-package -p --python --color
		--allow-insecure-host --directory --project --config-file`)
	prefix := []string{"run"}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-m" || arg == "--module" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if isPythonExecutable(filepath.Base(arg)) {
				return append(prefix, arg)
			}
			break
		}
		prefix = append(prefix, arg)
		if arg == "--" {
			break
		}
		if slices.Contains(valueOptions, arg) && i+1 < len(args) {
			i++
			prefix = append(prefix, args[i])
		}
	}
	return append(prefix, "python")
}

func isPythonExecutable(base string) bool {
	base = strings.TrimSuffix(base, ".exe")
	if !strings.HasPrefix(base, "python") {
		return false
	}
	for _, character := range strings.TrimPrefix(base, "python") {
		if (character < '0' || character > '9') && character != '.' {
			return false
		}
	}
	return true
}

func (p *Python) InstallTestdriveTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	directory := options.Directory
	command, prefixArgs := pythonInterpreter(options.Command, options.Args)
	packageName := "ddtrace"
	if ref, ok := strings.CutPrefix(options.Version, "git:"); ok {
		if ref == "" {
			return TracerInstallation{}, fmt.Errorf("tracer git ref must not be empty")
		}
		packageName += " @ git+https://github.com/DataDog/dd-trace-py.git@" + ref
	} else if options.Version != "" && options.Version != "latest" {
		packageName += "==" + options.Version
	}
	version, err := p.DetectTracer(ctx, options)
	if err == nil && version != "" {
		return TracerInstallation{Project: true}, nil
	}
	// uv environments need not contain pip; provide it only for this setup command.
	if filepath.Base(command) == "uv" && len(prefixArgs) > 0 {
		prefixArgs = slices.Insert(prefixArgs, 1, "--with", "pip")
	}
	target := filepath.Join(directory, "python-packages")
	args := append(append([]string{}, prefixArgs...), "-m", "pip", "install", "--disable-pip-version-check", "--target", target, packageName)
	if output, err := p.executor.CombinedOutput(ctx, command, args, map[string]string{"DD_FAST_BUILD": "1"}); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("install ddtrace", output, err)
	}
	bootstrap := filepath.Join(directory, "python")
	if err := os.MkdirAll(bootstrap, 0755); err != nil {
		return TracerInstallation{}, fmt.Errorf("create Python tracer bootstrap: %w", err)
	}
	encodedTarget, _ := json.Marshal(target)
	contents := "import sys\nsys.path.append(" + string(encodedTarget) + ")\n"
	if err := os.WriteFile(filepath.Join(bootstrap, "sitecustomize.py"), []byte(contents), 0600); err != nil {
		return TracerInstallation{}, fmt.Errorf("write Python tracer bootstrap: %w", err)
	}
	return TracerInstallation{Path: bootstrap}, nil
}
