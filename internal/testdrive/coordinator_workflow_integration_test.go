package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

// This is a real framework journey through the public binary, with no coding
// agent, real credentials, or remote intake. Run it in a background-like process
// with no terminal/stdin, as an agent host does.
func TestPublicCoordinatorJestFactsAndFreshReport(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES to installed Jest/dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = "../.."
	data, err := build.CombinedOutput()
	require.NoError(t, err, string(data))
	root := workspace.CanonicalRoot(t.TempDir())
	require.NoError(t, workspace.Copy(ctx, filepath.Dir(modules), root, skipIgnoredOutput))
	write := func(name, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), body)
	}
	versions := map[string]string{}
	for _, name := range []string{"jest", "dd-trace"} {
		data, err := os.ReadFile(filepath.Join(modules, name, "package.json"))
		require.NoError(t, err)
		var pkg struct{ Version string }
		require.NoError(t, json.Unmarshal(data, &pkg))
		versions[name] = pkg.Version
	}
	manifest, err := json.Marshal(map[string]any{"private": true, "scripts": map[string]string{"test": "jest --runInBand", "build": "node build.js", "start": "node server.cjs"}, "devDependencies": versions})
	require.NoError(t, err)
	write("package.json", string(manifest))
	write(".gitignore", "node_modules/\ndist/\n.testoptimization/\n")
	write("build.js", `require('fs').mkdirSync('dist',{recursive:true});require('fs').writeFileSync('dist/lib.js','module.exports=42')`)
	write("one.test.js", `test('built application',()=>expect(require('./dist/lib')).toBe(42));test('application service',done=>{require('http').get('http://127.0.0.1:'+process.env.APP_PORT,response=>{response.resume();response.on('end',done)}).on('error',done)})`)
	write("server.cjs", `require('http').createServer((req,res)=>res.end('ready')).listen(Number(process.env.APP_PORT),'127.0.0.1')`)
	write("jest.config.cjs", `module.exports={testEnvironment:'node'}`)
	write(".github/workflows/test.yml", "name: CI\non: [push]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm run build && npm test\n")
	init := exec.CommandContext(ctx, "git", "init", "-q")
	init.Dir = root
	data, err = init.CombinedOutput()
	require.NoError(t, err, string(data))
	invoke := func(args ...string) (onboard.AgentResult, error) {
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				command.Env = append(command.Env, value)
			}
		}
		var out, diagnostics bytes.Buffer
		command.Stdout = &out
		command.Stderr = &diagnostics
		err := command.Run()
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &result), "args=%v stdout=%s stderr=%s", args, out.String(), diagnostics.String())
		return result, err
	}
	args := []string{"onboard", "--framework", "jest", "--apply", "--allow-dirty", "--yes", "--format", "json"}
	state, err := invoke(args...)
	require.Error(t, err)
	require.Equal(t, "setup_unresolved", state.Code)
	require.NotNil(t, state.InputTemplate)
	facts := state.InputTemplate
	facts.Configurations[0].Prerequisites[0].ExpectedFiles = []string{"dist/lib.js"}
	facts.Configurations[0].Services = []onboard.ServiceCommand{{Argv: []string{"npm", "start"}, Directory: ".", ReadinessURL: "http://127.0.0.1:0", TimeoutSeconds: 10, PortEnvironment: "APP_PORT"}}
	data, err = json.Marshal(facts)
	require.NoError(t, err)
	factsFile := filepath.Join(t.TempDir(), "setup.json")
	require.NoError(t, os.WriteFile(factsFile, data, 0600))
	state, err = invoke(append(args, "--facts", factsFile)...)
	require.NoError(t, err, "%+v", state)
	require.Equal(t, "complete_locally", state.Status, "%+v", state)
	require.NotEmpty(t, state.SetupDiff)
	require.NoDirExists(t, filepath.Join(root, "dist"), "build must run only in the owned copy")
	report := readValidationReport(t, root)
	require.True(t, report.Success)
	require.Equal(t, "npm run build", report.Configurations[0].Preparation[0].Command)
	require.Len(t, report.Configurations[0].ServiceExecutions, 1)
	require.True(t, report.Configurations[0].ServiceExecutions[0].Ready)
	state, err = invoke("testdrive", "--report", "--format", "json")
	require.NoError(t, err)
	require.Equal(t, "complete_locally", state.Status)
	require.True(t, filepath.IsAbs(state.JSONReport))
	state, err = invoke("testdrive", "--framework", "jest", "--check-only", "--yes", "--format", "json")
	require.Error(t, err, "ordinary static discovery has no setup-facts binding")
	require.Equal(t, "configuration_check_failed", state.Code)
	state, err = invoke("testdrive", "--report", "--format", "json")
	require.NoError(t, err)
	require.Equal(t, "complete_locally", state.Status)
	write("one.test.js", `test('built application',()=>expect(require('./dist/lib')).toBe(42)) `)
	state, err = invoke("testdrive", "--report", "--format", "json")
	require.Error(t, err)
	require.Equal(t, "stale_report", state.Code)
	require.NotEmpty(t, state.NextCommand)
}
