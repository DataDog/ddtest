package testdrive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestSetupSelectionUsesInstalledTracerAndChecksEveryRuntime(t *testing.T) {
	for _, tc := range []struct{ name, node, frameworkVersion, want string }{
		{"project selection", "24.14.1", "30.2.0", ""},
		{"incompatible framework", "24.14.1", "24.9.0", "Jest >=28"},
		{"unresolved CI runtime", "${{ env.CUSTOM_NODE }}", "30.2.0", "Node version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preflightFixture(t, tc.frameworkVersion, "jest-circus/runner.js")
			require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0700))
			workflow := "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '" + tc.node + "'}\n      - run: npm test\n"
			requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml"), workflow)
			scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "npx jest"}}}
			selected, err := drive.selectWorkflowTracer(t.Context(), scope, false)
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "6.16.0", selected)
			require.Equal(t, workflow, readApplyFile(t, drive.repositoryRoot, ".github/workflows/test.yml"), "selection must not edit CI")
		})
	}
}

func TestSetupSelectionRejectsAbsentCIAndConflictingPins(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "npx jest"}}}
	_, err := drive.selectWorkflowTracer(t.Context(), scope, false)
	require.ErrorContains(t, err, "CI test runtime is unresolved")
	require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0700))
	job := `    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24.14.1'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: VERSION}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml"), "jobs:\n  first:\n"+strings.ReplaceAll(job, "VERSION", "6.16.0")+"  second:\n"+strings.ReplaceAll(job, "VERSION", "6.17.0"))
	_, err = drive.selectWorkflowTracer(t.Context(), scope, false)
	require.ErrorContains(t, err, "multiple tracer versions")
}

func TestSetupSelectionHonorsExplicitCIOptOuts(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	drive.executor = workflowPreflightExecutor{root: root, runner: "jest"}
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npm test\n")
	active := onboard.TestCommand{Directory: ".", Command: "jest"}
	excluded := onboard.TestCommand{Directory: ".", Command: "unknown-command", CIInstrumentationExcluded: true}
	selected, err := drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{excluded, active}}, false)
	require.NoError(t, err, "an excluded runner must not be preflighted")
	require.Equal(t, "6.16.0", selected)
	selection, err := drive.selectSetupTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{excluded, active}}, false)
	require.NoError(t, err, "plain apply must share the coordinator's exclusion policy")
	require.Equal(t, "6.16.0", selection.Version)
	_, err = drive.selectSetupTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{excluded}}, false)
	require.ErrorContains(t, err, "no configurations enabled for instrumentation")

	_, err = drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{excluded}}, false)
	require.ErrorContains(t, err, "no configurations enabled for instrumentation")
	excluded.CIInstrumentationExcluded = false
	_, err = drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{active, excluded}}, false)
	require.Error(t, err, "unknown active runners must still block setup")
}

func TestApplyRejectsUnknownSetupBeforeSourceEdits(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	path := filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	workflow := "jobs:\n  test:\n    steps:\n      - run: node unknown-wrapper.js\n"
	requireWriteFile(t, path, workflow)
	var output bytes.Buffer
	require.ErrorContains(t, drive.Apply(t.Context(), &output, ApplyOptions{Site: "datadoghq.com"}), "CI discovery needs review")
	require.Empty(t, output.String())
	require.Equal(t, workflow, readApplyFile(t, drive.repositoryRoot, ".github/workflows/test.yml"))
	require.NoFileExists(t, filepath.Join(drive.repositoryRoot, ".gitignore"))
	require.NoDirExists(t, filepath.Join(drive.repositoryRoot, ".testoptimization"), "a refused apply leaves no report folder")
	drive.language = "python"
	require.ErrorContains(t, drive.Apply(t.Context(), &output, ApplyOptions{}), "automatic setup is unavailable")
}

func readApplyFile(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	require.NoError(t, err)
	return string(data)
}

func TestSetupSelectionResolvesExecutableFromOwningPackage(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	pkg := filepath.Join(root, "packages/app")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "node_modules/.bin"), 0700))
	require.NoError(t, os.Rename(filepath.Join(root, "node_modules/dd-trace"), filepath.Join(pkg, "node_modules/dd-trace")))
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"devDependencies":{"jest":"30.2.0","dd-trace":"6.16.0"}}`)
	requireWriteFile(t, filepath.Join(pkg, "node_modules/.bin/jest"), "")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: cd packages/app && jest\n")
	selected, err := drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "packages/app", Command: "jest"}}}, false)
	require.NoError(t, err)
	require.Equal(t, "6.16.0", selected)
	require.Equal(t, filepath.Join(pkg, "node_modules/.bin/jest"), drive.executor.(*preflightExecutor).name)
}

func TestSetupSelectionPreservesDiscoveredNodeOptions(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n")
	selected, err := drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "npx jest", Environment: map[string]string{"NODE_OPTIONS": "--conditions=customer"}}}}, false)
	require.NoError(t, err)
	require.Equal(t, "6.16.0", selected)
	require.Contains(t, drive.executor.(*preflightExecutor).environment["NODE_OPTIONS"], "--conditions=customer")
}

func TestSetupSelectionPreservesWorkspaceTracerWithoutRootInstallation(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	pkg := filepath.Join(root, "packages/app")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "node_modules/.bin"), 0700))
	require.NoError(t, os.Rename(filepath.Join(root, "node_modules/dd-trace"), filepath.Join(pkg, "node_modules/dd-trace")))
	requireWriteFile(t, filepath.Join(pkg, "node_modules/dd-trace/package.json"), `{"version":"5.128.0","engines":{"node":">=18"}}`)
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.2.0","dd-trace":"5.128.0"}}`)
	requireWriteFile(t, filepath.Join(pkg, "node_modules/.bin/jest"), "")
	drive.projectTracer = ""
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: cd packages/app && jest\n")
	selected, err := drive.selectSetupTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "packages/app", Command: "jest"}}}, false)
	require.NoError(t, err)
	require.Equal(t, "5.128.0", selected.Version)
	require.Equal(t, "project", selected.Source)
}

func TestSetupSelectionFindsHoistedWorkspaceExecutable(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	pkg := filepath.Join(root, "packages/app")
	require.NoError(t, os.MkdirAll(pkg, 0700))
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.2.0"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: cd packages/app && jest\n")
	selected, err := drive.selectSetupTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "packages/app", Command: "jest"}}}, false)
	require.NoError(t, err)
	require.Equal(t, "6.16.0", selected.Version)
	require.Equal(t, filepath.Join(root, "node_modules/.bin/jest"), drive.executor.(*preflightExecutor).name)
}

func TestSetupSelectionRefusesConfigurationOutsideTheCheckout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func(outside string) string
		link    bool
	}{
		{"symlinked config", func(string) string { return "npx jest" }, true},
		{"explicit config", func(outside string) string { return "npx jest --config " + outside }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
			root := drive.repositoryRoot
			outside := filepath.Join(t.TempDir(), "jest.config.js")
			requireWriteFile(t, outside, "throw new Error('external configuration executed')")
			if tc.link {
				require.NoError(t, os.Symlink(outside, filepath.Join(root, "jest.config.js")))
			}
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
			requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npm test\n")
			executor := drive.executor.(*preflightExecutor)
			_, err := drive.selectSetupTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: tc.command(outside)}}}, false)
			require.ErrorContains(t, err, "configuration file resolves outside checkout")
			require.Zero(t, executor.calls, "preflight must not load the external configuration")
		})
	}
}

func TestAutomaticSetupFrameworksExcludeCypress(t *testing.T) {
	names := AutomaticSetupFrameworkNames()
	require.Contains(t, names, "Jest")
	require.Contains(t, names, "WebdriverIO")
	require.NotContains(t, names, "Cypress")
	require.Contains(t, ValidationFrameworkNames(), "Cypress")
}

func TestRemoveEmptyDirectoriesKeepsAnyFile(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty", ".testoptimization")
	require.NoError(t, os.MkdirAll(filepath.Join(empty, "testdrive", "session"), 0700))
	removeEmptyDirectories(empty)
	require.NoDirExists(t, empty)
	kept := filepath.Join(root, "kept", ".testoptimization")
	require.NoError(t, os.MkdirAll(filepath.Join(kept, "testdrive"), 0700))
	requireWriteFile(t, filepath.Join(kept, "testdrive.json"), "{}")
	removeEmptyDirectories(kept)
	require.FileExists(t, filepath.Join(kept, "testdrive.json"))
}

func TestSetupSelectionAcceptsMochaCoverageThatSetupWraps(t *testing.T) {
	// Setup wraps a Mocha command with NYC; c8 coverage needs a manual switch.
	for command, repairable := range map[string]bool{"npm test": true, "npx c8 mocha": false} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0","c8":"10.1.3"}}`)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
		requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js, js-tracer-version: '6.20.1'}\n      - run: "+command+"\n        env:\n          NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}\n")
		ci := onboard.CheckCIRuntimes(t.Context(), root, "mocha")
		require.Len(t, ci.Jobs, 1, command)
		require.Equal(t, onboard.CodeUnverifiedInitialization, ci.Jobs[0].Code, command)
		require.Equal(t, repairable, repairableSetupFinding("mocha", ci.Jobs[0]), "%s: %s", command, ci.Jobs[0].Reason)
		require.False(t, repairableSetupFinding("jest", ci.Jobs[0]), "only Mocha setup adds the NYC wrapper")
	}
}
