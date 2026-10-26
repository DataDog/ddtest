package testdrive

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCucumberFormatterOptionsReachOnlyActualCLI(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules/@cucumber/cucumber")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0700))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"main":"index.js","bin":{"cucumber-js":"bin/cucumber.js"}}`)
	requireWriteFile(t, filepath.Join(pkg, "index.js"), `exports.Formatter=class {};exports.Cli=class {};`)
	requireWriteFile(t, filepath.Join(root, "observer.cjs"), cucumberSupportObserver)
	capture := `require('node:fs').writeFileSync(process.env.OUTPUT,JSON.stringify(process.argv.slice(2)))`
	requireWriteFile(t, filepath.Join(pkg, "bin/cucumber.js"), capture)
	requireWriteFile(t, filepath.Join(root, "wrapper.js"), capture)
	formatters := []string{"--format", "message:result.ndjson"}
	data, err := json.Marshal(formatters)
	require.NoError(t, err)
	for _, entry := range []string{filepath.Join(pkg, "bin/cucumber.js"), filepath.Join(root, "wrapper.js")} {
		output := filepath.Join(root, "argv.json")
		command := exec.Command(node, "--require", filepath.Join(root, "observer.cjs"), entry, "features")
		command.Dir = root
		command.Env = append(os.Environ(), "DDTEST_CUCUMBER_FORMATTERS="+string(data), "OUTPUT="+output)
		log, err := command.CombinedOutput()
		require.NoError(t, err, string(log))
		actual, err := os.ReadFile(output)
		require.NoError(t, err)
		var args []string
		require.NoError(t, json.Unmarshal(actual, &args))
		expected := []string{"features"}
		if filepath.Base(entry) == "cucumber.js" {
			expected = append(expected, formatters...)
		}
		require.Equal(t, expected, args)
	}
	// node -e uses argv[1] for an arbitrary argument, not a script path.
	output := filepath.Join(root, "eval-argv.json")
	command := exec.Command(node, "--require", filepath.Join(root, "observer.cjs"), "-e", capture, "not-a-script", "features")
	command.Dir = root
	command.Env = append(os.Environ(), "DDTEST_CUCUMBER_FORMATTERS="+string(data), "OUTPUT="+output)
	log, err := command.CombinedOutput()
	require.NoError(t, err, string(log))
	actual, err := os.ReadFile(output)
	require.NoError(t, err)
	var args []string
	require.NoError(t, json.Unmarshal(actual, &args))
	require.Equal(t, []string{"features"}, args)

}

// NODE_OPTIONS reaches every Node process of the run, such as a server a hook
// starts in another folder. Only the Cucumber CLI may load Cucumber.
func TestCucumberSupportPreloadLeavesOtherNodeProcessesUnchanged(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	observer := filepath.Join(t.TempDir(), "observer.cjs")
	requireWriteFile(t, observer, cucumberSupportObserver)
	project := t.TempDir()
	pkg := filepath.Join(project, "node_modules/@cucumber/cucumber")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0700))
	requireWriteFile(t, filepath.Join(project, "package.json"), `{}`)
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"main":"index.js","bin":{"cucumber-js":"bin/cucumber.js"}}`)
	requireWriteFile(t, filepath.Join(pkg, "index.js"), `throw new Error('Cucumber loaded outside its CLI')`)
	requireWriteFile(t, filepath.Join(project, "server.js"), `console.log('server ran')`)
	for name, tc := range map[string]struct {
		directory string
		args      []string
	}{
		"without Cucumber": {t.TempDir(), []string{"-e", "console.log('server ran')"}},
		"another script":   {project, []string{filepath.Join(project, "server.js")}},
		"node -e":          {project, []string{"-e", "console.log('server ran')"}},
	} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(node, append([]string{"--require", observer}, tc.args...)...)
			command.Dir = tc.directory
			command.Env = append(os.Environ(), "DDTEST_CUCUMBER_FORMATTERS=[]", "DDTEST_CUCUMBER_SUPPORT="+filepath.Join(t.TempDir(), "support.json"))
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Equal(t, "server ran\n", string(output))
		})
	}
}
