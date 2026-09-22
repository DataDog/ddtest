package platform

import (
	"encoding/json"
	"fmt"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/kballard/go-shellquote"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type packageManifest struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func readPackageManifest(repositoryRoot string) (packageManifest, bool, error) {
	path := filepath.Join(repositoryRoot, "package.json")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return packageManifest{}, false, nil
	}
	if err != nil {
		return packageManifest{}, false, fmt.Errorf("read %s: %w", path, err)
	}

	var manifest packageManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return packageManifest{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return manifest, true, nil
}

func (m packageManifest) usesAny(names ...string) bool {
	for _, name := range names {
		if _, ok := m.Dependencies[name]; ok {
			return true
		}
		if _, ok := m.DevDependencies[name]; ok {
			return true
		}
	}

	for _, script := range m.Scripts {
		if isDirectJavaScriptCommand(script, names...) {
			return true
		}
	}
	return false
}

// isDirectJavaScriptCommand deliberately accepts only a single direct runner
// invocation. For shell expressions and wrappers, detection relies on declared
// dependencies rather than guessing which words represent an executable.
func isDirectJavaScriptCommand(script string, names ...string) bool {
	if strings.ContainsAny(script, "\r\n;&|<>`$()#") {
		return false
	}
	args, err := shellquote.Split(script)
	if err != nil || len(args) == 0 || slices.Contains(args, "--") {
		return false
	}
	command := strings.TrimPrefix(args[0], "./")
	command = strings.TrimPrefix(command, "node_modules/.bin/")
	for _, name := range names {
		if command == name {
			return true
		}
	}
	return false
}

func (j *JavaScript) detectFramework(root, hint string) (framework.Framework, error) {
	candidates := []framework.Framework{framework.NewJest(), framework.NewMocha(), framework.NewCypress(), framework.NewPlaywright(), framework.NewCucumber(), framework.NewVitest()}
	if hint == "" {
		manifest, found, err := readPackageManifest(root)
		if err != nil {
			return nil, err
		}
		if !found {
			candidates = nil
		} else {
			candidates = slices.DeleteFunc(candidates, func(f framework.Framework) bool {
				names := []string{f.Name()}
				switch f.Name() {
				case "playwright":
					names = append(names, "@playwright/test")
				case "cucumber":
					names = append(names, "@cucumber/cucumber", "cucumber-js")
				}
				return !manifest.usesAny(names...)
			})
		}
	}
	fw, err := selectFramework(j.Name(), hint, candidates)
	if err != nil {
		return nil, err
	}
	env := j.GetPlatformEnv()
	if fw.Name() == "vitest" {
		env = addNodeImport(env, ddTraceRegisterModule)
	}
	fw.SetPlatformEnv(env)
	return fw, nil
}
