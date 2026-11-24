package bazeldoctor

import (
	"path/filepath"
	"strings"
	"testing"
)

var trickyPaths = []string{"/", "//", "///", "/a", "/a/", "/a/b", "/a/bc", "/a/b/c", "/a/./b", "/a/../b", "/a//b",
	"//x/a", "//x/b", "/ab", "a", "a/b", ".", "..", "./a", "a/..", "", "/a/b/.", "/a/b/..", "/a/b/c.json", "/a/bd/x"}

// The prefix fast paths answer as the component rules do.
func TestPlainPathFastPaths(t *testing.T) {
	posixOnly(t)
	for _, x := range trickyPaths {
		comparePathRules(t, x, "/a/b")
		for _, y := range trickyPaths {
			comparePathRules(t, x, y)
		}
	}
	for path, want := range map[string]bool{"/": true, "/a": true, "a/b": true, "": false, "//a": false, "/a/": false,
		"a/./b": false, "a/../b": false, ".": false, "..": false, "a//b": false} {
		if plainPath(path) != want {
			t.Errorf("plainPath(%q) = %v", path, !want)
		}
	}
}

func FuzzPlainPathFastPaths(f *testing.F) {
	for _, path := range trickyPaths {
		f.Add(path, "/a/b")
	}
	f.Fuzz(func(t *testing.T, x, y string) {
		// Both rules stat the same root; names the file system rejects fall
		// back in either.
		for part := range strings.SplitSeq(x+"/"+y, "/") {
			if len(part) > 200 || strings.ContainsRune(part, 0) {
				t.Skip()
			}
		}
		comparePathRules(t, x, y)
	})
}

func comparePathRules(t *testing.T, x, y string) {
	t.Helper()
	for _, paths := range [][]string{{x}, {x, y}, {y, x}, {x, y, x + "/z"}} {
		if got, want := commonRoot(paths), commonRootParts(paths); got != want {
			t.Fatalf("commonRoot(%q) = %q, want %q", paths, got, want)
		}
	}
	gotRelative, gotOK := sourceBelow(x, y)
	wantRelative, wantOK := relativeTo(x, y)
	if gotRelative != wantRelative || gotOK != wantOK {
		t.Fatalf("sourceBelow(%q, %q) = %q %v, want %q %v", x, y, gotRelative, gotOK, wantRelative, wantOK)
	}
	want, err := filepath.Rel(y, x)
	if err != nil {
		want = x
	}
	if got := relativePath(x, y); got != want {
		t.Fatalf("relativePath(%q, %q) = %q, want %q", x, y, got, want)
	}
}
