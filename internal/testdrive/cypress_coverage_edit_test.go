package testdrive

import (
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
	require.True(t, edit.RequiresApproval)
	require.Equal(t, "npm install --save-dev babelify @babel/core babel-plugin-istanbul", edit.Install)
	require.Equal(t, fileEdit{Path: "babel.config.json", Action: "create", Content: babelTestCoverage}, edit.Files[0])
	require.Equal(t, "package.json", edit.Files[1].Path)
	require.Equal(t, `"build:coverage": "cross-env ./node_modules/typescript/bin/tsc -p ./js/ && cross-env BABEL_ENV=test node ./node_modules/browserify/bin/cmd -t babelify ./js/app.js -o ./js/bundle.js"`+"\n", edit.Files[1].Content)
	require.Contains(t, edit.Serve, "npm run build:coverage")

	requireWriteFile(t, filepath.Join(root, "pnpm-lock.yaml"), "")
	require.Equal(t, "pnpm add --save-dev babelify @babel/core babel-plugin-istanbul", cypressCoverageEdit(root).Install)
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

	webpack := t.TempDir()
	requireWriteFile(t, filepath.Join(webpack, "package.json"), `{"devDependencies":{"webpack":"^5.0.0"}}`)
	requireWriteFile(t, filepath.Join(webpack, "webpack.config.js"), "module.exports = {}")
	edit = cypressCoverageEdit(webpack)
	require.Equal(t, "webpack", edit.Build)
	require.Equal(t, []string{"babel.config.json", "webpack.config.js"}, []string{edit.Files[0].Path, edit.Files[1].Path})
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
	require.Contains(t, coverage.Reason, "apply it only with their approval")
}
