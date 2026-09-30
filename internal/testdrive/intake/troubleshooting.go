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

// Advice explains a likely cause of a finding and a concrete next step.
type Advice struct {
	Finding string
	Title   string
	Text    string
	URL     string
}

// Troubleshoot returns advice only when the project provides evidence for it.
func Troubleshoot(repositoryRoot, framework string, facts Facts) []Advice {
	if !facts.MissingCoverage || !strings.EqualFold(framework, "cucumber") || !nycAbsent(repositoryRoot) {
		return nil
	}
	return []Advice{{
		Finding: MissingCoverageFinding,
		Title:   "Cucumber needs nyc for coverage",
		Text:    "This project does not declare or install nyc locally. Add it with npm install --save-dev nyc, then run cucumber-js through nyc (for example, nyc cucumber-js) and repeat the testdrive.",
		URL:     "https://docs.datadoghq.com/tests/test_impact_analysis/setup/javascript/",
	}}
}

func nycAbsent(repositoryRoot string) bool {
	data, err := os.ReadFile(filepath.Join(repositoryRoot, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	for _, dependencies := range []map[string]json.RawMessage{
		manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies, manifest.PeerDependencies,
	} {
		if _, exists := dependencies["nyc"]; exists {
			return false
		}
	}
	_, err = os.Stat(filepath.Join(repositoryRoot, "node_modules", ".bin", "nyc"))
	return errors.Is(err, os.ErrNotExist)
}
