package testdrive

import (
	"encoding/json"
	"github.com/DataDog/ddtest/internal/environment"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func javascriptEnvironment(ciInitPath string) map[string]string {
	return javascriptEnvironmentWithOptions(ciInitPath, os.Getenv("NODE_OPTIONS"))
}

func javascriptEnvironmentWithOptions(ciInitPath, currentOptions string) map[string]string {
	nodeOptions := "-r " + strconv.Quote(ciInitPath)
	if current := stripDatadogNodeOptions(currentOptions); current != "" {
		// Even an absolute tracer path can depend on the project's loader to
		// resolve its dependencies.
		nodeOptions = current + " " + nodeOptions
	}

	return map[string]string{"NODE_OPTIONS": nodeOptions}
}

func stripDatadogNodeOptions(value string) string {
	fields, err := environment.SplitNodeOptions(value)
	if err != nil {
		return value
	}
	kept := make([]string, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if field == "-r" || field == "--require" || field == "--import" {
			if index+1 < len(fields) && isDatadogNodePreload(fields[index+1]) {
				index++
				continue
			}
		}
		if strings.HasPrefix(field, "--require=") && isDatadogNodePreload(strings.TrimPrefix(field, "--require=")) {
			continue
		}
		if strings.HasPrefix(field, "--import=") && isDatadogNodePreload(strings.TrimPrefix(field, "--import=")) {
			continue
		}
		if strings.HasPrefix(field, "-r") && isDatadogNodePreload(strings.TrimPrefix(field, "-r")) {
			continue
		}
		kept = append(kept, field)
	}
	return environment.JoinNodeOptions(kept)
}

func isDatadogNodePreload(value string) bool {
	value = strings.ReplaceAll(value, `\`, "/")
	return value == "dd-trace/ci/init" || value == "dd-trace/register.js" || strings.HasSuffix(filepath.ToSlash(value), "/dd-trace/ci/init.js") ||
		strings.HasSuffix(filepath.ToSlash(value), "/dd-trace/register.js")
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

func supportsNodeRegister(version string) bool {
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
	// dd-trace/register.js needs module.register, which arrived after --import.
	return major > 20 || major == 20 && minor >= 6 || major == 18 && minor >= 19
}

func (t *Testdrive) javascriptEnvironment(path string) map[string]string {
	env := javascriptEnvironmentWithOptions(path, t.nodeOptions())
	// ESM instrumentation is needed by Vitest and by ESM test/config files.
	version := ""
	if t.nodeVersion != nil {
		version = t.nodeVersion()
	}
	if supportsNodeRegister(version) {
		registerPath := filepath.Join(filepath.Dir(filepath.Dir(path)), "register.js")
		if info, err := os.Stat(registerPath); err == nil && info.Mode().IsRegular() {
			env["NODE_OPTIONS"] += " --import " + strconv.Quote(absoluteFileURL(registerPath))
		}
	}
	return env
}
