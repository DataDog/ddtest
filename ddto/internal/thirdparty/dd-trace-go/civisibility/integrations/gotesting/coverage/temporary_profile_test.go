// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"flag"
	"os"
	"path/filepath"
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
