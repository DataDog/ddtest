package compatibility

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
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
