package releaseworkflow

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// release-commit.sh makes the release commit on the checked-out commit, after
// checking that go install builds it: go.mod requires ddto at the version,
// without the replacement, go.sum has its checksums, and ddto reports the
// version. The script runs in a clone of the checkout, as shallow as CI's,
// and leaves no tag behind.
func TestReleaseCommit(t *testing.T) {
	const version = "v0.999.999"
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	head := git(root, "rev-parse", "HEAD")
	repo := filepath.Join(t.TempDir(), "repo")
	git(root, "clone", "-q", "--shared", "--no-checkout", root, repo)
	git(repo, "checkout", "-q", "--detach", head)

	cmd := exec.Command("bash", filepath.Join(root, ".github", "scripts", "release-commit.sh"))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "VERSION="+version)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("release-commit.sh: %v\n%s", err, stderr.String())
	}
	commit := strings.TrimSpace(string(out))
	if parent := git(repo, "rev-parse", commit+"^"); parent != head {
		t.Fatalf("release commit %s is on %s, want %s", commit, parent, head)
	}
	if changed := git(repo, "diff", "--name-only", head, commit); changed != "ddto/internal/version/version.go\ngo.mod\ngo.sum" {
		t.Fatalf("release commit changes %q", changed)
	}
	goMod := git(repo, "show", commit+":go.mod")
	if strings.Contains(goMod, "replace") || !strings.Contains(goMod, "github.com/DataDog/ddtest/ddto "+version+"\n") {
		t.Fatalf("release go.mod:\n%s", goMod)
	}
	goSum := git(repo, "show", commit+":go.sum")
	for _, line := range []string{"github.com/DataDog/ddtest/ddto " + version + " h1:", "github.com/DataDog/ddtest/ddto " + version + "/go.mod h1:"} {
		if !strings.Contains(goSum, line) {
			t.Fatalf("release go.sum lacks %q", line)
		}
	}
	if source := git(repo, "show", commit+":ddto/internal/version/version.go"); !strings.Contains(source, `const Number = "0.999.999"`) {
		t.Fatalf("release version.go:\n%s", source)
	}
	if tags := git(repo, "tag", "-l", version, "ddto/"+version); tags != "" {
		t.Fatalf("local tags left: %s", tags)
	}
}
