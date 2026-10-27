// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testing exits with status 2 when it cannot create the profile, so ddto sets
// one only in a temporary directory where it created the file.
func TestSetTemporaryCoverProfile(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	value := flags.String("test.coverprofile", "", "")
	missing := filepath.Join(t.TempDir(), "missing")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, missing)
	}
	if path, err := setTemporaryCoverProfile(flags.Lookup("test.coverprofile").Value); err == nil || *value != "" {
		t.Fatalf("with a missing temporary directory: %q %v; the flag is %q", path, err, *value)
	}

	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	path, err := setTemporaryCoverProfile(flags.Lookup("test.coverprofile").Value)
	if err != nil || *value != path || filepath.Dir(path) != dir {
		t.Fatalf("profile %q %v; the flag is %q", path, err, *value)
	}

	// A relative temporary directory gives an absolute profile, which a test
	// that changes directory does not move.
	t.Chdir(filepath.Dir(dir))
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, filepath.Base(dir))
	}
	*value = ""
	relative, err := setTemporaryCoverProfile(flags.Lookup("test.coverprofile").Value)
	got, _ := filepath.EvalSymlinks(filepath.Dir(relative))
	want, _ := filepath.EvalSymlinks(dir)
	if err != nil || !filepath.IsAbs(relative) || got != want {
		t.Fatalf("with a relative temporary directory: %q %v", relative, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("the profile starts empty: %v", err)
	}
	flags.Visit(func(f *flag.Flag) { t.Fatalf("flag.Visit reports %s", f.Name) })
}

// The directory of coverage captures stays where it was created when a test
// changes the working directory, also with a relative TMPDIR.
func TestCoverageTempDirectoryIsAbsolute(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	t.Setenv("TMPDIR", "tmp")
	t.Setenv("TMP", "tmp")
	t.Setenv("TEMP", "tmp")
	if err := os.Mkdir("tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := mkdirCoverageTemp("coverage")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) || !strings.HasPrefix(dir, filepath.Join(base, "tmp")) {
		t.Fatalf("coverage directory %q", dir)
	}
}

// A TestMain that changes directory before M.Run leaves the module resolved
// from the package's directory, where the process started.
func TestModuleInfoUsesTheStartingDirectory(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	start := testPackageDirectory
	t.Chdir(t.TempDir())
	deferModuleInfo()
	moduleInfoSource.Lock()
	dir := moduleInfoSource.dir
	moduleInfoSource.pending = false
	moduleInfoSource.Unlock()
	if dir != start {
		t.Fatalf("module resolved from %s; want %s", dir, start)
	}
}

// Rewriting a profile, as backfill and retry merges do, writes through a
// symbolic link as testing does and keeps the profile's mode.
func TestProfileRewriteKeepsLinkAndMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "artifacts", "coverage.out")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("mode: set\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	// Windows reports a writable file as 0666.
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "coverage.out")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symbolic links:", err)
	}
	profile := &orderedCoverProfile{lines: []orderedCoverProfileLine{{raw: "mode: count"}}}
	if err := profile.writeAtomic(link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", info, err)
	}
	data, err := os.ReadFile(target)
	info, statErr := os.Stat(target)
	if err != nil || statErr != nil || string(data) != "mode: count\n" || info.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("target %q, mode %v: %v %v", data, info.Mode(), err, statErr)
	}
}
