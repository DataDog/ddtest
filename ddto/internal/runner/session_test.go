package runner

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

var timeZero time.Time

// The session's coverage is the coverage profile's statement total, which
// counts a block once although several test processes report it, covered
// if one did.
func TestProfileCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cover.out")
	profile := "mode: set\n" +
		"example.com/a/a.go:1.1,2.2 6 1\n" +
		"example.com/a/a.go:3.1,4.2 4 0\n" +
		"example.com/b/b.go:1.1,2.2 10 0\n" +
		// -coverpkg: another test process reports the same blocks.
		"example.com/a/a.go:3.1,4.2 4 1\n" +
		"example.com/b/b.go:1.1,2.2 10 0\n" +
		// A file name can contain spaces.
		"example.com/c/source file.go:1.1,2.2 20 1\n" +
		"example.com/c/other file.go:1.1,2.2 20 0\n"
	if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := profileCoverage(path); !ok || math.Abs(got-50) > 1e-9 {
		t.Fatalf("coverage %v, %t; want 50", got, ok)
	}
	for name, content := range map[string]string{"empty": "mode: set\n", "malformed": "mode: set\nexample.com/a/a.go:1.1,2.2 x 1\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := profileCoverage(path); ok {
			t.Errorf("%s profile gave a coverage", name)
		}
	}
	if _, ok := profileCoverage(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("a missing profile gave a coverage")
	}
}

// go test writes a relative -coverprofile in -outputdir, which is relative to
// its directory.
func TestCoverProfilePath(t *testing.T) {
	// Absolute on every system, with a volume on Windows.
	dir, abs := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		profile, output, want string
	}{
		{"", "", ""},
		{"c.out", "", filepath.Join(dir, "c.out")},
		{"c.out", "out", filepath.Join(dir, "out", "c.out")},
		{"c.out", abs, filepath.Join(abs, "c.out")},
		{filepath.Join(abs, "p", "c.out"), "out", filepath.Join(abs, "p", "c.out")},
	} {
		if got := coverProfilePath(options{coverProfile: tc.profile, outputDir: tc.output}, dir); got != tc.want {
			t.Errorf("profile %q outputdir %q: %q, want %q", tc.profile, tc.output, got, tc.want)
		}
	}
}

// The test processes report their own sessions, as without ddto, wherever
// ddto cannot own the command's.
func TestStartTestSessionRefusals(t *testing.T) {
	t.Setenv("DD_CIVISIBILITY_ENABLED", "true")
	t.Setenv("DD_SERVICE", "svc")
	mini := CLIOptions{Runtime: Mini}
	for name, tc := range map[string]struct {
		cli  CLIOptions
		opts options
		plan Plan
	}{
		"sdk":                {CLIOptions{Runtime: SDK}, options{}, Plan{}},
		"session per binary": {CLIOptions{Runtime: Mini, SessionPerBinary: true}, options{}, Plan{}},
		"compile only":       {mini, options{compileOnly: true}, Plan{}},
		"print only":         {mini, options{printOnly: true}, Plan{}},
		"launcher":           {mini, options{}, Plan{testLauncher: true}},
		"old runtime":        {mini, options{}, Plan{sessionRefusal: "the runtime's civisibility/utils package lacks HandedCLISession"}},
	} {
		if session, refusal := startTestSession(tc.cli, tc.opts, tc.plan, t.TempDir(), timeZero, os.Stderr); session != nil || refusal == "" {
			t.Errorf("%s: owned the session", name)
		}
	}
	t.Setenv("DD_SERVICE", "")
	t.Setenv("DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS", "true")
	if session, _ := startTestSession(mini, options{}, Plan{}, t.TempDir(), timeZero, os.Stderr); session != nil {
		t.Error("CODEOWNERS services: owned the session")
	}
	t.Setenv("DD_SERVICE", "svc")
	t.Setenv("DD_CIVISIBILITY_ENABLED", "false")
	if session, _ := startTestSession(mini, options{}, Plan{}, t.TempDir(), timeZero, os.Stderr); session != nil {
		t.Error("CI Visibility disabled: owned the session")
	}
	t.Setenv("DD_CIVISIBILITY_ENABLED", "true")
	// A relative TMPDIR still gives the session an absolute directory, which
	// test processes in other directories read.
	t.Chdir(t.TempDir())
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, ".")
	}
	session, refusal := startTestSession(mini, options{}, Plan{}, t.TempDir(), timeZero, os.Stderr)
	if session == nil {
		t.Fatalf("not owned: %s", refusal)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.dir) })
	if session.session.ID == 0 || session.session.Command == "" || !filepath.IsAbs(session.dir) {
		t.Fatalf("session %+v in %s", session.session, session.dir)
	}
	if _, err := os.Stat(filepath.Join(session.dir, "session.json")); err != nil {
		t.Fatal(err)
	}
}

// The session takes its Git metadata from go test's directory: a test package
// in another checkout, or in one while that directory is in none, gives each
// test process its own session.
func TestSameCheckout(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"repo/pkg", "repo/nested/pkg", "plain/pkg"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"repo", "repo/nested"} {
		if err := os.Mkdir(filepath.Join(root, dir, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	packages := func(dirs ...string) []utils.CLITestPackage {
		var list []utils.CLITestPackage
		for _, dir := range dirs {
			list = append(list, utils.CLITestPackage{Dir: filepath.Join(root, dir)})
		}
		return list
	}
	for _, tc := range []struct {
		dir      string
		packages []utils.CLITestPackage
		want     bool
	}{
		{"repo", packages("repo", "repo/pkg"), true},
		{"repo", packages("repo/pkg", "repo/nested/pkg"), false},
		{"repo", packages("repo/pkg", "plain/pkg"), false},
		{".", packages("repo/pkg"), false},
		{".", packages("plain/pkg"), true},
		{"plain", nil, true},
	} {
		if got := sameCheckout(filepath.Join(root, tc.dir), tc.packages); got != tc.want {
			t.Errorf("%s with %v: %t, want %t", tc.dir, tc.packages, got, tc.want)
		}
	}
	t.Setenv("DD_CIVISIBILITY_ENABLED", "true")
	t.Setenv("DD_SERVICE", "svc")
	plan := Plan{}
	plan.tests.Packages = packages("repo/pkg")
	if session, refusal := startTestSession(CLIOptions{Runtime: Mini}, options{}, plan, root, timeZero, os.Stderr); session != nil || !strings.Contains(refusal, "Git checkout") {
		t.Fatalf("owned the session of another checkout's tests: %q", refusal)
	}
}

// The hooks that close CI Visibility before a crash need the exit hook,
// which keeps the crash's exit status.
func TestCrashHooksNeedTheExitHook(t *testing.T) {
	if refusal := crashHookRefusal("internal/runtime/exithook lacks func Add(Hook)"); !strings.Contains(refusal, "exit hook") || !strings.Contains(refusal, "lacks func Add") {
		t.Fatalf("refusal %q", refusal)
	}
	if refusal := crashHookRefusal(""); refusal != "" {
		t.Fatalf("refusal %q", refusal)
	}
	if refusal := timeoutHookRefusal(nil, nil); !strings.Contains(refusal, "exit hook") {
		t.Fatalf("timeout hook without the exit hook: %q", refusal)
	}
	if refusal, err := preparePanicHook(nil, nil, t.TempDir(), nil); err != nil || !strings.Contains(refusal, "exit hook") {
		t.Fatalf("panic hook without the exit hook: %q, %v", refusal, err)
	}
}
