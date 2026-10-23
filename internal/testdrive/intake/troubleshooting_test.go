// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestMissingCoverageFindingRequiresTestEventsAndNoCoveragePayload(t *testing.T) {
	events := msgp.AppendMapHeader(nil, 1)
	events = msgp.AppendString(events, "events")
	events = msgp.AppendArrayHeader(events, 1)
	events = appendEvent(events, "test", 10, 20, 100)

	server := serverWithCoverage(t, events)
	server.requests = server.requests[:1]
	facts, err := server.Facts()
	require.NoError(t, err)
	require.True(t, facts.MissingCoverage)

	server = serverWithCoverage(t, events, appendCoverage(nil, 10, 20, 100, "src/greet.js"))
	facts, err = server.Facts()
	require.NoError(t, err)
	require.False(t, facts.MissingCoverage)

	server = serverWithCoverage(t, events, appendCoverage(nil, 10, 20, 100))
	facts, err = server.Facts()
	require.NoError(t, err)
	require.False(t, facts.MissingCoverage) // Empty coverage has its own finding.
}

func TestTroubleshootMochaWithoutNYC(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "package.json")
	require.NoError(t, os.WriteFile(manifest, []byte(`{"devDependencies":{"mocha":"11.7.6"}}`), 0644))
	facts := Facts{MissingCoverage: true}

	advice := Troubleshoot(root, "Mocha", facts, CoverageSetup{})
	require.Len(t, advice, 1)
	require.Equal(t, MissingCoverageFinding, advice[0].Finding)
	require.Equal(t, "Install nyc for Mocha coverage", advice[0].Title)
	require.NotContains(t, advice[0].Text, "./node_modules/.bin/nyc ddtest")
	require.Contains(t, advice[0].Text, "Select the same NYC-wrapped command")
	require.Contains(t, advice[0].Text, "npm install --save-dev nyc")
	require.NotContains(t, advice[0].Text, "--command")
	require.Empty(t, Troubleshoot(root, "Mocha", Facts{}, CoverageSetup{}))
	for _, framework := range []string{"Jest", "Vitest"} {
		require.Empty(t, Troubleshoot(root, framework, facts, CoverageSetup{}))
	}

	require.NoError(t, os.WriteFile(manifest, []byte(`{"devDependencies":{"nyc":"17.0.0"}}`), 0644))
	advice = Troubleshoot(root, "Mocha", facts, CoverageSetup{})
	require.Len(t, advice, 1)
	require.Equal(t, "Install the declared nyc dependency", advice[0].Title)
	require.Contains(t, advice[0].Text, "Install the project's dependencies")

	require.NoError(t, os.WriteFile(manifest, []byte(`{"devDependencies":{"mocha":"11.7.6"}}`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", ".bin", "nyc"), nil, 0755))
	advice = Troubleshoot(root, "Mocha", facts, CoverageSetup{})
	require.Len(t, advice, 1)
	require.Equal(t, "Check NYC coverage configuration", advice[0].Title)
	require.NotContains(t, advice[0].Text, "./node_modules/.bin/nyc ddtest")
	require.Contains(t, advice[0].Text, "include/exclude")
	require.NotContains(t, advice[0].Text, "npm install")
}

// Cucumber keeps coverage tooling on the Cucumber command, so advice follows
// the command's own setup and never suggests wrapping testdrive in nyc.
func TestTroubleshootCucumberCoverageFollowsTheCommandSetup(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "package.json")
	require.NoError(t, os.WriteFile(manifest, []byte(`{"devDependencies":{"@cucumber/cucumber":"13.3.0"}}`), 0644))
	facts := Facts{MissingCoverage: true}
	for _, tc := range []struct {
		setup        CoverageSetup
		title, text  string
		withoutTexts []string
	}{
		{CoverageSetup{Parallel: true, Wrapper: "nyc"}, "Cucumber parallel workers send no coverage", "sends no suite coverage from Cucumber parallel workers", []string{"may not", "npm install"}},
		{CoverageSetup{Wrapper: "c8"}, "Use nyc instead of c8 for Cucumber coverage", "Do not wrap c8 or testdrive in nyc", []string{"npm install"}},
		{CoverageSetup{Wrapper: "nyc"}, "Check the command's nyc configuration", "already runs under nyc", []string{"npm install"}},
		{CoverageSetup{}, "Run the Cucumber command under nyc", "npm install --save-dev nyc", nil},
	} {
		advice := Troubleshoot(root, "Cucumber", facts, tc.setup)
		require.Len(t, advice, 1)
		require.Equal(t, MissingCoverageFinding, advice[0].Finding)
		require.Equal(t, tc.title, advice[0].Title)
		require.Contains(t, advice[0].Text, tc.text)
		require.NotContains(t, advice[0].Text, "nyc ddtest testdrive")
		for _, text := range tc.withoutTexts {
			require.NotContains(t, advice[0].Text, text)
		}
	}
	require.Empty(t, Troubleshoot(root, "Cucumber", Facts{}, CoverageSetup{Wrapper: "c8"}))

	require.NoError(t, os.WriteFile(manifest, []byte(`{"devDependencies":{"nyc":"17.1.0"}}`), 0644))
	require.Contains(t, Troubleshoot(root, "Cucumber", facts, CoverageSetup{})[0].Text, "Install the project's dependencies")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", ".bin", "nyc"), nil, 0755))
	require.NotContains(t, Troubleshoot(root, "Cucumber", facts, CoverageSetup{})[0].Text, "Install")
}

func TestMochaExistingBabelIstanbulAdvice(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "babel.config.cjs"), []byte(`module.exports={plugins:['istanbul']}`), 0600))
	advice := Troubleshoot(root, "Mocha", Facts{MissingCoverage: true}, CoverageSetup{})
	require.Len(t, advice, 1)
	require.Equal(t, "Check existing Babel Istanbul instrumentation", advice[0].Title)
	require.NotContains(t, advice[0].Text, "npm install")
}
