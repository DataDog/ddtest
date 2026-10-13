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

func TestTroubleshootFrameworkWithoutNYC(t *testing.T) {
	for _, tc := range []struct {
		framework, manifest string
	}{
		{"Cucumber", `{"devDependencies":{"@cucumber/cucumber":"13.2.1"}}`},
		{"Mocha", `{"devDependencies":{"mocha":"11.7.6"}}`},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			root := t.TempDir()
			manifest := filepath.Join(root, "package.json")
			require.NoError(t, os.WriteFile(manifest, []byte(tc.manifest), 0644))
			facts := Facts{MissingCoverage: true}

			advice := Troubleshoot(root, tc.framework, facts)
			require.Len(t, advice, 1)
			require.Equal(t, MissingCoverageFinding, advice[0].Finding)
			wantTitle := tc.framework + " needs nyc for coverage"
			if tc.framework == "Mocha" {
				wantTitle = "Install nyc for Mocha coverage"
				require.NotContains(t, advice[0].Text, "./node_modules/.bin/nyc ddtest")
				require.Contains(t, advice[0].Text, "Testdrive will use NYC automatically")
			}
			require.Equal(t, wantTitle, advice[0].Title)
			require.Contains(t, advice[0].Text, "npm install --save-dev nyc")
			require.Contains(t, advice[0].Text, "ddtest testdrive")
			require.NotContains(t, advice[0].Text, "--command")
			require.Empty(t, Troubleshoot(root, tc.framework, Facts{}))
			for _, framework := range []string{"Jest", "Vitest", "Cypress"} {
				require.Empty(t, Troubleshoot(root, framework, facts))
			}

			require.NoError(t, os.WriteFile(manifest, []byte(`{"devDependencies":{"nyc":"17.0.0"}}`), 0644))
			advice = Troubleshoot(root, tc.framework, facts)
			require.Len(t, advice, 1)
			require.Equal(t, "Install the declared nyc dependency", advice[0].Title)
			require.Contains(t, advice[0].Text, "Install the project's dependencies")

			require.NoError(t, os.WriteFile(manifest, []byte(tc.manifest), 0644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", ".bin", "nyc"), nil, 0755))
			advice = Troubleshoot(root, tc.framework, facts)
			require.Len(t, advice, 1)
			wantTitle = "Run testdrive through nyc for coverage"
			if tc.framework == "Mocha" {
				wantTitle = "Check NYC coverage configuration"
				require.NotContains(t, advice[0].Text, "./node_modules/.bin/nyc ddtest")
			}
			require.Equal(t, wantTitle, advice[0].Title)
			require.Contains(t, advice[0].Text, "ddtest testdrive")
			require.Contains(t, advice[0].Text, "include/exclude")
			require.NotContains(t, advice[0].Text, "npm install")
			require.Empty(t, Troubleshoot(root, tc.framework, Facts{}))
		})
	}
}
