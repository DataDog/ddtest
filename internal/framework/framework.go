package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/kballard/go-shellquote"
)

type Framework interface {
	Name() string
	Detect(repositoryRoot string) (bool, error)
	TestPattern() string
	DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error)
	DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error)
	RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error
	SetPlatformEnv(platformEnv map[string]string)
	GetPlatformEnv() map[string]string
	SupportsFullTestDiscovery() bool
	SourceFileForSuite(suite string) (string, bool)
	HasUnskippableMarker(testFile string) bool
}

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
// invocation. Shell expressions and wrappers cannot safely receive appended
// runner flags. For those scripts, detection relies on declared dependencies.
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

func detectJavaScriptFramework(repositoryRoot string, packageNames ...string) (bool, error) {
	manifest, found, err := readPackageManifest(repositoryRoot)
	if err != nil || !found {
		return false, err
	}
	return manifest.usesAny(packageNames...), nil
}

func detectAnyPath(repositoryRoot string, paths ...string) (bool, error) {
	for _, relativePath := range paths {
		path := filepath.Join(repositoryRoot, relativePath)
		_, err := os.Stat(path)
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	return false, nil
}

func detectFileContaining(repositoryRoot string, filename string, values ...string) (bool, error) {
	path := filepath.Join(repositoryRoot, filename)
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	text := strings.ToLower(string(contents))
	for _, value := range values {
		if strings.Contains(text, strings.ToLower(value)) {
			return true, nil
		}
	}
	return false, nil
}
