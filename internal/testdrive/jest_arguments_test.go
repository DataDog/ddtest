// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestJestArgumentsReplaceExpandedSelectionsAndOutputs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required to execute the argument preload")
	}
	root := t.TempDir()
	main := filepath.Join(root, "node_modules/jest/bin/jest.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(main), 0700))
	requireWriteFile(t, main, `console.log(JSON.stringify(process.argv.slice(2)))`)
	cli := filepath.Join(root, "node_modules/jest-cli")
	require.NoError(t, os.MkdirAll(cli, 0700))
	requireWriteFile(t, filepath.Join(cli, "package.json"), `{"name":"jest-cli","main":"index.js"}`)
	requireWriteFile(t, filepath.Join(cli, "index.js"), `exports.yargsOptions={config:{alias:'c',type:'string'},runInBand:{type:'boolean'},selectProjects:{type:'array'},ignoreProjects:{type:'array'},shard:{type:'string'},watchman:{type:'boolean'},testPathPatterns:{type:'array'},runTestsByPath:{type:'boolean'},testNamePattern:{type:'string'},coverageThreshold:{type:'string'},coverageDirectory:{type:'string'},outputFile:{type:'string'}}`)
	for _, tc := range []struct {
		name, probe string
		args, want  []string
	}{
		{"expanded script flags", "", []string{"--outputFile=customer.json", "--config", "a config.cjs", "--output-file", "temporary.json", "one.test.js"}, []string{"--config", "a config.cjs", "one.test.js", "--coverageDirectory", "coverage", "--outputFile", "temporary.json"}},
		{"probe positional and option values", "probe.test.js", []string{"one.test.js", "--runInBand", "--config", "a config.cjs", "--selectProjects", "A", "--testPathPatterns", "old-pattern", "--testNamePattern", "old name", "--coverageThreshold", "old", "--shard=2/2"}, []string{"--runInBand", "--config", "a config.cjs", "--selectProjects", "A", "--coverageDirectory", "coverage", "--outputFile", "temporary.json", "--coverageThreshold", "{}", "--testNamePattern", "^ddtest validation probe$", "--runTestsByPath", "probe.test.js"}},
		{"project selectors replaced after script expansion", "probe.test.js", []string{"--selectProjects", "A", "B", "--ignoreProjects", "A", "--shard", "2/2", "--coverageThreshold={}", "--coverageThreshold", "old"}, []string{"--coverageDirectory", "coverage", "--outputFile", "temporary.json", "--selectProjects", "A", "--coverageThreshold", "{}", "--testNamePattern", "^ddtest validation probe$", "--runTestsByPath", "probe.test.js"}},
		{"native separator preserved", "", []string{"--", "--outputFile=literal-file"}, []string{"--coverageDirectory", "coverage", "--outputFile", "temporary.json", "--", "--outputFile=literal-file"}},
		{"native separator replaced for probe", "probe.test.js", []string{"--config", "a config.cjs", "--", "one.test.js"}, []string{"--config", "a config.cjs", "--coverageDirectory", "coverage", "--outputFile", "temporary.json", "--coverageThreshold", "{}", "--testNamePattern", "^ddtest validation probe$", "--runTestsByPath", "probe.test.js"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			directory := t.TempDir()
			request := jestArgumentRequest{Coverage: "coverage", Output: "temporary.json", Probe: tc.probe}
			if tc.name == "project selectors replaced after script expansion" {
				request.Project = "A"
			}
			require.NoError(t, redirectJestArguments(directory, request, env))
			cmd := exec.Command(node, append([]string{main}, tc.args...)...)
			cmd.Env = append(os.Environ(), "NODE_OPTIONS="+env["NODE_OPTIONS"])
			data, err := cmd.CombinedOutput()
			require.NoError(t, err, string(data))
			var actual []string
			require.NoError(t, json.Unmarshal(data, &actual), string(data))
			index := slices.Index(actual, "--watchman")
			require.GreaterOrEqual(t, index, 0)
			require.Equal(t, "false", actual[index+1])
			actual = append(actual[:index], actual[index+2:]...)
			require.Equal(t, tc.want, actual)
			require.Equal(t, tc.probe != "", probeThresholdOverrideApplied(directory))
		})
	}
}
