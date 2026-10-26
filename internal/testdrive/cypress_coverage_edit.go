// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// coverageEdit is the exact, approval-gated change that makes a recognized
// application build expose Istanbul's window.__coverage__ to Cypress. It is
// never applied by ddtest: it changes the customer's build and dependencies.
type coverageEdit struct {
	Build            string     `json:"build"`
	Install          string     `json:"install"`
	Files            []fileEdit `json:"files"`
	Serve            string     `json:"serve"`
	RequiresApproval bool       `json:"requires_approval"`
}

type fileEdit struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	// Content is the whole file for create, or the exact text to add for edit.
	Content string `json:"content"`
	Where   string `json:"where,omitempty"`
}

// babelTestCoverage scopes Istanbul to builds run with BABEL_ENV=test, so
// ordinary and production builds stay uninstrumented.
const babelTestCoverage = `{
  "env": {
    "test": {
      "plugins": ["istanbul"]
    }
  }
}
`

var browserifyInvocation = regexp.MustCompile(`(?:^|[\s&;|])((?:node\s+)?(?:\./)?(?:node_modules/\.bin/browserify|node_modules/browserify/bin/cmd(?:\.js)?|browserify))(\s)`)

// cypressCoverageEdit recognizes the application build served to Cypress and
// returns the exact instrumentation change, or nil when the build is not one
// ddtest recognizes; the prose guidance then applies.
func cypressCoverageEdit(root string) *coverageEdit {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	depends := func(name string) bool {
		_, dev := manifest.DevDependencies[name]
		_, prod := manifest.Dependencies[name]
		return dev || prod
	}
	install := func(packages ...string) string {
		switch {
		case coverageFileExists(filepath.Join(root, "pnpm-lock.yaml")):
			return "pnpm add --save-dev " + strings.Join(packages, " ")
		case coverageFileExists(filepath.Join(root, "yarn.lock")):
			return "yarn add --dev " + strings.Join(packages, " ")
		}
		return "npm install --save-dev " + strings.Join(packages, " ")
	}
	// An existing Babel configuration would replace the one proposed here.
	for _, name := range []string{".babelrc", ".babelrc.json", ".babelrc.js", "babel.config.json", "babel.config.js", "babel.config.cjs", "babel.config.mjs"} {
		if coverageFileExists(filepath.Join(root, name)) {
			return nil
		}
	}
	if config := firstExisting(root, "vite.config.ts", "vite.config.js", "vite.config.mts", "vite.config.mjs", "vite.config.cjs"); config != "" && depends("vite") {
		return &coverageEdit{
			Build:   "vite",
			Install: install("vite-plugin-istanbul"),
			Files: []fileEdit{{Path: config, Action: "edit", Where: "add the import at the top and the plugin to the plugins array of the exported config",
				Content: "import istanbul from 'vite-plugin-istanbul'\n\nistanbul({ include: 'src/*', exclude: ['node_modules', 'cypress/'], extension: ['.js', '.jsx', '.ts', '.tsx', '.vue'], requireEnv: true })\n"}},
			Serve:            "Start the application served to Cypress with VITE_COVERAGE=true; other builds stay uninstrumented.",
			RequiresApproval: true,
		}
	}
	if config := firstExisting(root, "webpack.config.js", "webpack.config.cjs", "webpack.config.mjs", "webpack.config.ts"); config != "" && depends("webpack") {
		return &coverageEdit{
			Build:   "webpack",
			Install: install("babel-loader", "@babel/core", "babel-plugin-istanbul"),
			Files: []fileEdit{
				{Path: "babel.config.json", Action: "create", Content: babelTestCoverage},
				{Path: config, Action: "edit", Where: "add as the first entry of module.rules",
					Content: "{ test: /\\.[cm]?[jt]sx?$/, exclude: /node_modules/, use: 'babel-loader' },\n"},
			},
			Serve:            "Build the application served to Cypress with BABEL_ENV=test; other builds stay uninstrumented.",
			RequiresApproval: true,
		}
	}
	for _, name := range slices.Sorted(maps.Keys(manifest.Scripts)) {
		script := manifest.Scripts[name]
		match := browserifyInvocation.FindStringSubmatchIndex(script)
		if match == nil {
			continue
		}
		environment := "BABEL_ENV=test "
		if depends("cross-env") {
			environment = "cross-env BABEL_ENV=test "
		}
		// Scope the environment to the browserify command itself, which may follow
		// other commands, and add the transform right after its executable.
		instrumented := script[:match[2]] + environment + script[match[2]:match[3]] + " -t babelify" + script[match[3]:]
		coverage := name + ":coverage"
		return &coverageEdit{
			Build:   "browserify",
			Install: install("babelify", "@babel/core", "babel-plugin-istanbul"),
			Files: []fileEdit{
				{Path: "babel.config.json", Action: "create", Content: babelTestCoverage},
				{Path: "package.json", Action: "edit", Where: "add to scripts", Content: fmt.Sprintf("%q: %q\n", coverage, instrumented)},
			},
			Serve:            fmt.Sprintf("Serve the bundle from `npm run %s` in the job that runs Cypress, instead of `npm run %s`; other builds stay uninstrumented.", coverage, name),
			RequiresApproval: true,
		}
	}
	return nil
}

func firstExisting(root string, names ...string) string {
	for _, name := range names {
		if coverageFileExists(filepath.Join(root, name)) {
			return name
		}
	}
	return ""
}

func coverageFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func cypressCoverageEditReason(edit *coverageEdit) string {
	return fmt.Sprintf("No application source coverage observed, so Test Impact Analysis cannot be validated. ddtest recognized the %s build served to Cypress; coverage.edit in the JSON report holds the exact change: install (%s), %d file edit(s), and how to serve the instrumented build. It changes the application build and its dependencies, so present it to the user and apply it only with their approval.", edit.Build, edit.Install, len(edit.Files))
}
