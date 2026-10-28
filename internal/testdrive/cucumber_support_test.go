package testdrive

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

// Also exercised in the existing Cucumber 7–13 adapter matrix: older CLI and
// newer formatter APIs must preserve the same World/hooks for feature controls.
func TestCucumberSupportMetadataIntegration(t *testing.T) {
	modules := os.Getenv("DDTEST_CUCUMBER_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_CUCUMBER_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, workspace.Copy(ctx, filepath.Dir(modules), root, skipIgnoredOutput))
	write := func(name, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), body)
	}
	write("package.json", `{"scripts":{"test":"cucumber-js"}}`)
	write("features/one.feature", "Feature: Support\n  Scenario: Original\n    Given original support works\n")
	write("features/support/world.js", `const {Before,setWorldConstructor}=require('@cucumber/cucumber');class World{constructor(){this.value='world'}};setWorldConstructor(World);Before(function(){if(this.value!=='world')throw Error('World missing');this.hook=true});`)
	write("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');Given('original support works',function(){if(!this.hook)throw Error('hook missing')})`)
	observer := filepath.Join(t.TempDir(), "support.cjs")
	require.NoError(t, os.WriteFile(observer, []byte(cucumberSupportObserver), 0600))
	metadata := filepath.Join(t.TempDir(), "support.json")
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	cli := filepath.Join(root, "node_modules/.bin/cucumber-js")
	run := func(args []string) {
		cmd := exec.CommandContext(ctx, node, append([]string{"--require", observer, cli}, args...)...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "NODE_OPTIONS=", "DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_ENABLED=false", "DDTEST_CUCUMBER_SUPPORT="+metadata, "CUCUMBER_PUBLISH_ENABLED=false")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	run([]string{"--format", observer})
	body, err := os.ReadFile(metadata)
	require.NoError(t, err)
	var support cucumberSupport
	require.NoError(t, json.Unmarshal(body, &support))
	require.Contains(t, append(support.RequirePaths, support.ImportPaths...), filepath.Join(root, "features/support/world.js"))
	// The plan/run adapter and its effective support discovery still support 7.
	// Isolated testdrive probes use --config, introduced in Cucumber 8.
	manifest, err := os.ReadFile(filepath.Join(modules, "@cucumber/cucumber/package.json"))
	require.NoError(t, err)
	var pkg struct{ Version string }
	require.NoError(t, json.Unmarshal(manifest, &pkg))
	major, _, known := releaseParts(pkg.Version)
	require.True(t, known)
	if major < 8 {
		t.Log("Legacy support metadata verified; isolated testdrive probes require Cucumber 8 or later")
		return
	}
	write("features/probe.feature.config.cjs", `module.exports={default:{}}`)
	write("features/probe.feature", "Feature: Support\n  Scenario: Probe\n    Given probe support works\n")
	write("features/probe.feature.steps.cjs", `const {Given}=require('@cucumber/cucumber');Given('probe support works',function(){if(this.value!=='world'||!this.hook)throw Error('effective support was lost')})`)
	drive := &Testdrive{repositoryRoot: root, command: cli, cucumberNode: node, cucumberExecutable: cli, cucumberSupport: &support}
	command, args, err := drive.cucumberProbeCommand(filepath.Join(root, "features/probe.feature"))
	require.NoError(t, err)
	require.Equal(t, node, command)
	run(append(args[1:], "--format", observer))
}
