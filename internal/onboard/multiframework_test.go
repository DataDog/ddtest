// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOnboardAllSupportedFrameworks(t *testing.T) {
	for _, name := range []string{"jest", "mocha", "vitest", "playwright", "cucumber", "cypress", "pytest", "rspec", "minitest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			language := "js"
			entry := name
			files := map[string]string{"package.json": `{"scripts":{"test":"` + name + `"}}`}
			switch name {
			case "pytest":
				files = map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n"}
				language = "python"
				entry = "uv run tox"
			case "rspec", "minitest":
				files = map[string]string{"Gemfile": "gem '" + name + "'\n"}
				language = "ruby"
				entry = "bundle exec rake"
			}
			for file, contents := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, file), []byte(contents), 0644))
			}
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("jobs:\n  tests:\n    steps:\n      - run: "+entry+"\n"), 0644))
			var output bytes.Buffer
			t.Chdir(root)
			require.NoError(t, Run(&output))
			require.Contains(t, output.String(), "languages: "+language)
			require.Contains(t, output.String(), "ddtest testdrive --framework "+name)
			require.Contains(t, output.String(), "Ask a human to connect Datadog")
			if language != "js" {
				require.NotContains(t, output.String(), "NODE_OPTIONS")
			}
			if name == "vitest" {
				require.Contains(t, output.String(), "DD_TRACE_ESM_IMPORT")
			}
			if name == "cypress" {
				require.Contains(t, output.String(), "setupNodeEvents")
			}
		})
	}
}
