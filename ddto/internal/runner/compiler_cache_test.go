package runner

import (
	"strings"
	"testing"
)

// The marker keeps the user's last matching gcflags for the package, quoting
// each flag as go would read it back, and rejects gcflags that go rejects.
func TestPackageCompilerCacheFlag(t *testing.T) {
	pkg := &goPackage{ImportPath: "example.com/p", commandLine: true}
	for _, tc := range []struct {
		goflags string
		build   []string
		want    string
		err     string
	}{
		{build: []string{"-gcflags", "-N"}, want: "-gcflags=example.com/p=-N marker"},
		{goflags: "-gcflags=all=-l", build: []string{"-gcflags=example.com/other=-N"}, want: "-gcflags=example.com/p=-l marker"},
		{build: []string{`-gcflags=all='-d=a b'`}, want: "-gcflags=example.com/p='-d=a b' marker"},
		{build: []string{`-gcflags=all="-d=it's b"`}, want: `-gcflags=example.com/p="-d=it's b" marker`},
		{build: []string{"-gcflags"}, err: "missing gcflags value"},
		{build: []string{"-gcflags=nopattern"}, err: "invalid gcflags pattern"},
		{build: []string{"-gcflags=all='-N"}, err: "unterminated"},
		{goflags: "'-gcflags", err: "unterminated"},
	} {
		t.Setenv("GOFLAGS", tc.goflags)
		got, err := packageCompilerCacheFlag(t.TempDir(), options{buildFlags: tc.build}, pkg, "marker")
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("GOFLAGS=%q %q: %q, %v; want an error with %q", tc.goflags, tc.build, got, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("GOFLAGS=%q %q: %q, %v; want %q", tc.goflags, tc.build, got, err, tc.want)
		}
	}
}
