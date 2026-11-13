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

	"github.com/stretchr/testify/require"
)

// WDIO 9 starts a Node worker through xvfb-run when DISPLAY is absent. Ubuntu
// 26's wrapper uses fd 3 for its error log, colliding with the worker's IPC.
func TestWebdriverIO9WorkerIPC(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(modules, "@wdio/local-runner/package.json"))
	require.NoError(t, err)
	var pkg struct{ Version string }
	require.NoError(t, json.Unmarshal(data, &pkg))
	if !strings.HasPrefix(pkg.Version, "9.") {
		t.Skip("WebdriverIO 9 child-process IPC regression")
	}
	root := t.TempDir()
	worker := filepath.Join(root, "worker.cjs")
	requireWriteFile(t, worker, `const timeout=setTimeout(()=>process.exit(1),10000);
process.on('message',()=>process.send({name:'ready',origin:'worker'},()=>{clearTimeout(timeout);process.disconnect()}));`)
	script := `import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import {once} from 'node:events';
const require=createRequire(process.argv[1]);
const {resolve}=await import(pathToFileURL(require.resolve('import-meta-resolve')).href);
const runner=resolve('@wdio/local-runner',pathToFileURL(process.argv[1]).href);
const {ProcessFactory}=await import(resolve('@wdio/xvfb',runner));
const worker=await new ProcessFactory().createWorkerProcess(process.argv[2],[],{env:process.env,stdio:['ignore','pipe','pipe','ipc']});
worker.stdout.pipe(process.stdout);worker.stderr.pipe(process.stderr);
const messages=[];worker.on('message',message=>messages.push(message));
const exited=once(worker,'exit');worker.send('start');
const [code]=await exited;assert.equal(code,0,'worker must exit successfully');
assert.deepEqual(messages,[{name:'ready',origin:'worker'}],'worker IPC handshake must reach its parent');`
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cli, err := filepath.EvalSymlinks(filepath.Join(modules, "@wdio/cli/bin/wdio.js"))
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script, cli, worker)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}
