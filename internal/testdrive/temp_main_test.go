package testdrive

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testutil"
)

func TestMain(m *testing.M) {
	// Java setups name a fixed latest dd-java-agent release, read offline.
	onboard.SetJavaReleaseResolver(func(context.Context) (string, error) { return "1.68.1", nil })
	if os.Getenv("DDTEST_ISOLATED_TEST_TMP") == "1" {
		os.Exit(m.Run())
	}
	root, err := os.MkdirTemp("", "ddtest-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("DDTEST_ISOLATED_TEST_TMP", "1")
	testutil.SetBinaryDirectory(root)
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		_ = os.Setenv(key, root)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
