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

func TestTroubleshootCucumberWithoutNYC(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"@cucumber/cucumber":"13.2.1"}}`), 0644))
	facts := Facts{MissingCoverage: true}

	advice := Troubleshoot(root, "Cucumber", facts)
	require.Len(t, advice, 1)
	require.Equal(t, MissingCoverageFinding, advice[0].Finding)
	require.Contains(t, advice[0].Text, "npm install --save-dev nyc")
	require.Empty(t, Troubleshoot(root, "Cucumber", Facts{}))
	require.Empty(t, Troubleshoot(root, "Jest", facts))

	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"@cucumber/cucumber":"13.2.1","nyc":"17.0.0"}}`), 0644))
	require.Empty(t, Troubleshoot(root, "Cucumber", facts))
}
