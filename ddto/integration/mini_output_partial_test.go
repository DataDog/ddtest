package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line that a test writes to t.Output in parts, the last in a cleanup,
// stays one line, as under native go test, when logs are not collected.
func TestMiniKeepsPartialOutputLines(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "zz_output_partial_test.go"), []byte(`package fixture_test

import (
	"fmt"
	"testing"
)

func TestPartialOutput(t *testing.T) {
	w := t.Output()
	t.Cleanup(func() { fmt.Fprint(w, "world\n") })
	fmt.Fprint(w, "hello ")
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := execute(t, dir, compileMini(t, dir, driver), []string{"-test.v", "-test.run=^TestPartialOutput$"}, true, false)
	if got.code != 0 || !strings.Contains(got.out, "hello world\n") {
		t.Fatalf("exit %d; want one hello world line:\n%s\n%s", got.code, got.out, got.stderr)
	}
}
