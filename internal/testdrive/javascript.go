package testdrive

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/utils"
)

func javascriptEnvironment(ciInitPath string) map[string]string {
	nodeOptions := "-r " + strconv.Quote(ciInitPath)
	if current := stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS")); current != "" {
		// Even an absolute tracer path can depend on the project's loader to
		// resolve its dependencies.
		nodeOptions = current + " " + nodeOptions
	}

	return map[string]string{"NODE_OPTIONS": nodeOptions}
}

func stripDatadogNodeOptions(value string) string {
	for _, module := range []string{"dd-trace/ci/init", "dd-trace/register.js"} {
		value = utils.NodeOptionsWithoutRequire(value, module)
		value = utils.NodeOptionsWithoutImport(value, module)
	}
	return value
}

func javascriptTracerVersion(preload string) string {
	if preload == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(preload)), "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

func currentNodeVersion() string {
	output, err := exec.Command("node", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func supportsNodeImport(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		return false
	}
	return major > 18 || major == 18 && minor >= 18
}

func (t *Testdrive) javascriptEnvironment(path string) map[string]string {
	env := javascriptEnvironment(path)
	// ESM instrumentation is needed by Vitest and by ESM test/config files.
	version := ""
	if t.nodeVersion != nil {
		version = t.nodeVersion()
	}
	if supportsNodeImport(version) {
		registerPath := filepath.Join(filepath.Dir(filepath.Dir(path)), "register.js")
		if info, err := os.Stat(registerPath); err == nil && info.Mode().IsRegular() {
			env["NODE_OPTIONS"] += " --import " + strconv.Quote(absoluteFileURL(registerPath))
		}
	}
	return env
}
