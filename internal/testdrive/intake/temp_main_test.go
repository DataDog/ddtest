package intake

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("DDTEST_ISOLATED_TEST_TMP") == "1" {
		os.Exit(m.Run())
	}
	root, err := os.MkdirTemp("", "ddtest-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("DDTEST_ISOLATED_TEST_TMP", "1")
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		_ = os.Setenv(key, root)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
