package testdrive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicWebdriverIOApply(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES to real WDIO dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	// Leave room for Chrome's Linux socket paths under the owned session.
	temporaryBase := os.TempDir()
	if runtime.GOOS != "windows" {
		temporaryBase = "/tmp"
	}
	temporary, err := os.MkdirTemp(temporaryBase, "wa-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(temporary)) })
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, temporary)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	command := func(root, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		return cmd.CombinedOutput()
	}
	output, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(output))
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root, false))
	write := func(name, value string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), value)
	}
	write("package.json", `{"private":true,"scripts":{"test":"npm run browser-tests","browser-tests":"wdio run wdio.conf.cjs"},"devDependencies":{"@wdio/cli":"9.20.0","@wdio/local-runner":"9.20.0","@wdio/mocha-framework":"9.20.0","dd-trace":"6.20.1"}}`)
	write("wdio.conf.cjs", `exports.config={
 runner: 'local',
 framework:'mocha',specs:['./test/*.js'],maxInstances:1,reporters:[],capabilities:[{browserName:'chrome','goog:chromeOptions':{binary:process.env.DDTEST_CHROME_BINARY,args:['--headless=new','--no-sandbox','--disable-dev-shm-usage']},...(process.env.DDTEST_CHROMEDRIVER_BINARY?{'wdio:chromedriverOptions':{binary:process.env.DDTEST_CHROMEDRIVER_BINARY}}:{})}]
};`)
	write("test/one.js", `describe('browser',()=>it('renders',async()=>{await browser.url('data:text/html,<h1>hello</h1>');if(await $('h1').getText()!=='hello')throw Error('heading')}))`)
	write(".gitignore", "node_modules/\n")
	write(".github/workflows/test.yml", "name: CI\non: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npm test\n")
	output, err = command(root, "git", "init", "-q")
	require.NoError(t, err, string(output))
	output, err = command(root, binary, "onboard", "--framework", "webdriverio", "--apply", "--yes", "--allow-dirty")
	require.NoError(t, err, string(output))
	assertAppliedWorkflowActionlint(t, filepath.Join(root, ".github/workflows/test.yml"))
	report := readValidationReport(t, root)
	require.True(t, report.Success, string(output))
	require.Equal(t, "compatible", report.Compatibility.Status)
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"test":"npm run browser-tests"`)
	output, err = command(root, binary, "onboard", "--framework", "webdriverio", "--apply", "--yes", "--allow-dirty")
	require.NoError(t, err, string(output))
	require.NotContains(t, string(output), "Updated ")
	assertAppliedWorkflowActionlint(t, filepath.Join(root, ".github/workflows/test.yml"))
}
