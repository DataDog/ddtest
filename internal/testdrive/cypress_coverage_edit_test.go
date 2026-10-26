package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

// The todomvc layout: tsc compiles js/, then browserify bundles it.
const browserifyApp = `{"scripts":{"build":"cross-env ./node_modules/typescript/bin/tsc -p ./js/ && node ./node_modules/browserify/bin/cmd ./js/app.js -o ./js/bundle.js","start":"npm run build & npm run serve"},"devDependencies":{"browserify":"^16.2.3","cross-env":"7.0.3","typescript":"5.5.4"}}`

func TestCypressCoverageEditForABrowserifyBuild(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), browserifyApp)
	edit := cypressCoverageEdit(root)
	require.NotNil(t, edit)
	require.Equal(t, "browserify", edit.Build)
	require.False(t, edit.RequiresApproval, "a recognized, test-only coverage edit is prescribed")
	// Babel 8 is current but babelify needs Babel 7: unpinned installs fail.
	require.Equal(t, "npm install --save-dev babelify@^10 @babel/core@^7 babel-plugin-istanbul@^7", edit.Install)
	require.Equal(t, fileEdit{Path: "babel.config.json", Action: "create", Content: babelTestCoverage}, edit.Files[0])
	require.Equal(t, "package.json", edit.Files[1].Path)
	require.Equal(t, `"build:coverage": "cross-env ./node_modules/typescript/bin/tsc -p ./js/ && cross-env BABEL_ENV=test node ./node_modules/browserify/bin/cmd -t babelify ./js/app.js -o ./js/bundle.js"`+"\n", edit.Files[1].Content)
	// Serving is an exact edit too: a start variant that builds the coverage bundle.
	require.Equal(t, fileEdit{Path: "package.json", Action: "edit", Where: "add to scripts", Content: `"start:coverage": "npm run build:coverage & npm run serve"` + "\n"}, edit.Files[2])
	require.Contains(t, edit.Serve, "npm run start:coverage")

	requireWriteFile(t, filepath.Join(root, "pnpm-lock.yaml"), "")
	require.Equal(t, "pnpm add --save-dev babelify@^10 @babel/core@^7 babel-plugin-istanbul@^7", cypressCoverageEdit(root).Install)
}

// The real todomvc layout: tsc compiles js/*.tsx to gitignored js/*.js before
// browserify bundles it. Instrumenting that output would name untracked files.
func TestCypressCoverageEditBundlesTrackedTypeScriptSources(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), browserifyApp)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "js"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755))
	requireWriteFile(t, filepath.Join(root, "js", "app.tsx"), "export {}")
	requireWriteFile(t, filepath.Join(root, "js", "tsconfig.json"), `{"compilerOptions":{"module":"commonjs","jsx":"react"}}`)
	requireWriteFile(t, filepath.Join(root, ".github", "workflows", "main.yml"), "jobs:\n  e2e:\n    steps:\n      - uses: cypress-io/github-action@v6\n        with:\n          start: npm run start\n")
	edit := cypressCoverageEdit(root)
	require.NotNil(t, edit)
	require.Equal(t, "npm install --save-dev babelify@^10 @babel/core@^7 babel-plugin-istanbul@^7 @babel/preset-env@^7 @babel/preset-react@^7 @babel/preset-typescript@^7 @babel/plugin-transform-modules-commonjs@^7", edit.Install)
	// Without esModuleInterop, tsc emits plain requires; Babel must match.
	require.Contains(t, edit.Files[0].Content, `["@babel/plugin-transform-modules-commonjs", { "noInterop": true }], "istanbul"`)
	require.Contains(t, edit.Files[0].Content, `"@babel/preset-react", "@babel/preset-typescript"`)
	require.Equal(t, `"build:coverage": "cross-env BABEL_ENV=test node ./node_modules/browserify/bin/cmd ./js/app.tsx --extension .ts --extension .tsx -t [ babelify --extensions .ts,.tsx ] -o ./js/bundle.js"`+"\n", edit.Files[1].Content)
	require.Equal(t, fileEdit{Path: ".github/workflows/main.yml", Action: "edit", Where: "the start input of the cypress-io/github-action step; keep its indentation", Replace: "start: npm run start", Content: "start: npm run start:coverage\n"}, edit.Files[3])

	// With esModuleInterop, Babel's default interop already matches tsc.
	requireWriteFile(t, filepath.Join(root, "js", "tsconfig.json"), `{"compilerOptions":{"esModuleInterop": true,"jsx":"react"}}`)
	edit = cypressCoverageEdit(root)
	require.NotContains(t, edit.Install, "plugin-transform-modules-commonjs")
	require.Contains(t, edit.Files[0].Content, `"plugins": ["istanbul"]`)
}

func TestCypressCoverageEditForViteAndWebpack(t *testing.T) {
	vite := t.TempDir()
	requireWriteFile(t, filepath.Join(vite, "package.json"), `{"devDependencies":{"vite":"^7.0.0"}}`)
	requireWriteFile(t, filepath.Join(vite, "vite.config.ts"), "export default {}")
	edit := cypressCoverageEdit(vite)
	require.Equal(t, "vite", edit.Build)
	require.Equal(t, "npm install --save-dev vite-plugin-istanbul", edit.Install)
	require.Equal(t, "vite.config.ts", edit.Files[0].Path)
	require.Contains(t, edit.Files[0].Content, "requireEnv: true")
	require.Contains(t, edit.Files[0].Content, "include: 'src/**/*'", "modules under src/components are instrumented too")

	webpack := t.TempDir()
	requireWriteFile(t, filepath.Join(webpack, "package.json"), `{"devDependencies":{"webpack":"^5.0.0"}}`)
	requireWriteFile(t, filepath.Join(webpack, "webpack.config.js"), "module.exports = {}")
	edit = cypressCoverageEdit(webpack)
	require.Equal(t, "webpack", edit.Build)
	require.Equal(t, []string{"babel.config.json", "webpack.config.js"}, []string{edit.Files[0].Path, edit.Files[1].Path})
	require.Equal(t, "npm install --save-dev babel-loader@^10 @babel/core@^7 babel-plugin-istanbul@^7", edit.Install)
}

func TestUnrecognizedCypressBuildsKeepTheGuidance(t *testing.T) {
	plain := t.TempDir()
	requireWriteFile(t, filepath.Join(plain, "package.json"), `{"scripts":{"build":"tsc"}}`)
	require.Nil(t, cypressCoverageEdit(plain))

	// An existing Babel configuration is the customer's; ddtest does not replace it.
	babel := t.TempDir()
	requireWriteFile(t, filepath.Join(babel, "package.json"), browserifyApp)
	requireWriteFile(t, filepath.Join(babel, ".babelrc"), `{"presets":["@babel/preset-env"]}`)
	require.Nil(t, cypressCoverageEdit(babel))

	coverage := cypressSourceCoverage(plain, cypressTestCode{}, intake.Facts{TestCount: 1, Tests: []intake.Test{{Name: "a"}}}, nil)
	require.Equal(t, cypressMissingCoverageReason, coverage.Reason)
	require.Nil(t, coverage.Edit)
}

func TestMissingCypressCoverageCarriesTheExactEdit(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), browserifyApp)
	coverage := cypressSourceCoverage(root, cypressTestCode{}, intake.Facts{TestCount: 1, Tests: []intake.Test{{Name: "a"}}}, nil)
	require.Equal(t, "not observed", coverage.Status)
	require.NotNil(t, coverage.Edit)
	require.Contains(t, coverage.Reason, "recognized the browserify build")
	require.Contains(t, coverage.Reason, `runs[].coverage.edit of the run whose role is "reporting-only"`)
	require.Contains(t, coverage.Reason, "ddtest-prescribed, test-only change: apply it exactly as given")
	require.NotContains(t, coverage.Reason, "approval")
}

func TestCypressCoverageEditUsesBunInBunRepositories(t *testing.T) {
	for _, lockfile := range []string{"bun.lock", "bun.lockb"} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"vite":"^7.0.0"}}`)
		requireWriteFile(t, filepath.Join(root, "vite.config.ts"), "export default {}")
		requireWriteFile(t, filepath.Join(root, lockfile), "")
		require.Equal(t, "bun add --dev vite-plugin-istanbul", cypressCoverageEdit(root).Install, lockfile)
	}
}
