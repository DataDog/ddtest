package testdrive

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jestReportModel builds a Jest-shaped report where every test shares one
// source excerpt and one covered-file list, as large suites typically do.
func jestReportModel(t *testing.T, tests int) reportModel {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o700))
	requireWriteFile(t, filepath.Join(root, "src", "sum.test.js"), "// shared-source-canary <script>alert(1)</script>\n")
	source := readSource(root, filepath.Join("src", "sum.test.js"), 1, 1)
	files := []string{"src/coverage-canary.js"}
	for i := 1; i < 60; i++ {
		files = append(files, fmt.Sprintf("src/module-%d.js", i))
	}
	model := reportModel{CoverageLevel: "test"}
	for i := 0; i < tests; i++ {
		model.Tests = append(model.Tests, reportTest{Name: fmt.Sprintf("sum %d", i), Suite: "sum", Status: "Pass", SourceFile: "src/sum.test.js", Source: source, CoverageLevel: "test", CoveredFiles: files})
	}
	return model
}

func TestLargeReportSharesEvidenceWithoutLosingEscapingOrCounts(t *testing.T) {
	model := jestReportModel(t, 600)
	var output bytes.Buffer
	require.NoError(t, testdriveReport.Execute(&output, model))
	text := output.String()
	require.Equal(t, 1, strings.Count(text, "shared-source-canary"))
	require.Equal(t, 1, strings.Count(text, "src/coverage-canary.js"))
	require.Contains(t, text, `data-shared-detail="shared-source-0"`)
	require.Contains(t, text, `<template id="shared-coverage-0">`)
	require.Equal(t, 600, strings.Count(text, `data-shared-detail="shared-source-0"`), "every test keeps its own placeholder")
	require.NotContains(t, text, "<script>alert(1)</script>")
	require.Len(t, model.Tests[0].Source.Lines, 1, "rendering must not mutate evidence models")
	if path := os.Getenv("DDTEST_COMPACT_REPORT_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, output.Bytes(), 0600))
	}
}

func TestSmallReportIsRenderedUnchanged(t *testing.T) {
	model := jestReportModel(t, 20)
	prepared := model.Prepared()
	require.Equal(t, model, prepared)
	var output bytes.Buffer
	require.NoError(t, testdriveReport.Execute(&output, model))
	text := output.String()
	require.NotContains(t, text, "data-shared-detail=")
	require.NotContains(t, text, `<template id="shared-`)
	require.Equal(t, 20, strings.Count(text, "shared-source-canary"), "small reports inline every source excerpt")
}

func TestLargeReportKeepsUniqueEvidenceInline(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0o700))
	var lines strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&lines, "test('unique-%d', () => {})\n", i)
	}
	requireWriteFile(t, filepath.Join(root, "test", "sum.test.js"), lines.String())
	model := reportModel{}
	for i := 0; i < 600; i++ {
		model.Tests = append(model.Tests, reportTest{Name: fmt.Sprintf("unique %d", i), Status: "Pass", SourceFile: "test/sum.test.js", Source: readSource(root, filepath.Join("test", "sum.test.js"), i+1, i+1)})
	}
	var output bytes.Buffer
	require.NoError(t, testdriveReport.Execute(&output, model))
	text := output.String()
	require.NotContains(t, text, `<template id="shared-`, "unique excerpts gain nothing from sharing")
	require.Contains(t, text, "unique-599")
}

func TestReportSearchMatchesSharedEvidenceEverywhere(t *testing.T) {
	// Searching and suite-to-test navigation must agree on what matches;
	// otherwise following a link clears a query that matches shared evidence.
	require.Contains(t, reportHTML, "function matchesQuery(item) {")
	require.Contains(t, reportHTML, "state.items.filter(matchesQuery)")
	require.Contains(t, reportHTML, "if (!matchesQuery(target)) {")
	require.NotContains(t, reportHTML, "target.text.includes(query)")
}
