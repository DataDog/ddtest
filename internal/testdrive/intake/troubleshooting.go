// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"encoding/json"
	"github.com/DataDog/ddtest/internal/onboard"
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

// CoverageSetup is what the selected command already does for coverage.
type CoverageSetup struct {
	// Wrapper is the coverage tool already wrapping the test runner: nyc, c8 or empty.
	Wrapper string
	// Parallel is set when the runner executed tests in parallel workers.
	Parallel bool
}

// Troubleshoot returns advice only when the project provides evidence for it.
func Troubleshoot(repositoryRoot, framework string, facts Facts, setup CoverageSetup) []Advice {
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
	switch strings.ToLower(framework) {
	case "cucumber":
		return append(advices, troubleshootCucumberCoverage(repositoryRoot, setup))
	case "mocha":
		return append(advices, troubleshootMochaCoverage(repositoryRoot))
	case "cypress":
		return append(advices, Advice{
			Finding: MissingCoverageFinding,
			Title:   "Enable coverage collection for Cypress",
			Text:    "Collect Test Impact Analysis data by enabling Istanbul coverage in your test build: add babel-plugin-istanbul to Babel plugins or vite-plugin-istanbul to Vite plugins. Restart the dev server and rerun ddtest testdrive. Verify that window.__coverage__ contains source files in the tested page or component.",
			URL:     "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/#cypress",
		})
	}
	return advices
}

// Cucumber keeps its coverage tool on the Cucumber command, never around
// testdrive, so advise on the setup the command already has.
func troubleshootCucumberCoverage(repositoryRoot string, setup CoverageSetup) Advice {
	advice := Advice{Finding: MissingCoverageFinding, URL: "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/"}
	switch {
	case setup.Parallel:
		advice.Title = "Cucumber parallel workers send no coverage"
		advice.Text = "The selected dd-trace sends no suite coverage from Cucumber parallel workers, so Test Impact Analysis cannot be validated for this command. Keep the parallel CI command unchanged and report TIA as unvalidated; a command without --parallel can validate coverage separately."
	case setup.Wrapper == "c8":
		advice.Title = "Use nyc instead of c8 for Cucumber coverage"
		advice.Text = "c8 writes V8 coverage after the run; dd-trace reports per-test coverage only from Istanbul instrumentation. Run the Cucumber command under nyc (nyc cucumber-js) instead of c8, in CI and in testdrive. Do not wrap c8 or testdrive in nyc."
	case setup.Wrapper == "nyc":
		advice.Title = "Check the command's nyc configuration"
		advice.Text = "The Cucumber command already runs under nyc; do not add another nyc around it or around testdrive. Check nyc's include/exclude rules and that application code is loaded outside Cucumber's support paths. Native ESM also needs @istanbuljs/esm-loader-hook."
	default:
		advice.Title = "Run the Cucumber command under nyc"
		advice.Text = "Add nyc to the Cucumber command itself (nyc cucumber-js), in CI and in the command testdrive selects; do not wrap testdrive in nyc. Native ESM also needs @istanbuljs/esm-loader-hook."
		if info, err := os.Stat(filepath.Join(repositoryRoot, "node_modules", ".bin", "nyc")); err != nil || info.IsDir() {
			install := "From the project directory:\nnpm install --save-dev nyc\n"
			if declared, known := nycDeclared(repositoryRoot); known && declared {
				install = "NYC executable missing. Install the project's dependencies.\n"
			}
			advice.Text = install + advice.Text
		}
	}
	return advice
}

func troubleshootMochaCoverage(repositoryRoot string) Advice {
	if onboard.MochaBabelIstanbulConfigured(repositoryRoot) {
		return Advice{Finding: MissingCoverageFinding, Title: "Check existing Babel Istanbul instrumentation", Text: "Babel config already references the Istanbul plugin. Verify that the tested source passes through Babel and that include/exclude rules retain application files; do not add another NYC wrapper before checking the existing instrumentation. Rerun ddtest testdrive after correcting it.", URL: "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/"}
	}
	advice := Advice{
		Finding: MissingCoverageFinding,
		Title:   "Check NYC coverage configuration",
		Text:    "Testdrive preserves the selected command. If coverage is intended and the command does not already run under NYC, wrap it with the installed NYC; wrapping alone does not prove coverage. Check instrument:false, include/exclude rules and file extensions. NYC's require hook alone does not instrument native ESM; dd-trace's ESM loader enables tracing, not Istanbul coverage. Preserve coverage thresholds and report this limitation if a separate ESM instrumentation step is needed. Rerun ddtest testdrive after correcting instrumentation.",
		URL:     "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/",
	}
	info, err := os.Stat(filepath.Join(repositoryRoot, "node_modules", ".bin", "nyc"))
	if err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		return advice
	}
	advice.Title = "Install nyc for Mocha coverage"
	advice.Text = "From the project directory:\nnpm install --save-dev nyc\nnpx --no-install nyc -- mocha\nSelect the same NYC-wrapped command in CI and ddtest testdrive. Keep your testdrive options."
	if declared, known := nycDeclared(repositoryRoot); known && declared {
		advice.Title = "Install the declared nyc dependency"
		advice.Text = "NYC executable missing. Install the project's dependencies, then rerun:\nnpx --no-install nyc -- mocha\nSelect the same NYC-wrapped command in CI and ddtest testdrive. Keep your testdrive options."
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
