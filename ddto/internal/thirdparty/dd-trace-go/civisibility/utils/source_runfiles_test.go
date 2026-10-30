package utils

import "testing"

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
	for _, unchanged := range []SourceFilePath{
		{RuntimePath: "pkg/a_test.go", FilesystemPath: "/workspace/pkg/a_test.go", RelativePath: "pkg/a_test.go", FilesystemKnown: true},
		{RuntimePath: "pkg/missing_test.go", FilesystemPath: "pkg/missing_test.go", RelativePath: "pkg/missing_test.go"},
		{},
	} {
		if got := withRunfilesSource(unchanged, rlocation); got != unchanged {
			t.Errorf("%+v changed to %+v", unchanged, got)
		}
	}
}
