// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeVitestIdentityFixture(t *testing.T, path string) {
	t.Helper()
	var run validationRun
	readRunnerResults(path, "Vitest", &run)
	identities := []map[string]string{}
	for _, test := range run.Tests {
		name := test.Name
		if test.telemetryName != nil {
			name = *test.telemetryName
		}
		identities = append(identities, map[string]string{"file": test.File, "reportedName": test.Name, "name": test.Name, "telemetryName": name, "status": test.Status})
	}
	data, err := json.Marshal(identities)
	require.NoError(t, err)
	requireWriteFile(t, path+".identities.json", string(data))
}

func TestVitestCollectedIdentitiesIntegration(t *testing.T) {
	root := vitestIntegrationRoot(t)
	requireWriteFile(t, filepath.Join(root, "scrubber.mjs"), `
const scrub = task => { task.name = task.name.replaceAll('session:fixture', '<redacted>'); task.tasks?.forEach(scrub) };
export default class {
  onCollected(files) { files.forEach(scrub) }
  onTestModuleCollected(module) { scrub(module.task) }
};
`)
	requireWriteFile(t, filepath.Join(root, "original.test.js"), `
import {describe,test} from 'vitest';
describe('session:fixture', () => {
  test('session:fixture', () => {});
  test('session:fixture', () => {});
  test.skip('skipped session:fixture', () => {});
  describe('', () => { test('', () => {}); });
});
test('x'.repeat(17000), () => {});
test('x'.repeat(5000), () => {});
test('x'.repeat(4999) + '💡x', () => {});
`)
	output := filepath.Join(t.TempDir(), "results.json")
	reporter := filepath.Join(t.TempDir(), "identities.mjs")
	requireWriteFile(t, reporter, vitestIdentityReporter)
	// Keep the mutating reporter before the validation collectors.
	requireWriteFile(t, filepath.Join(root, "vitest.config.mjs"), fmt.Sprintf(`export default {test:{pool:'forks',reporters:['./scrubber.mjs',%q,'json'],outputFile:%q}}`, reporter, output))
	data, err := exec.CommandContext(t.Context(), "node", filepath.Join(root, "node_modules/vitest/vitest.mjs"), "run").CombinedOutput()
	require.NoError(t, err, string(data))
	var native, collected validationRun
	readRunnerResults(output, "Vitest", &native)
	readVitestResults(output, &collected)
	require.Empty(t, collected.ResultError)
	require.Len(t, collected.Tests, 7)
	require.Contains(t, native.Tests[0].Name, "<redacted>")
	require.Equal(t, strings.ReplaceAll(native.Tests[0].Name, "<redacted>", "session:fixture"), collected.Tests[0].Name)
	require.Equal(t, "session:fixture ", *collected.Tests[3].telemetryName)
	require.Equal(t, native.Tests[4].Name, collected.Tests[4].Name)
	require.GreaterOrEqual(t, len(collected.Tests[4].Name), 17000)
	require.Equal(t, strings.Repeat("x", 5000)+"...", *collected.Tests[4].telemetryName)
	require.Equal(t, strings.Repeat("x", 5000), *collected.Tests[5].telemetryName)
	require.Equal(t, strings.Repeat("x", 4999)+"\uFFFD...", *collected.Tests[6].telemetryName)
	// Missing, reordered or inconsistent supplemental evidence fails closed.
	identityPath := output + ".identities.json"
	identities, err := os.ReadFile(identityPath)
	require.NoError(t, err)
	for _, invalid := range []string{"[]", "null", "invalid", string(identities[:len(identities)-1]), strings.Replace(string(identities), `"status":"passed"`, `"status":"failed"`, 1), strings.Replace(string(identities), `"reportedName":`, `"wrongName":`, 1)} {
		requireWriteFile(t, identityPath, invalid)
		var run validationRun
		readVitestResults(output, &run)
		require.NotEmpty(t, run.ResultError)
	}
}
