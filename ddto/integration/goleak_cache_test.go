package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

func TestGoleakCacheAndWarmVersionGuard(t *testing.T) {
	driver := sharedDriver(t, "..")
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module example.com/goleak-cache\n\ngo 1.25.0\nrequire (\ngithub.com/tonyredondo/dd-ci-testing-poc v0.0.0\ngo.uber.org/goleak v1.3.0\n)\nreplace github.com/tonyredondo/dd-ci-testing-poc => %s\nreplace go.uber.org/goleak => go.uber.org/goleak v1.3.0\n", filepath.ToSlash(root))
	source := "package guard\nimport (\"testing\";\"go.uber.org/goleak\";_ \"github.com/tonyredondo/dd-ci-testing-poc/testopt\")\nfunc TestClean(t *testing.T){goleak.VerifyNone(t)}\n"
	for name, data := range map[string]string{"go.mod": mod, "guard_test.go": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "vendor"}} {
		out, stderr, code := command(t, dir, testEnv(), "go", args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
	}
	args := []string{"test", "--runtime=mini", "-mod=vendor", "-x", "-c", "-o", filepath.Join(dir, executableName("fixture.test")), "."}
	for i := 0; i < 2; i++ {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
		if code != 0 || i == 1 && len(compilerTraceLines(stderr)) != 0 {
			t.Fatalf("warm build %d: exit=%d\n%s\n%s", i, code, out, stderr)
		}
	}
	path := filepath.Join(dir, "vendor", "go.uber.org", "goleak", "leaks.go")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("\n// Changed preparation input for the cache regression.\n")
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
	if code != 0 || !strings.Contains(stderr, " -p go.uber.org/goleak ") {
		t.Fatalf("changed goleak input was not rebuilt: exit=%d\n%s\n%s", code, out, stderr)
	}
	for _, line := range compilerTraceLines(stderr) {
		for _, unrelated := range []string{"strings", "fmt", "crypto/sha256"} {
			if strings.Contains(line, " -p "+unrelated+" ") {
				t.Fatal("goleak invalidated unrelated cache", line)
			}
		}
	}
	for _, name := range []string{"go.mod", "vendor/modules.txt"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		changed := strings.ReplaceAll(string(data), "=> go.uber.org/goleak v1.3.0", "=> go.uber.org/goleak v1.2.1")
		if changed == string(data) {
			t.Fatal("replacement metadata absent", name)
		}
		if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The version check runs before the build, even with unchanged cached
	// sources: the unsupported version builds without the integration, and the
	// cached instrumented object is not reused.
	out, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
	if code != 0 || !strings.Contains(stderr, version.BuildLogPrefix+" WARN: goleak v1.2.1 is not instrumented") || strings.Contains(stderr, "tool-overlay") {
		t.Fatalf("cached unsupported version: exit=%d\n%s\n%s", code, out, stderr)
	}
	symbols, stderr, code := command(t, dir, testEnv(), "go", "tool", "nm", filepath.Join(dir, executableName("fixture.test")))
	if code != 0 || strings.Contains(symbols, "PrepareLeakCheck") {
		t.Fatalf("unsupported goleak still linked the leak-check hook: exit=%d %s", code, stderr)
	}
}
