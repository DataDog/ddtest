package testdrive

import (
	"encoding/json"
	"os"
	"path/filepath"
)

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
