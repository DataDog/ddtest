// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Snapshot of main's template at 83cb80ba36e20a751dcad5cb1ec57fda1a5b3a6a.
//
//go:embed testdata/github-main.md
var mainGitHubInstructions string

func TestPythonRubyOnboardMatchesMain(t *testing.T) {
	for _, fixture := range []struct{ language, framework, command, manifest, contents, bootstrap string }{
		{"python", "pytest", "python -m pytest", "pytest.ini", "[pytest]\n", pythonBootstrap},
		{"ruby", "rspec", "bundle exec rspec", "Gemfile", "gem 'rspec'\n", rubyBootstrap},
		{"ruby", "minitest", "bundle exec rake test", "Gemfile", "gem 'minitest'\n", rubyBootstrap},
	} {
		for _, configured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/configured=%t", fixture.framework, configured), func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(root, fixture.manifest), []byte(fixture.contents), 0644))
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
				action := ""
				if configured {
					action = "      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: " + fixture.language + "\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("jobs:\n  test:\n    steps:\n"+action+"      - run: "+fixture.command+"\n"), 0644))
				t.Chdir(root)
				expected := fmt.Sprintf("DDTest found %s, %s, and GitHub Actions.\n\nTest workflow(s):\n  - .github/workflows/ci.yml\n\n", fixture.language, fixture.framework)
				if configured {
					expected += "Datadog Test Optimization already appears in every detected test workflow.\n"
					expected += fmt.Sprintf("Run `ddtest testdrive --framework %s` to check the setup locally.\n", fixture.framework)
					expected += "After it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.\n"
				} else {
					text := strings.NewReplacer("__FRAMEWORK__", fixture.framework, "__LANGUAGE__", fixture.language, "__BOOTSTRAP__", fixture.bootstrap, "__TRACER_SETTING__", fixture.language+"-tracer-version: '' # Latest release").Replace(mainGitHubInstructions)
					text = strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+fixture.framework)
					expected += strings.TrimSpace(text) + "\n"
				}
				for _, run := range []func(*bytes.Buffer) error{
					func(output *bytes.Buffer) error { return Run(output) },
				} {
					var output bytes.Buffer
					require.NoError(t, run(&output))
					require.Equal(t, expected, output.String())
				}
			})
		}
	}
}
