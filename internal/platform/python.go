package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
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
	output, err := DetectPythonTracer(ctx, p.executor, "python", nil)
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
