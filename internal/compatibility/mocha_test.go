package compatibility

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestMochaAdapterIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_MOCHA_NODE_MODULES")

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, ".mocharc.json", `{"spec":["test/**/*.spec.js"],"file":["setup.js"]}`)
	writeFixture(t, root, "setup.js", "global.ddtestSetup = true\n")
	writeFixture(t, root, "test/selected.spec.js", `const assert = require("assert"); describe("selected", () => { it("uses setup", () => assert.equal(global.ddtestSetup, true)) })`)
	writeFixture(t, root, "test/unselected.spec.js", `describe("unselected", () => { it("must not run", () => { throw new Error("unselected file ran") }) })`)
	t.Chdir(root)

	mocha := framework.NewMocha()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files, err := mocha.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"test/selected.spec.js", "test/unselected.spec.js"}
	requireFiles(t, files, want)
	if err := mocha.RunTests(ctx, []string{"test/selected.spec.js"}, nil); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}

	if err := os.Remove(filepath.Join(root, ".mocharc.json")); err != nil {
		t.Fatal(err)
	}
	files, err = mocha.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err != nil {
		t.Fatalf("default discovery failed: %v", err)
	}
	requireFiles(t, files, want)
}

func TestMochaAdapterCustomLocationAndCommandIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_MOCHA_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	mochaCommand := filepath.Join(nodeModules, ".bin", "mocha")
	wrapper := filepath.Join(root, "mocha-wrapper.sh")
	writeFixture(t, root, "mocha-wrapper.sh", "#!/bin/sh\nexport DDTEST_MOCHA_WRAPPER=preserved\nexec \"$@\"\n")
	if err := os.Chmod(wrapper, 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, ".mocharc.json", `{"spec":["test/**/*.spec.js"]}`)
	writeFixture(t, root, "spec/custom.spec.js", `const assert = require("assert"); describe("custom", () => { it("uses wrapper", () => assert.equal(process.env.DDTEST_MOCHA_WRAPPER, "preserved")) })`)
	t.Chdir(root)
	configureFramework(shellCommand(wrapper, mochaCommand), "spec/**/*.js")

	mocha := framework.NewMocha()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files, err := mocha.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	requireFiles(t, files, []string{"spec/custom.spec.js"})
	if err := mocha.RunTests(ctx, files, nil); err != nil {
		t.Fatalf("custom-command run failed: %v", err)
	}
}

func TestMochaActionPreloadIntegration(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_NODE_OPTIONS=%t", explicit), func(t *testing.T) {
			testMochaActionPreloadIntegration(t, explicit)
		})
	}
}

func testMochaActionPreloadIntegration(t *testing.T, explicit bool) {
	t.Helper()
	nodeModules := requireEnv(t, "DDTEST_MOCHA_NODE_MODULES")
	resetSettingsAfterTest(t)
	configureFramework("", "")

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "package.json", `{"devDependencies":{"mocha":"11"}}`)
	writeFixture(t, root, "test/action.spec.js", `const assert = require("assert"); describe("action preload", () => { it("loads for a test run", () => assert.strictEqual(global.ddtestTracerLoaded, true)) })`)
	marker := filepath.Join(root, "tracer-started")
	external := t.TempDir()
	preload := filepath.Join(external, "dd-trace", "ci", "init.js")
	writeFixture(t, external, "dd-trace/ci/init.js", fmt.Sprintf("require('fs').appendFileSync(%q, 'loaded\\n'); global.ddtestTracerLoaded = true;\n", marker))
	t.Chdir(root)
	t.Setenv("NODE_OPTIONS", "")
	t.Setenv("DD_TRACE_PACKAGE", "")
	t.Setenv("NODE_PATH", "")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	javascript := platform.NewJavaScript()
	require.Error(t, javascript.SanityCheck(ctx), "fixture must not have a project tracer")
	_, err := javascript.CreateTagsMap(ctx)
	require.NoError(t, err, "planning tags do not need a tracer")
	fw, err := javascript.DetectFramework()
	require.NoError(t, err)
	files, err := fw.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: fw.TestPattern()})
	require.NoError(t, err, "planning discovery does not need a tracer")
	requireFiles(t, files, []string{"test/action.spec.js"})

	t.Setenv("DD_TRACE_PACKAGE", preload)
	if explicit {
		t.Setenv("NODE_OPTIONS", "-r "+strconv.Quote(preload))
	}
	require.NoError(t, javascript.SanityCheck(ctx))
	_, err = javascript.CreateTagsMap(ctx)
	require.NoError(t, err)
	fw, err = javascript.DetectFramework()
	require.NoError(t, err)
	if explicit {
		require.Empty(t, fw.GetPlatformEnv(), "worker should inherit the customer's absolute preload")
	} else {
		require.Equal(t, "-r "+strconv.Quote(preload), fw.GetPlatformEnv()["NODE_OPTIONS"])
	}
	files, err = fw.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: fw.TestPattern()})
	require.NoError(t, err)
	requireFiles(t, files, []string{"test/action.spec.js"})
	require.NoFileExists(t, marker, "sanity checks, runtime tags, and discovery must not start tracing")

	require.NoError(t, fw.RunTests(ctx, files, nil))
	require.FileExists(t, marker, "the test worker must load the action's tracer")
}
