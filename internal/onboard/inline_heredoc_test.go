package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterpreterHeredocsAreReadLikeInlineCode(t *testing.T) {
	for run, want := range map[string]bool{
		// express's npm-publish dist-tag step: reads package.json and runs npm view.
		"node <<'EOF'\nconst fs = require('fs')\nconst { execFileSync } = require('child_process')\nconst SEMVER = /^(\\d+)$/\nconst parsed = SEMVER.exec(version)\nconst latest = execFileSync('npm', ['view', name, 'version'], { encoding: 'utf8' }).trim()\nfs.appendFileSync(process.env.GITHUB_OUTPUT, `tag=${tag}\\n`)\nEOF": false,
		"node <<'EOF'\nrequire('child_process').execSync('npx mocha')\nEOF":                                              true,
		"node <<'EOF'\nrequire('./scripts/test')\nEOF":                                                                   true,
		"node <<'EOF'\nconst { spawn } = require('child_process')\nspawn(command)\nEOF":                                  true,
		"node <<'EOF'\nconst cp = require('node:child_process')\ncp.spawnSync('npm', ['run', script])\nEOF":              true,
		"bash <<'EOF'\nnpx mocha\nEOF":                                                                                   true,
		"bash <<'EOF'\necho done\nEOF":                                                                                   false,
		"cat <<'EOF' > notes.txt\nnpx mocha\nEOF":                                                                        false,
		"node -e \"const { execFileSync } = require('child_process'); execFileSync('npm', ['view', 'pkg', 'version'])\"": false,
	} {
		require.Equal(t, want, mayStartTests(runtimeStep{Run: run}, "mocha"), run)
	}
}

func TestWebdriverIOLeafFollowsAnyBaseConfigImport(t *testing.T) {
	root := t.TempDir()
	// TypeScript ESM imports name the emitted .js file; the source is .ts.
	require.NoError(t, os.WriteFile(filepath.Join(root, "wdio.shared.conf.ts"), []byte("export const config = {\n    runner: 'local',\n}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "wdio.local.conf.ts"), []byte("import { config as sharedConfig } from './wdio.shared.conf.js'\n\nexport const config = { ...sharedConfig }\n"), 0600))
	local, err := localWDIOLeaf(root, "wdio run wdio.local.conf.ts")
	require.NoError(t, err)
	require.True(t, local)
}
