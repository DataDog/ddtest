// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const MissingCoverageFinding = "missing-coverage"
const MissingCoverageContext = "No per-test code coverage collected. Test Impact Analysis is not set up correctly."

// Advice explains a likely cause of a finding and a concrete next step.
type Advice struct {
	Finding string
	Title   string
	Text    string
	URL     string
}

// Troubleshoot returns advice only when the project provides evidence for it.
func Troubleshoot(repositoryRoot, framework string, facts Facts) []Advice {
	var advices []Advice
	if facts.BrowserCoverageUnavailable {
		advices = append(advices, Advice{
			Finding: BrowserCoverageFinding,
			Title:   "Run Node test projects separately for coverage",
			Text:    "Use a Vitest configuration containing only Node test projects. Select it with --framework vitest --vitest-config <config> when running ddtest testdrive to collect Test Impact Analysis coverage for supported Node projects.",
		})
	}
	if !facts.MissingCoverage {
		return advices
	}
	var name string
	switch strings.ToLower(framework) {
	case "cucumber":
		name = "Cucumber"
	case "mocha":
		return append(advices, troubleshootMochaCoverage(repositoryRoot))
	case "cypress":
		return append(advices, Advice{
			Finding: MissingCoverageFinding,
			Title:   "Enable coverage collection for Cypress",
			Text:    "Collect Test Impact Analysis data by enabling Istanbul coverage in your test build: add babel-plugin-istanbul to Babel plugins or vite-plugin-istanbul to Vite plugins. Restart the dev server and rerun ddtest testdrive. Verify that window.__coverage__ contains source files in the tested page or component.",
			URL:     "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/#cypress",
		})
	default:
		return advices
	}
	advice := Advice{
		Finding: MissingCoverageFinding,
		Title:   "Run testdrive through nyc for coverage",
		Text:    "From the project directory:\n./node_modules/.bin/nyc ddtest testdrive\nKeep your testdrive options. Still no coverage? Check NYC's include/exclude rules.",
		URL:     "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/",
	}
	nycPath := filepath.Join(repositoryRoot, "node_modules", ".bin", "nyc")
	info, statErr := os.Stat(nycPath)
	if statErr == nil && !info.IsDir() {
		return append(advices, advice)
	}
	declared, known := nycDeclared(repositoryRoot)
	if known && !declared && errors.Is(statErr, os.ErrNotExist) {
		advice.Title = name + " needs nyc for coverage"
		advice.Text = "From the project directory:\nnpm install --save-dev nyc\n./node_modules/.bin/nyc ddtest testdrive\nKeep your testdrive options."
	} else if known && declared && errors.Is(statErr, os.ErrNotExist) {
		advice.Title = "Install the declared nyc dependency"
		advice.Text = "NYC executable missing. Install the project's dependencies, then run:\n./node_modules/.bin/nyc ddtest testdrive\nKeep your testdrive options."
	} else {
		advice.Text += "\nNYC missing? npm install --save-dev nyc"
	}
	return append(advices, advice)
}

func troubleshootMochaCoverage(repositoryRoot string) Advice {
	advice := Advice{
		Finding: MissingCoverageFinding,
		Title:   "Check NYC coverage configuration",
		Text:    "Testdrive uses NYC automatically. Check NYC's include/exclude rules and file extensions, then rerun ddtest testdrive with the same options.",
		URL:     "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/",
	}
	info, err := os.Stat(filepath.Join(repositoryRoot, "node_modules", ".bin", "nyc"))
	if err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		return advice
	}
	advice.Title = "Install nyc for Mocha coverage"
	advice.Text = "From the project directory:\nnpm install --save-dev nyc\nddtest testdrive\nTestdrive will use NYC automatically. Keep your testdrive options."
	if declared, known := nycDeclared(repositoryRoot); known && declared {
		advice.Title = "Install the declared nyc dependency"
		advice.Text = "NYC executable missing. Install the project's dependencies, then rerun:\nddtest testdrive\nTestdrive will use NYC automatically. Keep your testdrive options."
	}
	return advice
}

func nycDeclared(repositoryRoot string) (declared, known bool) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot, "package.json"))
	if err != nil {
		return false, false
	}
	var manifest struct {
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false, false
	}
	for _, dependencies := range []map[string]json.RawMessage{
		manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies, manifest.PeerDependencies,
	} {
		if _, exists := dependencies["nyc"]; exists {
			return true, true
		}
	}
	return false, true
}
