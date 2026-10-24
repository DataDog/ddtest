package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratedSetupWorkflowsActionlint(t *testing.T) {
	actionlint := os.Getenv("DDTEST_ACTIONLINT")
	if actionlint == "" {
		t.Skip("set DDTEST_ACTIONLINT to validate every generated QA workflow")
	}
	for _, tc := range []struct {
		name, step string
		crlf       bool
	}{
		{"block", "      - name: Tests\n        run: |\n          mocha\n", false},
		{"unnamed block", "      - run: |\n          mocha\n", false},
		{"existing coverage block", "      - run: |\n          npx nyc -- mocha\n", false},
		{"folded", "      - run: >\n          npm\n          test\n", false},
		{"four-column sequence", "      -   name: Tests\n          run: mocha\n", false},
		{"assignments", "      - run: CI=true npm test\n", false},
		{"step environment", "      - run: mocha\n        env:\n          LANG: en_US.utf8\n          NODE_OPTIONS: --conditions=customer\n", false},
		{"comments", "      # This comment describes the test\n      - run: mocha # Keep this inline comment\n", false},
		{"foot comment", "      - name: Tests\n        run: mocha\n        # This comment describes the test\n      - run: echo done\n", false},
		{"CRLF", "      # This comment describes the test\n      - run: mocha\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := "name: CI\non: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n" + tc.step
			if tc.crlf {
				workflow = strings.ReplaceAll(workflow, "\n", "\r\n")
			}
			root := newJestRepository(t, workflow)
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
			if tc.crlf {
				require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\r\n"), 0600))
			}
			for pass := 0; pass < 2; pass++ {
				changes, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
				require.NoError(t, err)
				if pass > 0 {
					require.Empty(t, changes)
				}
				path := filepath.Join(root, ".github/workflows/test.yml")
				command := exec.Command(actionlint, "-shellcheck=", "-pyflakes=", path)
				output, err := command.CombinedOutput()
				require.NoError(t, err, string(output))
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				if tc.crlf {
					require.NotContains(t, strings.ReplaceAll(string(data), "\r\n", ""), "\n")
					ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
					require.NoError(t, err)
					require.Equal(t, "node_modules/\r\n/.testoptimization/\r\n/.nyc_output/\r\n", string(ignore), "the CRLF case wraps Mocha in nyc, so its output is ignored too")
				}
				if tc.name == "comments" || tc.crlf {
					require.Less(t, strings.Index(string(data), "Configure Datadog"), strings.Index(string(data), "This comment describes the test"))
				}
				if strings.Contains(tc.step, "#") {
					require.Equal(t, 1, strings.Count(string(data), "This comment describes the test"), string(data))
				}
			}
		})
	}
}

func TestApplyIgnoreFormsAndPackageJSONCharacters(t *testing.T) {
	for _, entry := range []string{"", "/.testoptimization", ".testoptimization", "/.testoptimization/", ".testoptimization/"} {
		root := newJestRepository(t, ordinaryVitestCI)
		writeScripts(t, root, map[string]string{"build": "tsc"})
		require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(entry), 0600))
		_, err := applyVitestSetup(root, "6.20.1")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
		require.NoError(t, err)
		if entry == "" {
			require.Equal(t, "/.testoptimization/\n", string(data))
		} else {
			require.Equal(t, entry, string(data))
		}
	}
	root := scopedReviewRepository(t, "webdriverio", "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data = []byte(strings.Replace(string(data), "wdio run wdio.conf.cjs", `wdio run wdio.conf.cjs --mochaOpts.grep 'a&&b<>c'`, 1))
	require.NoError(t, os.WriteFile(path, data, 0600))
	_, err = ApplyJavaScriptSetup(root, "webdriverio", "6.20.1", "datadoghq.com", "")
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "a&&b<>c")
	require.NotContains(t, string(data), `\u0026`)
	require.NotContains(t, string(data), `\u003c`)
}

func TestGeneratedSetupWorkflowVariantsActionlint(t *testing.T) {
	actionlint := os.Getenv("DDTEST_ACTIONLINT")
	if actionlint == "" {
		t.Skip("set DDTEST_ACTIONLINT")
	}
	for _, framework := range []string{"mocha", "cucumber", "webdriverio"} {
		t.Run(framework, func(t *testing.T) {
			workflow := "name: CI\non: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix: {node: ['18.0.0', '18.19.0', '24.14.1']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n"
			root := newJestRepository(t, workflow)
			pkg := `{"type":"module","scripts":{"test":"nyc -- mocha test.mjs"},"devDependencies":{"nyc":"17.1.0"}}`
			if framework != "mocha" {
				root = scopedReviewRepository(t, framework, workflow)
				data, err := os.ReadFile(filepath.Join(root, "package.json"))
				require.NoError(t, err)
				pkg = string(data)
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0600))
			_, err := ApplyJavaScriptSetup(root, framework, "5.132.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			path := filepath.Join(root, ".github/workflows/test.yml")
			output, err := exec.Command(actionlint, "-shellcheck=", "-pyflakes=", path).CombinedOutput()
			require.NoError(t, err, string(output))
		})
	}
	t.Run("SHA pin", func(t *testing.T) {
		root := newJestRepository(t, "name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - uses: "+setupActionV3+"\n        with:\n          languages: js\n          api_key: ${{ secrets.DD_API_KEY }}\n          site: datadoghq.com\n          js-tracer-version: 6.20.1\n      - run: npx vitest run\n        env:\n          NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'\n")
		_, err := ApplyJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		path := filepath.Join(root, ".github/workflows/test.yml")
		output, err := exec.Command(actionlint, "-shellcheck=", "-pyflakes=", path).CombinedOutput()
		require.NoError(t, err, string(output))
		changes, err := ApplyJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		require.Empty(t, changes)
	})
	t.Run("four space file", func(t *testing.T) {
		root := newJestRepository(t, "name: CI\non: push\njobs:\n    test:\n        runs-on: ubuntu-latest\n        steps:\n            -   uses: actions/setup-node@v4\n                with:\n                    node-version: '24.14.1'\n            -   run: mocha\n")
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"nyc":"17.1.0"}}`), 0600))
		_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		path := filepath.Join(root, ".github/workflows/test.yml")
		output, err := exec.Command(actionlint, "-shellcheck=", "-pyflakes=", path).CombinedOutput()
		require.NoError(t, err, string(output))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "                with:\n                    languages: js")
	})
}
