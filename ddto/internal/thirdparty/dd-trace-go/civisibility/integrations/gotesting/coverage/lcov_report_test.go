// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	civisibilityutils "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
)

func TestWriteLCOVReportFromProfile(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})
	civisibilityutils.AddCITagsMap(map[string]string{
		constants.GitRepositoryURL: "https://github.com/acme/project.git",
	})

	profilePath := writeLCOVTestProfile(t, `mode: count
github.com/acme/project/b.go:10.1,12.1 1 1
github.com/acme/project/b.go:11.1,13.5 1 0
github.com/acme/project/a.go:5.1,5.8 1 0
github.com/acme/project/a.go:5.1,6.1 1 1
github.com/acme/project/ignored.go:1.1,3.1 0 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:a.go
DA:5,1
LH:1
LF:1
end_of_record
SF:b.go
DA:10,1
DA:11,1
DA:12,0
DA:13,0
LH:2
LF:4
end_of_record
SF:ignored.go
DA:1,1
DA:2,1
LH:2
LF:2
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileIncludesZeroStatementCoveredBlocks(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `mode: count
main.go:23.19,24.2 0 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:main.go
DA:23,1
DA:24,1
LH:2
LF:2
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileSkipsUnresolvedFileNamesWithColon(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `mode: set
C:/workspace/project/file.go:3.1,4.1 1 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	if report.Len() != 0 {
		t.Fatalf("expected unresolved drive path to be skipped, got:\n%s", report.String())
	}
}

func TestWriteLCOVReportFromProfileUsesRepositoryRelativePathsFromCITags(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	workspace := t.TempDir()
	civisibilityutils.AddCITagsMap(map[string]string{
		constants.CIWorkspacePath:  workspace,
		constants.GitRepositoryURL: "https://github.com/acme/project.git",
	})

	profilePath := writeLCOVTestProfile(t, fmt.Sprintf(`mode: count
github.com/acme/project/pkg/imported.go:10.1,11.1 1 1
github.com/acme/project/pkg/aliased.go:1.1,2.1 1 0
%s:2.1,3.1 1 1
./pkg/relative.go:7.1,8.1 1 1
github.com/other/project/pkg/external.go:1.1,2.1 1 1
%s:1.1,2.1 1 1
%s:3.1,4.1 1 1
`,
		filepath.Join(workspace, "pkg", "aliased.go"),
		filepath.Join(filepath.Dir(workspace), "outside.go"),
		filepath.Join(workspace, "pkg", "absolute.go")))

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:pkg/absolute.go
DA:3,1
LH:1
LF:1
end_of_record
SF:pkg/aliased.go
DA:1,0
DA:2,1
LH:1
LF:2
end_of_record
SF:pkg/imported.go
DA:10,1
LH:1
LF:1
end_of_record
SF:pkg/relative.go
DA:7,1
LH:1
LF:1
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileUsesModuleRelativePaths(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	modulePath = "github.com/acme/project/v2/internal/module"
	moduleDir = t.TempDir()
	profilePath := writeLCOVTestProfile(t, fmt.Sprintf(`mode: count
github.com/acme/project/v2/internal/module/pkg/imported.go:10.1,11.1 1 1
%s:3.1,4.1 1 1
`, filepath.Join(moduleDir, "local.go")))

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:internal/module/local.go
DA:3,1
LH:1
LF:1
end_of_record
SF:internal/module/pkg/imported.go
DA:10,1
LH:1
LF:1
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

// A nested module's files keep the module's directory in the repository,
// which its module path does not show.
func TestWriteLCOVReportFromProfileUsesNestedModuleDirectory(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	workspace := t.TempDir()
	civisibilityutils.AddCITagsMap(map[string]string{constants.CIWorkspacePath: workspace})
	modulePath = "github.com/acme/project/services/api"
	moduleDir = filepath.Join(workspace, "services", "api")
	profilePath := writeLCOVTestProfile(t, `mode: count
github.com/acme/project/services/api/handler.go:1.1,2.1 1 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}
	if want := "SF:services/api/handler.go\nDA:1,1\nLH:1\nLF:1\nend_of_record\n"; report.String() != want {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", want, report.String())
	}
}

// A directory of the module whose name has a dot, as api.v2, keeps its files:
// only paths that the module does not resolve are filtered that way.
func TestWriteLCOVReportFromProfileKeepsDottedModuleDirectories(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	modulePath = "github.com/acme/project"
	moduleDir = t.TempDir()
	profilePath := writeLCOVTestProfile(t, `mode: count
github.com/acme/project/api.v2/routes.go:3.1,4.1 1 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}
	if want := "SF:api.v2/routes.go\nDA:3,1\nLH:1\nLF:1\nend_of_record\n"; report.String() != want {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", want, report.String())
	}
}

func TestWriteLCOVReportFromProfileIncludesProfileWithoutExecutableStatements(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `mode: count
ignored.go:1.1,3.1 0 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:ignored.go
DA:1,1
DA:2,1
LH:2
LF:2
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileRejectsInvalidProfile(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `not a coverprofile
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err == nil {
		t.Fatal("WriteLCOVReportFromProfile() expected error")
	}
}

func writeLCOVTestProfile(t *testing.T, content string) string {
	t.Helper()

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(profilePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write coverage profile: %v", err)
	}
	return profilePath
}

// A nested module's file takes the backend coverage of its own path in the
// repository, not of a file at the root with the same module-relative path.
func TestBackfillPrefersNestedModuleRepositoryPath(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	workspace := t.TempDir()
	civisibilityutils.AddCITagsMap(map[string]string{constants.CIWorkspacePath: workspace})
	modulePath = "example.com/api"
	moduleDir = filepath.Join(workspace, "services", "api")
	root, nested := filebitmap.FromActiveRange(3, 3), filebitmap.FromActiveRange(9, 9)
	name, bitmap, ok := backfillBitmapForProfileFile("example.com/api/handler.go", map[string]*filebitmap.FileBitmap{
		"handler.go":              root,
		"services/api/handler.go": nested,
	})
	if !ok || name != "services/api/handler.go" || bitmap != nested {
		t.Fatalf("backend coverage of %q, %t; want services/api/handler.go", name, ok)
	}
	// Without the nested file's entry, the root file's does not stand in.
	if name, _, ok := backfillBitmapForProfileFile("example.com/api/handler.go", map[string]*filebitmap.FileBitmap{"handler.go": root}); ok {
		t.Fatalf("backend coverage of %q for services/api/handler.go", name)
	}
}

// With -trimpath, the test's own file is named by its import path; its
// coverage row takes its path in the repository.
func TestTestCoverageNamesTrimmedTestFileInRepository(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})
	workspace := t.TempDir()
	civisibilityutils.AddCITagsMap(map[string]string{
		constants.CIWorkspacePath:  workspace,
		constants.GitRepositoryURL: "https://github.com/acme/project.git",
	})
	for path, want := range map[string]string{
		"github.com/acme/project/pkg/example_test.go":  "pkg/example_test.go",
		filepath.Join(workspace, "pkg", "abs_test.go"): "pkg/abs_test.go",
	} {
		if got := NewTestCoverage(1, 2, 3, 4, path).(*testCoverage).testFile; got != want {
			t.Errorf("%s: test file %q; want %q", path, got, want)
		}
	}
	// A nested module's directory need not match its path.
	modulePath, moduleDir = "github.com/acme/project/api", filepath.Join(workspace, "services", "api")
	if got := NewTestCoverage(1, 2, 3, 4, "github.com/acme/project/api/example_test.go").(*testCoverage).testFile; got != "services/api/example_test.go" {
		t.Errorf("relocated module's test file %q", got)
	}
	modulePath, moduleDir = "", ""
	// Experimental file coverage names the test's file once, also trimmed.
	files := fileCoverageFiles("pkg/example_test.go", []string{"github.com/acme/project/pkg/example_test.go", "github.com/acme/project/pkg/lib.go"})
	if len(files) != 2 || files[0].name != "pkg/example_test.go" || files[1].name != "pkg/lib.go" {
		t.Errorf("file coverage rows %+v", files)
	}
}
