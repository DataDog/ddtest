package testdrive

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestCypressAPIPreservesOptionsHooksAndCleansStartup(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable")
	}
	for _, lateFailure := range []string{"false", "true", "ignore-term"} {
		t.Run("startup="+lateFailure, func(t *testing.T) {
			root := t.TempDir()
			module := filepath.Join(root, "node_modules", "cypress")
			require.NoError(t, os.MkdirAll(module, 0700))
			files := map[string]string{
				"package.json":       `{}`,
				"api.cjs":            cypressAPI,
				"observer.cjs":       cypressObserver,
				"children.cjs":       processChildren,
				"server.cjs":         `const fs=require('fs');fs.writeFileSync('started',String(process.pid));process.on('SIGTERM',()=>{fs.writeFileSync('stopped','yes');if(process.env.DDTEST_API_LATE_START_FAILURE!=='ignore-term')process.exit(0)});if(process.env.DDTEST_API_LATE_START_FAILURE==='exit-zero')process.exit(0);if(process.env.DDTEST_API_LATE_START_FAILURE==='ignore-term'){process.removeAllListeners('SIGTERM');process.on('SIGTERM',()=>{})};setInterval(()=>{if(process.env.DDTEST_API_LATE_START_FAILURE==='true'&&fs.existsSync('api-started'))process.exit(2)},10)`,
				"cypress.config.cjs": `module.exports={e2e:{setupNodeEvents(on){on('after:run',()=>require('fs').writeFileSync('user-hook','called'))}}}`,
				"node_modules/cypress/index.js": `exports.run=async options=>{
					const fs=require('fs'),assert=require('assert')
					assert.equal(options.browser,'firefox');assert.equal(options.env,'feature=true')
					assert.equal(options.record,false);assert.equal(options.parallel,false)
					assert.deepEqual(options.spec,['sample.cy.js'])
					for(let i=0;!fs.existsSync('started')&&i<100;i++)await new Promise(r=>setTimeout(r,10))
					assert(fs.existsSync('started'),'startup was not executed')
					if(process.env.DDTEST_API_LATE_START_FAILURE==='true'){fs.writeFileSync('api-started','yes');for(let i=0;i<100;i++){try{process.kill(Number(fs.readFileSync('started','utf8')),0)}catch{break};await new Promise(r=>setTimeout(r,10))};await new Promise(r=>setTimeout(r,20))}
					const config=require(options.configFile),events={}
					await config.e2e.setupNodeEvents((event,handler)=>{events[event]=handler},{})
					const results={totalFailed:0,runs:[{spec:{absolute:require('path').resolve('sample.cy.js')},tests:[{title:['native'],state:'passed',attempts:[]}]}]}
					await events['after:run'](results)
					return results
				}`,
			}
			for name, content := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0600))
			}
			plan := onboard.CypressCommand{Action: "cypress-io/github-action@v7", Options: map[string]any{"browser": "firefox", "env": "feature=true", "spec": []string{"sample.cy.js"}, "record": true, "parallel": true}, Start: [][]string{{node, filepath.Join(root, "server.cjs")}}}
			data, err := json.Marshal(plan)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), data, 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			env := map[string]string{}
			owner, err := startProcessOwner(root, env)
			require.NoError(t, err)
			env["DDTEST_API_LATE_START_FAILURE"], env["DDTEST_CYPRESS_DIRECTORY"] = lateFailure, root
			env["NODE_OPTIONS"] = "--require " + filepath.Join(root, "children.cjs")
			t.Cleanup(func() { require.NoError(t, owner.Close()) })
			executor := &ext.DefaultCommandExecutor{Timeout: 10 * time.Second}
			output, err := executor.CombinedOutputInDirectory(ctx, root, node, []string{filepath.Join(root, "api.cjs"), filepath.Join(root, "plan.json")}, env)
			require.NoError(t, owner.Close())
			if lateFailure == "true" {
				require.Error(t, err, string(output))
				failure, readErr := os.ReadFile(filepath.Join(root, "execution-error.txt"))
				require.NoError(t, readErr)
				require.Contains(t, string(failure), "Startup process exited")
			} else {
				require.NoError(t, err, string(output))
				if lateFailure == "false" {
					require.FileExists(t, filepath.Join(root, "stopped"))
				}
			}
			require.FileExists(t, filepath.Join(root, "user-hook"))
			var run validationRun
			readCypressResults(filepath.Join(root, "results.json"), &run)
			require.Empty(t, run.ResultError)
			require.Len(t, run.Tests, 1)
			require.Equal(t, "passed", run.Tests[0].Status)
		})
	}

}

func TestCypressBrowserInventoryDoesNotSubstituteChannels(t *testing.T) {
	info := "Detected 1 browser installed:\n1. Chrome\n - Name: chrome\n - Channel: stable\n - Executable: /browser/firefox\n"
	available, known := cypressBrowserAvailable(info, "chrome")
	require.True(t, available)
	require.True(t, known)
	available, known = cypressBrowserAvailable(info, "firefox")
	require.False(t, available)
	require.True(t, known)
	available, known = cypressBrowserAvailable(info, "chrome:canary")
	require.False(t, available)
	require.True(t, known)
	available, known = cypressBrowserAvailable("inventory failed", "chrome")
	require.False(t, available)
	require.False(t, known)
}

func TestCypressAPIProbeDisablesEveryCustomerRetrySource(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	for _, config := range []any{nil, map[string]any{"retries": 2, "viewportWidth": 800}} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "cypress"), 0700))
		files := map[string]string{
			"package.json":       `{}`,
			"api.cjs":            cypressAPI,
			"observer.cjs":       cypressObserver,
			"probe.cy.js":        ``,
			"cypress.config.cjs": `module.exports={retries:3,e2e:{setupNodeEvents:(on,cfg)=>({...cfg,retries:2})}}`,
			"node_modules/cypress/index.js": `exports.run=async options=>{
				const assert=require('assert')
				assert.equal(process.env.CYPRESS_RETRIES,undefined);assert.equal(process.env.cypress_retries,undefined)
				assert.equal(options.config.retries,0)
				const config=require(options.configFile),events={}
				const effective=await config.e2e.setupNodeEvents((event,handler)=>{events[event]=handler},{retries:options.config.retries})
				assert.equal(effective.retries,0)
				const results={totalFailed:0,runs:[]}
				await events['after:run'](results)
				return results
			}`,
		}
		for name, content := range files {
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0600))
		}
		options := map[string]any{"browser": "electron"}
		if config != nil {
			options["config"] = config
		}
		data, err := json.Marshal(onboard.CypressCommand{Action: "cypress-io/github-action@v7", Options: options})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), data, 0600))
		command := exec.CommandContext(t.Context(), node, filepath.Join(root, "api.cjs"), filepath.Join(root, "plan.json"))
		command.Dir = root
		command.Env = append(os.Environ(), "NODE_OPTIONS=", "CYPRESS_RETRIES=2", "cypress_retries=2", "DDTEST_PROBE_MODE=fail", "DDTEST_CYPRESS_DIRECTORY="+root, "DDTEST_CYPRESS_PROBE="+filepath.Join(root, "probe.cy.js"))
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
}
