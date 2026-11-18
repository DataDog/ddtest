package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/net"
)

// The suite of a map is the test command without what the map's
// configuration leaves out: coverage, -v, -count, -json or -timeout change
// nothing, and neither does the order of the package patterns; the working
// directory, the packages and the -run and -skip patterns do.
func TestLocalTIASuiteID(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	suite := func(dir, goflags string, args ...string) string {
		opts, err := parseOptions(args, goflags)
		if err != nil {
			t.Fatal(err)
		}
		return localTIASuiteID(opts, goflags, root, dir)
	}
	base := suite(root, "", "./...")
	for _, same := range []string{
		suite(root, "", "-v", "-count=1", "-json", "-timeout=1m", "-cover", "-covermode=atomic", "-coverpkg=./...", "./..."),
		suite(root, "-count=1 -v", "./..."),
	} {
		if same != base {
			t.Errorf("suite changed with flags that the map leaves out")
		}
	}
	if suite(root, "", "./a", "./b") != suite(root, "", "./b", "./a") {
		t.Error("the order of the package patterns changed the suite")
	}
	for name, other := range map[string]string{
		"directory": suite(filepath.Join(root, "sub"), "", "./..."),
		"packages":  suite(root, "", "./a/..."),
		"-run":      suite(root, "", "-run", "^TestA$", "./..."),
		"-run=":     suite(root, "", "-run=^TestB$", "./..."),
		"-skip":     suite(root, "", "-skip=TestB", "./..."),
		"GOFLAGS":   suite(root, "-run=^TestA$", "./..."),
	} {
		if other == base {
			t.Errorf("%s: same suite", name)
		}
	}
	if len(base) != 64 {
		t.Fatalf("suite %q", base)
	}
}

// git runs git in dir and returns its output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=TIA", "-c", "user.email=tia@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A lookup names the run's commit and its most recent ancestors, nearest
// first, up to 20. With only the run's commit, as in a shallow clone, the
// repository is expanded first, once.
func TestLocalTIALookupCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main")
	var commits []string
	for i := range 25 {
		git(t, dir, "commit", "--allow-empty", "-m", fmt.Sprint(i))
		commits = append(commits, git(t, dir, "rev-parse", "HEAD"))
	}
	slices.Reverse(commits)
	unshallowed := 0
	original := unshallowRepository
	unshallowRepository = func() (bool, error) { unshallowed++; return false, nil }
	t.Cleanup(func() { unshallowRepository = original })
	remote := &localTIARemote{commits: []string{commits[0]}, root: dir, debug: nil}
	if got := remote.lookupCommits(context.Background()); !slices.Equal(got, commits[:20]) || unshallowed != 0 {
		t.Fatalf("commits %q, unshallowed %d", got, unshallowed)
	}
	remote.commits = []string{commits[22]}
	if got := remote.lookupCommits(context.Background()); !slices.Equal(got, commits[22:]) {
		t.Fatalf("an older commit: %q", got)
	}
	remote.commits = []string{commits[24]}
	if got := remote.lookupCommits(context.Background()); !slices.Equal(got, commits[24:]) || unshallowed != 1 {
		t.Fatalf("one commit: %q, unshallowed %d", got, unshallowed)
	}
	missing := strings.Repeat("ab", 20)
	remote.commits = []string{missing}
	if got := remote.lookupCommits(context.Background()); !slices.Equal(got, []string{missing}) || unshallowed != 2 {
		t.Fatalf("a commit that Git lacks: %q, unshallowed %d", got, unshallowed)
	}
}

// fakeArtifacts is a TIA artifacts API in memory.
type fakeArtifacts struct {
	stored  []byte
	uploads int
	err     error
}

func (f *fakeArtifacts) RepositoryID() string { return "https://example.invalid/repo.git" }

func (f *fakeArtifacts) LookupTIAArtifact(_ context.Context, _ net.TIAArtifactKey, commits []string) (*net.TIAArtifact, error) {
	if f.err != nil || f.stored == nil {
		return nil, f.err
	}
	return &net.TIAArtifact{Content: f.stored, MatchedCommit: commits[0], SourceCommit: commits[0]}, nil
}

func (f *fakeArtifacts) StoreTIAArtifact(_ context.Context, _ net.TIAArtifactKey, _ string, content []byte) error {
	f.uploads++
	f.stored = content
	return f.err
}

// An upload is the map's JSON without stats; a lookup returns it without
// stats. A map too large is not sent, with a warning, and a failed request
// only logs.
func TestLocalTIARemoteUpload(t *testing.T) {
	m, _ := storedMap()
	m.Files["lib/a.go"] = tiaFile{Size: 3, ModTime: 7, Hash: "a1"}
	artifacts := &fakeArtifacts{}
	remote := &localTIARemote{client: artifacts, commits: []string{strings.Repeat("1", 40)}, debug: nil}
	var warnings []string
	warn := func(message string) { warnings = append(warnings, message) }
	remote.upload(context.Background(), m, warn)
	if artifacts.uploads != 1 || len(warnings) != 0 || !json.Valid(artifacts.stored) || bytes.Contains(artifacts.stored, []byte("mtimes")) {
		t.Fatalf("uploads %d warnings %q stored %s", artifacts.uploads, warnings, artifacts.stored)
	}
	got := remote.lookup(context.Background())
	if got == nil || got.Files["lib/a.go"] != (tiaFile{Hash: "a1"}) || len(got.Packages["ex/p"].Tests) != len(m.Packages["ex/p"].Tests) {
		t.Fatalf("lookup %+v", got)
	}
	original := localTIAMaxUpload
	localTIAMaxUpload = 10
	t.Cleanup(func() { localTIAMaxUpload = original })
	remote.upload(context.Background(), m, warn)
	if artifacts.uploads != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "not shared") {
		t.Fatalf("oversized: uploads %d warnings %q", artifacts.uploads, warnings)
	}
	localTIAMaxUpload = original
	artifacts.err = fmt.Errorf("unavailable")
	remote.upload(context.Background(), m, warn)
	if got := remote.lookup(context.Background()); got != nil || len(warnings) != 1 {
		t.Fatalf("a failing API: %+v, warnings %q", got, warnings)
	}
}

// Without a client, as without an API key, there is no remote: no request.
func TestLocalTIARemoteWithoutClient(t *testing.T) {
	original := newTIAArtifactsClient
	newTIAArtifactsClient = func() net.TIAArtifactsClient { return nil }
	t.Cleanup(func() {
		newTIAArtifactsClient = original
		utils.ResetHandedCLIGitStateForTesting()
	})
	opts, _ := parseOptions([]string{"./..."}, "")
	if remote := newLocalTIARemote(t.TempDir(), utils.CLIGitRecord{SourceRoot: t.TempDir()}, opts, "", t.TempDir(), "key", nil); remote != nil {
		t.Fatalf("remote %+v", remote)
	}
}
