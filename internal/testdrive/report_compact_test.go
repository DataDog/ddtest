package testdrive

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLargeReportSharesEvidenceWithoutLosingEscapingOrCounts(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "one_test.rb"), "# shared-source-canary <script>alert(1)</script>\n")
	source := readSource(root, "one_test.rb", 1, 1)
	model := reportModel{CoverageLevel: "test"}
	files := []string{"lib/coverage-canary.rb"}
	for i := 1; i < 60; i++ {
		files = append(files, fmt.Sprintf("lib/file-%d.rb", i))
	}
	for i := 0; i < 1000; i++ {
		model.Tests = append(model.Tests, reportTest{Name: fmt.Sprintf("test_%d", i), Status: "Pass", SourceFile: "one_test.rb", Source: source, CoverageLevel: "test", CoveredFiles: files})
	}
	var output bytes.Buffer
	require.NoError(t, testdriveReport.Execute(&output, model))
	text := output.String()
	require.Equal(t, 1, strings.Count(text, "shared-source-canary"))
	require.Equal(t, 1, strings.Count(text, "lib/coverage-canary.rb"))
	require.Contains(t, text, `data-shared-detail="shared-source-0"`)
	require.Contains(t, text, `<template id="shared-coverage-0">`)
	require.NotContains(t, text, "<script>alert(1)</script>")
	require.Len(t, model.Tests[0].Source.Lines, 1, "rendering must not mutate evidence models")
	if path := os.Getenv("DDTEST_COMPACT_REPORT_OUTPUT"); path != "" {
		require.NoError(t, os.WriteFile(path, output.Bytes(), 0600))
	}
}
