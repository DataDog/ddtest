package compatibility

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

func TestCucumberAdapterIntegration(t *testing.T) {
	cucumberBinary := requireEnv(t, "DDTEST_CUCUMBER_BINARY")
	nodeModules := requireEnv(t, "DDTEST_CUCUMBER_NODE_MODULES")
	cucumberVersion := requireEnv(t, "DDTEST_CUCUMBER_VERSION")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	t.Chdir(root)
	if err := os.Symlink(nodeModules, "node_modules"); err != nil {
		t.Fatal(err)
	}
	cucumberConfig := `module.exports = {
  default: {
    tags: 'not @excluded',
    require: ['features/support/**/*.js']
  }
}
`
	if strings.HasPrefix(cucumberVersion, "7.") {
		// Cucumber 7 profiles are CLI argument strings. Object-based profiles were
		// introduced later and are silently treated as empty by Cucumber 7.
		cucumberConfig = `module.exports = {
  default: "--require 'features/support/**/*.js' --tags 'not @excluded'"
}
`
	}
	files := map[string]string{
		"cucumber.js": cucumberConfig,
		"features/included.feature": `Feature: included
  Scenario: selected by the default profile
    Given a passing step
`,
		"features/unassigned.feature": `Feature: unassigned
  Scenario: must not run
    Given a failing step
`,
		"features/excluded.feature": `@excluded
Feature: excluded
  Scenario: filtered by the default profile
    Given a passing step
`,
		"features/support/steps.js": `const { Given } = require('@cucumber/cucumber')
Given('a passing step', function () {})
Given('a failing step', function () { throw new Error('unassigned file ran') })
`,
	}
	for filename, content := range files {
		writeFixture(t, root, filename, content)
	}

	configureFramework(shellCommand(cucumberBinary), "")
	cucumber := framework.NewCucumber()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	discovered, err := cucumber.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: cucumber.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"features/included.feature", "features/unassigned.feature"}
	if !slices.Equal(discovered, wantFiles) {
		t.Fatalf("discovered = %v", discovered)
	}
	if err := cucumber.RunTests(ctx, []string{"features/included.feature"}, nil); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}
