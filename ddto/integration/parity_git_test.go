package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both runtimes observe the same synthetic two-commit repository. No host Git
// configuration, signing keys or existing checkout are changed.
func prepareParityGit(t *testing.T, dir string) (string, string) {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		args = append([]string{"-c", "user.name=Parity Fixture", "-c", "user.email=parity@example.invalid", "-c", "commit.gpgsign=false"}, args...)
		out, stderr, code := command(t, dir, testEnv(), "git", args...)
		if code != 0 {
			t.Fatalf("fixture git: %v %s %s", args, out, stderr)
		}
		return strings.TrimSpace(out)
	}
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "CODEOWNERS"), []byte("*.go @ci-owners\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "Base fixture")
	base := run("rev-parse", "HEAD")
	file := filepath.Join(dir, "sample_test.go")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.ReplaceAll(string(body), "test.log", "test.log.modified"))
	if err = os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "sample_test.go")
	run("commit", "-m", "Modify a test body")
	return base, run("rev-parse", "HEAD")
}
