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

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// observedWrappers lists the configuration wrappers the observer left in directory.
func observedWrappers(t *testing.T, directory string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, "ddtest-observed-*"))
	require.NoError(t, err)
	return matches
}

func TestCypressObserverKeepsWrappersInsideTheCopiedCheckout(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	config := "module.exports={e2e:{}}"
	source, copied, outside := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{source, copied, outside} {
		requireWriteFile(t, filepath.Join(root, "cypress.config.cjs"), config)
	}
	observer := filepath.Join(t.TempDir(), "observer.cjs")
	requireWriteFile(t, observer, cypressObserver)
	cli := filepath.Join(copied, "node_modules/cypress/bin/cypress")
	require.NoError(t, os.MkdirAll(filepath.Dir(cli), 0700))
	requireWriteFile(t, cli, `const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path')
const args=process.argv.slice(3),real=fs.realpathSync
assert.equal(real(args[args.lastIndexOf('--project')+1]),real(process.cwd()))
assert.equal(real(path.dirname(args[args.lastIndexOf('--config-file')+1])),real(process.cwd()))`)
	run := func(t *testing.T, args ...string) (string, string, error) {
		directory := t.TempDir()
		command := exec.CommandContext(t.Context(), node, append([]string{"--require", observer, cli, "run"}, args...)...)
		command.Dir = copied
		command.Env = append(os.Environ(), "NODE_OPTIONS=", "DDTEST_CYPRESS_DIRECTORY="+directory, "DDTEST_CYPRESS_CHECKOUT="+copied, "DDTEST_CYPRESS_SOURCE_ROOT="+source)
		output, err := command.CombinedOutput()
		return directory, string(output), err
	}

	t.Run("paths into the original checkout", func(t *testing.T) {
		directory, output, err := run(t, "--project", source, "--config-file", filepath.Join(source, "cypress.config.cjs"))
		require.NoError(t, err, output)
		require.Empty(t, observedWrappers(t, source), "no wrapper beside the customer's configuration")
		require.Len(t, observedWrappers(t, copied), 1)
		require.NoError(t, removeCypressWrappers(directory))
		require.Empty(t, observedWrappers(t, copied))
	})
	t.Run("configuration outside the checkout", func(t *testing.T) {
		directory, output, err := run(t, "-C", filepath.Join(outside, "cypress.config.cjs"))
		require.Error(t, err, output)
		require.Contains(t, output, "Cypress configuration file resolves outside checkout")
		require.Empty(t, observedWrappers(t, outside))
		require.NoFileExists(t, filepath.Join(directory, "wrappers"))
	})
}

func TestCypressAPIKeepsWrappersInsideTheCopiedCheckout(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	source, copied, outside := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{source, copied, outside} {
		requireWriteFile(t, filepath.Join(root, "cypress.config.cjs"), "module.exports={e2e:{}}")
	}
	directory := t.TempDir()
	requireWriteFile(t, filepath.Join(directory, "api.cjs"), cypressAPI)
	requireWriteFile(t, filepath.Join(directory, "observer.cjs"), cypressObserver)
	requireWriteFile(t, filepath.Join(copied, "package.json"), `{}`)
	module := filepath.Join(copied, "node_modules/cypress")
	require.NoError(t, os.MkdirAll(module, 0700))
	requireWriteFile(t, filepath.Join(module, "index.js"), `exports.run=async options=>{
		const assert=require('assert'),fs=require('fs'),path=require('path')
		assert.equal(fs.realpathSync(options.project),fs.realpathSync(process.cwd()))
		assert.equal(fs.realpathSync(path.dirname(options.configFile)),fs.realpathSync(process.cwd()))
		return {totalFailed:0,runs:[]}
	}`)
	for _, tc := range []struct {
		name, configFile, project string
		refused                   bool
	}{
		{"paths into the original checkout", filepath.Join(source, "cypress.config.cjs"), source, false},
		{"configuration outside the checkout", filepath.Join(outside, "cypress.config.cjs"), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := map[string]any{"configFile": tc.configFile}
			if tc.project != "" {
				options["project"] = tc.project
			}
			data, err := json.Marshal(onboard.CypressCommand{Action: "cypress-io/github-action@v7", Options: options})
			require.NoError(t, err)
			plan := filepath.Join(directory, "plan.json")
			require.NoError(t, os.WriteFile(plan, data, 0600))
			require.NoError(t, os.RemoveAll(filepath.Join(directory, "wrappers")))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, node, filepath.Join(directory, "api.cjs"), plan)
			command.Dir = copied
			command.Env = append(os.Environ(), "NODE_OPTIONS=", "DDTEST_CYPRESS_DIRECTORY="+directory, "DDTEST_CYPRESS_CHECKOUT="+copied, "DDTEST_CYPRESS_SOURCE_ROOT="+source)
			output, err := command.CombinedOutput()
			require.Empty(t, observedWrappers(t, source))
			require.Empty(t, observedWrappers(t, outside))
			if tc.refused {
				require.Error(t, err, string(output))
				failure, _ := os.ReadFile(filepath.Join(directory, "execution-error.txt"))
				require.Contains(t, string(output)+string(failure), "Cypress configuration file resolves outside checkout")
				return
			}
			require.NoError(t, err, string(output))
			wrappers, err := os.ReadFile(filepath.Join(directory, "wrappers"))
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(strings.TrimSpace(string(wrappers)), copied) || strings.Contains(string(wrappers), filepath.Base(copied)), string(wrappers))
			require.NoError(t, removeCypressWrappers(directory))
			require.Empty(t, observedWrappers(t, copied))
		})
	}
}
