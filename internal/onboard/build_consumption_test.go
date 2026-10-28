package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newBuildRepository models unjs/defu: CI builds with unbuild, then runs
// Vitest, and package.json points its entry points at the build output.
func newBuildRepository(t *testing.T, buildScript string, files map[string]string) string {
	t.Helper()
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - run: pnpm build
      - run: pnpm vitest run
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pkg","main":"./lib/pkg.cjs","module":"./dist/pkg.mjs","scripts":{"build":`+`"`+buildScript+`"`+`},"devDependencies":{"vitest":"4.1.0"}}`), 0600))
	files["src/pkg.ts"] = "export const value = 1\n"
	for name, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0600))
	}
	return root
}

func vitestPrerequisites(t *testing.T, root string) ([]string, ValidationScope) {
	t.Helper()
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1, "%+v", scope)
	var commands []string
	for _, build := range scope.Commands[0].Prerequisites {
		commands = append(commands, build.Command)
	}
	return commands, scope
}

func TestCIBuildIsReplayedOnlyWhenTestsReachItsOutputs(t *testing.T) {
	root := newBuildRepository(t, "unbuild", map[string]string{
		"test/pkg.test.ts": "import { value } from \"../src/pkg\"\nimport { expect, it } from \"vitest\"\nit(\"ignores\", () => expect([\"node_modules\", \"dist\"]).toContain(\"dist\"))\n",
	})
	prerequisites, scope := vitestPrerequisites(t, root)
	require.Empty(t, prerequisites, "tests import ../src only")
	require.True(t, strings.Contains(strings.Join(scope.Review, "\n"), "CI build is not replayed: no test, test configuration or self-reference reaches its outputs (dist, lib): unbuild"), "%v", scope.Review)

	for name, test := range map[string]string{
		"relative import":    "import { value } from \"../dist/pkg.mjs\"\n",
		"self-reference":     "import { value } from \"pkg\"\n",
		"committed main":     "const pkg = require(\"../lib/pkg.cjs\")\n",
		"path to an output":  "import { join } from \"node:path\"\nconst cli = join(__dirname, \"..\", \"dist\")\n",
		"mocked output path": "vi.mock(\"../dist/pkg.mjs\")\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newBuildRepository(t, "unbuild", map[string]string{"test/pkg.test.ts": test})
			prerequisites, _ := vitestPrerequisites(t, root)
			require.Equal(t, []string{"unbuild"}, prerequisites)
		})
	}
	t.Run("runner configuration", func(t *testing.T) {
		root := newBuildRepository(t, "unbuild", map[string]string{
			"test/pkg.test.ts":  "import { value } from \"../src/pkg\"\n",
			"vitest.config.mts": "export default { resolve: { alias: { pkg: \"./dist/pkg.mjs\" } } }\n",
		})
		prerequisites, _ := vitestPrerequisites(t, root)
		require.Equal(t, []string{"unbuild"}, prerequisites)
	})
	t.Run("unknown output location", func(t *testing.T) {
		root := newBuildRepository(t, "unbuild", map[string]string{
			"test/pkg.test.ts": "import { value } from \"../src/pkg\"\n",
			"build.config.ts":  "export default { outDir: process.env.OUT }\n",
		})
		prerequisites, _ := vitestPrerequisites(t, root)
		require.Equal(t, []string{"unbuild"}, prerequisites, "a non-literal outDir hides where outputs go")
	})
	t.Run("tsc without outDir", func(t *testing.T) {
		root := newBuildRepository(t, "tsc", map[string]string{
			"test/pkg.test.ts": "import { value } from \"../src/pkg\"\n",
			"tsconfig.json":    "{\"compilerOptions\": {\"target\": \"ES2022\"}}\n",
		})
		prerequisites, _ := vitestPrerequisites(t, root)
		require.Equal(t, []string{"tsc"}, prerequisites, "tsc emits beside the sources tests import")
	})
}

func TestTsconfigOutDirAllowsCommentsAndTrailingCommas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tsconfig.json")
	require.NoError(t, os.WriteFile(path, []byte("{\n  // emitted code\n  \"compilerOptions\": {\"outDir\": \"build/out\", /* note */ },\n}\n"), 0600))
	outDir, ok := tsconfigOutDir(path)
	require.True(t, ok)
	require.Equal(t, "build/out", outDir)
}

func TestBuildConsumptionNeedsSourceToInspect(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pkg"}`), 0600))
	require.True(t, consumersFound(root, []string{filepath.Join(root, "dist")}, "pkg"), "nothing inspected rules nothing out")
}

func TestBuildConsumptionResolvesWorkingDirectoryAndPackageImports(t *testing.T) {
	for name, test := range map[string]string{
		"cwd-rooted output path": "import { resolve } from \"node:path\"\nconst cli = resolve(process.cwd(), \"dist/pkg.mjs\")\n",
		"cwd-rooted output dir":  "const out = require(\"node:path\").resolve(process.cwd(), \"dist\")\n",
		"package directory":      "const pkg = require(\"..\")\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newBuildRepository(t, "unbuild", map[string]string{"test/pkg.test.ts": test})
			prerequisites, _ := vitestPrerequisites(t, root)
			require.Equal(t, []string{"unbuild"}, prerequisites)
		})
	}
	t.Run("directory without its own manifest", func(t *testing.T) {
		root := newBuildRepository(t, "unbuild", map[string]string{"test/pkg.test.ts": "import { value } from \"../src\"\n", "src/index.ts": "export * from './pkg'\n"})
		prerequisites, _ := vitestPrerequisites(t, root)
		require.Empty(t, prerequisites, "../src resolves to sources, not to the package entry points")
	})
}

func TestBuildConsumptionIncludesTheTestInvocation(t *testing.T) {
	root := newBuildRepository(t, "tsc", map[string]string{
		"test/pkg.test.ts": "import { value } from \"../src/pkg\"\n",
		"tsconfig.json":    "{\"compilerOptions\": {\"outDir\": \"out\"}}\n",
	})
	build := BuildCommand{Directory: ".", Command: "tsc"}
	consumed, outputs := buildConsumedByTests(root, build, TestCommand{Directory: ".", Command: "mocha test"})
	require.False(t, consumed)
	require.Equal(t, []string{"out"}, outputs)

	consumed, _ = buildConsumedByTests(root, build, TestCommand{Directory: ".", Command: "mocha out/test"})
	require.True(t, consumed, "the test command runs the compiled tests")
	consumed, _ = buildConsumedByTests(root, build, TestCommand{Directory: ".", Command: "mocha --spec=out/test/**/*.js"})
	require.True(t, consumed, "a flag value names the output")

	manifest := `{"name":"pkg","scripts":{"test":"mocha out/test"}}`
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0600))
	consumed, _ = buildConsumedByTests(root, build, TestCommand{Directory: ".", Command: "npm test", packageScripts: []packageScript{{Directory: ".", Name: "test", Leaf: true}}})
	require.True(t, consumed, "the package script the command runs names the output")
}
