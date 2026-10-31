package testdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func useReportRoot(t *testing.T, from, to string) {
	t.Helper()
	UseReportRoot(from, to)
	t.Cleanup(func() { UseReportRoot("", "") })
}

func TestReportsOfAPackageRunStayAtTheRepositoryRoot(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	pkg := filepath.Join(root, "test")
	require.NoError(t, os.MkdirAll(pkg, 0o700))
	require.Equal(t, filepath.Join(pkg, ".testoptimization", "testdrive.json"), validationPath(pkg))

	useReportRoot(t, pkg, root)
	require.Equal(t, filepath.Join(root, ".testoptimization", "testdrive.json"), validationPath(pkg))
	require.Equal(t, filepath.Join(root, ".testoptimization", "report.html"), htmlReportPath(pkg))
	require.Equal(t, filepath.Join(root, ".testoptimization", "testdrive.json"), validationPath(""), "the displayed location names the root report")
	other := filepath.Join(root, "other")
	require.Equal(t, filepath.Join(other, ".testoptimization", "testdrive.json"), validationPath(other), "only the package's own runs move")

	_ = finishValidation(io.Discard, pkg, validationResult{Framework: "mocha"}) // An INCOMPLETE report is written and returns an error.
	require.FileExists(t, filepath.Join(root, ".testoptimization", "testdrive.json"))
	require.NoDirExists(t, filepath.Join(pkg, ".testoptimization"))
}

func TestUnpackableReportPackagesAreNotApplicable(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest   string
		redirected bool
		want       string
		err        bool
	}{
		"publishable":          {manifest: `{"name":"app","version":"1.0.0"}`},
		"missing":              {want: "No package.json at the repository root"},
		"without name/version": {manifest: `{"scripts":{"test":"mocha *.test.js"}}`, want: "has no name or version"},
		"without version":      {manifest: `{"name":"app"}`, want: "has no name or version"},
		"private":              {manifest: `{"name":"app","version":"1.0.0","private":true}`, want: "Root package is private"},
		"below the root":       {manifest: `{"name":"app","version":"1.0.0"}`, redirected: true, want: "outside this package"},
		"unreadable":           {manifest: `{`, err: true},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if tc.manifest != "" {
				requireWriteFile(t, filepath.Join(root, "package.json"), tc.manifest)
			}
			reason, err := unpackableReportPackage(root, tc.redirected)
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.want == "" {
				require.Empty(t, reason)
			} else {
				require.Contains(t, reason, tc.want)
			}
		})
	}
}

func TestReportExclusionsOfAPackageRunAreCheckedAtTheRoot(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(output))
	pkg := filepath.Join(root, "test")
	require.NoError(t, os.MkdirAll(pkg, 0o700))
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"scripts":{"test":"mocha"}}`)
	t.Chdir(pkg)
	useReportRoot(t, pkg, root)
	drive := &Testdrive{repositoryRoot: pkg, language: "javascript", executor: &ext.DefaultCommandExecutor{}}

	checks := drive.verifyReportExclusions(t.Context())
	require.Equal(t, []string{"git", "check-ignore", "--no-index", "--", "../.testoptimization/testdrive.json", "../.testoptimization/report.html"}, checks[0].Command)
	require.Equal(t, "failed", checks[0].Status, "the root .gitignore does not exclude the reports yet")
	require.Equal(t, "not applicable", checks[1].Status)
	require.Contains(t, checks[1].Reason, "outside this package")

	requireWriteFile(t, filepath.Join(root, ".gitignore"), "/.testoptimization/\n")
	checks = drive.verifyReportExclusions(t.Context())
	require.Equal(t, "passed", checks[0].Status, checks[0].Reason)
}

// The disposable copy is the whole checkout with the package as the working
// directory, so tests can read files outside their package.
func TestPackageConfigurationCopiesTheWholeCheckout(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	const schemaSize = 1 << 16
	requireWriteFile(t, filepath.Join(root, "schema.json"), strings.Repeat(" ", schemaSize))
	pkg := filepath.Join(root, "test")
	require.NoError(t, os.MkdirAll(pkg, 0o700))
	requireWriteFile(t, filepath.Join(pkg, "package.json"), `{"scripts":{"test":"mocha"}}`)
	requireWriteFile(t, filepath.Join(pkg, "schema.test.js"), "require('../schema.json')")
	t.Chdir(pkg)
	drive := Testdrive{repositoryRoot: pkg, executor: &fakeTestdriveExecutor{}, framework: framework.NewMocha(platform.NewJavaScript()), language: "javascript",
		command: "mocha", discoveryError: errors.New("stop after isolation"), nodeVersion: func() string { return "v24.0.0" },
		preflight: func(context.Context, io.Writer, *validationResult) error { return nil }}
	var output bytes.Buffer
	item := configurationResult{TestCommand: onboard.TestCommand{Directory: "."}}
	_, _ = drive.runPreparedConfiguration(t.Context(), &output, &item)
	start := strings.Index(output.String(), "Isolating configuration:")
	require.GreaterOrEqual(t, start, 0, output.String())
	var size int
	_, err := fmt.Sscanf(output.String()[start:], "Isolating configuration: up to %d bytes", &size)
	require.NoError(t, err, output.String())
	require.GreaterOrEqual(t, size, schemaSize, "the copy includes files outside the package")
}
