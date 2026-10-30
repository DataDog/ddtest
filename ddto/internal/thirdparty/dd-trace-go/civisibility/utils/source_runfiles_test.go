package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithRunfilesSource(t *testing.T) {
	rlocation := func(logical string) (string, bool) {
		if logical == "pkg/a_test.go" {
			return "/runfiles/_main/pkg/a_test.go", true
		}
		return "", false
	}
	relative := SourceFilePath{RuntimePath: "pkg/a_test.go", FilesystemPath: "pkg/a_test.go", RelativePath: "pkg/a_test.go"}
	if got := withRunfilesSource(relative, rlocation); got.FilesystemPath != "/runfiles/_main/pkg/a_test.go" || !got.FilesystemKnown || got.RelativePath != "pkg/a_test.go" {
		t.Fatalf("relative source not found in runfiles: %+v", got)
	}
	// A workspace tag can name a checkout that the sandbox does not have.
	missing := SourceFilePath{RuntimePath: "pkg/a_test.go", FilesystemPath: filepath.Join(t.TempDir(), "pkg", "a_test.go"), RelativePath: "pkg/a_test.go", FilesystemKnown: true}
	if got := withRunfilesSource(missing, rlocation); got.FilesystemPath != "/runfiles/_main/pkg/a_test.go" || !got.FilesystemKnown || got.RelativePath != "pkg/a_test.go" {
		t.Fatalf("a missing workspace file kept the runfile from being found: %+v", got)
	}
	present := filepath.Join(t.TempDir(), "a_test.go")
	if err := os.WriteFile(present, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, unchanged := range []SourceFilePath{
		{RuntimePath: "pkg/a_test.go", FilesystemPath: present, RelativePath: "pkg/a_test.go", FilesystemKnown: true},
		{RuntimePath: "pkg/missing_test.go", FilesystemPath: "pkg/missing_test.go", RelativePath: "pkg/missing_test.go"},
		{},
	} {
		if got := withRunfilesSource(unchanged, rlocation); got != unchanged {
			t.Errorf("%+v changed to %+v", unchanged, got)
		}
	}
}
