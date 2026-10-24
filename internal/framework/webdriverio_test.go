package framework

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"

	"github.com/stretchr/testify/require"
)

func TestWebdriverIORunPreservesCommandAndSelectsOnlyRequestedFiles(t *testing.T) {
	executor := &cucumberCommandExecutor{}
	runner := NewWebdriverIO(&testPlatform{})
	runner.executor = executor
	runner.commandOverride = []string{"npm", "run", "browser"}
	require.NoError(t, runner.RunTests(context.Background(), []string{"selected.spec.js"}, map[string]string{}))
	require.Equal(t, "npm", executor.capturedName)
	require.Equal(t, []string{"run", "browser"}, executor.capturedArgs)
	var files []string
	require.NoError(t, json.Unmarshal([]byte(executor.capturedEnv["DDTEST_WDIO_FILES"]), &files))
	root, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "selected.spec.js")}, files)
	require.Contains(t, executor.capturedEnv["NODE_OPTIONS"], "webdriverio_observer.mjs")
}

type webdriverIODiscoveryExecutor struct{ root string }

func (e webdriverIODiscoveryExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	body, _ := json.Marshal(WebdriverIOConfig{Files: []string{filepath.Join(e.root, "click", "example.js")}})
	return nil, os.WriteFile(env["DDTEST_WDIO_INSPECT"], body, 0600)
}
func (e webdriverIODiscoveryExecutor) Run(context.Context, string, []string, map[string]string) error {
	return nil
}
func TestWebdriverIODiscoveryKeepsConfiguredSpecPaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Mkdir(filepath.Join(root, "click"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "click/example.js"), []byte(""), 0600))
	runner := NewWebdriverIO(&testPlatform{})
	runner.executor = webdriverIODiscoveryExecutor{root: root}
	files, err := runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.NoError(t, err)
	require.Equal(t, []string{"click/example.js"}, files)
}

type webdriverIOGroupedExecutor struct{ webdriverIODiscoveryExecutor }

func (e webdriverIOGroupedExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	body, _ := json.Marshal(WebdriverIOConfig{Files: []string{filepath.Join(e.root, "setup.js"), filepath.Join(e.root, "case.js")}, GroupedSpecs: true})
	return nil, os.WriteFile(env["DDTEST_WDIO_INSPECT"], body, 0600)
}
func TestWebdriverIODiscoveryRejectsGroupedSpecs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	runner := NewWebdriverIO(&testPlatform{})
	runner.executor = webdriverIOGroupedExecutor{webdriverIODiscoveryExecutor{root: root}}
	files, err := runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.ErrorIs(t, err, ErrWebdriverIOGroupedSpecs, "a group must not be split across runners")
	require.Empty(t, files)
}

func TestWebdriverIOCommandFindsDefaultConfigExtensions(t *testing.T) {
	for _, extension := range []string{"ts", "mjs", "cjs"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile("wdio.conf."+extension, []byte(""), 0600))
			runner := NewWebdriverIO(&testPlatform{})
			runner.commandOverride = nil
			_, args := runner.Command()
			require.Equal(t, []string{"run", "wdio.conf." + extension}, args)
		})
	}
}

func TestWebdriverIORunSelectedFilesIntegration(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	// Keep Chrome socket paths short and all adapter-owned caches under cleanup.
	temporaryBase := os.TempDir()
	if runtime.GOOS != "windows" {
		temporaryBase = "/tmp"
	}
	temporary, err := os.MkdirTemp(temporaryBase, "ddtest-wdio-adapter-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(temporary)) })
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, temporary)
	}

	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	require.NoError(t, os.MkdirAll("config", 0700))
	require.NoError(t, os.MkdirAll("test", 0700))
	require.NoError(t, os.MkdirAll("ran", 0700))
	require.NoError(t, os.WriteFile("package.json", []byte(`{}`), 0600))
	chrome := `'goog:chromeOptions':{binary:process.env.DDTEST_CHROME_BINARY,args:['--headless=new','--no-sandbox','--disable-dev-shm-usage']},...(process.env.DDTEST_CHROMEDRIVER_BINARY?{'wdio:chromedriverOptions':{binary:process.env.DDTEST_CHROMEDRIVER_BINARY}}:{})`
	// Each capability has its own selection, and the job runs native shard 2/2:
	// "first" selects c.js from a, b, c and "second" selects d.js from b, d.
	config := `exports.config={runner:'local',framework:'mocha',reporters:[],specs:['../test/*.js'],shard:{current:2,total:2},capabilities:[` +
		`{browserName:'chrome','ddtest:label':'first',specs:['../test/*.js'],exclude:['../test/d.js'],` + chrome + `},` +
		`{browserName:'chrome','ddtest:label':'second',specs:['../test/b.js','../test/d.js'],` + chrome + `}]}`
	require.NoError(t, os.WriteFile("config/wdio.conf.cjs", []byte(config), 0600))
	for _, name := range []string{"a", "b", "c", "d"} {
		test := `it('` + name + `',()=>require('node:fs').writeFileSync(require('node:path').join(process.env.DDTEST_WDIO_RAN,browser.requestedCapabilities['ddtest:label']+'-` + name + `'),''))`
		require.NoError(t, os.WriteFile("test/"+name+".js", []byte(test), 0600))
	}
	runner := NewWebdriverIO(&testPlatform{})
	runner.commandOverride = []string{filepath.Join(root, "node_modules/.bin/wdio"), "run", "config/wdio.conf.cjs"}
	files, err := runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.NoError(t, err)
	require.Equal(t, []string{"test/c.js", "test/d.js"}, files)
	require.NoError(t, runner.RunTests(t.Context(), files, map[string]string{"DD_TRACE_ENABLED": "false", "DD_CIVISIBILITY_ENABLED": "false", "DDTEST_WDIO_RAN": filepath.Join(root, "ran")}))
	ran, err := os.ReadDir("ran")
	require.NoError(t, err)
	var names []string
	for _, entry := range ran {
		names = append(names, entry.Name())
	}
	// Without per-capability selection both capabilities ran both files, and
	// re-sharding the assigned batch dropped c.js.
	require.Equal(t, []string{"first-c", "second-d"}, names)
	require.Error(t, runner.RunTests(t.Context(), []string{"test/a.js"}, map[string]string{"DD_TRACE_ENABLED": "false", "DD_CIVISIBILITY_ENABLED": "false", "DDTEST_WDIO_RAN": filepath.Join(root, "ran")}), "no capability selects a file outside its shard and selection")

	// Grouped specs run together in one worker, so planning must not split them;
	// multiremote shares the global selection.
	require.NoError(t, os.WriteFile("config/wdio.conf.cjs", []byte(`exports.config={runner:'local',framework:'mocha',reporters:[],specs:[['../test/a.js','../test/b.js']],capabilities:[{browserName:'chrome'}]}`), 0600))
	_, err = runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.ErrorIs(t, err, ErrWebdriverIOGroupedSpecs)
	require.NoError(t, os.WriteFile("config/wdio.conf.cjs", []byte(`exports.config={runner:'local',framework:'mocha',reporters:[],specs:['../test/a.js','../test/b.js'],capabilities:{one:{capabilities:{browserName:'chrome'}}}}`), 0600))
	files, err = runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.NoError(t, err)
	require.Equal(t, []string{"test/a.js", "test/b.js"}, files)
}
