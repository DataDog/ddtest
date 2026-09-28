package testdrive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func javascriptEnvironment(ciInitPath string) map[string]string {
	nodeOptions := "-r " + strconv.Quote(ciInitPath)
	if current := stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS")); current != "" {
		nodeOptions += " " + current
	}

	return map[string]string{"NODE_OPTIONS": nodeOptions}
}

func stripDatadogNodeOptions(value string) string {
	fields := strings.Fields(value)
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
	return strings.Join(kept, " ")
}

func isDatadogNodePreload(value string) bool {
	value = strings.Trim(value, `"'`)
	return value == "dd-trace/ci/init" || strings.HasSuffix(filepath.ToSlash(value), "/dd-trace/ci/init.js") ||
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
