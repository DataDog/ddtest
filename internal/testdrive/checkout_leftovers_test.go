package testdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckoutLeftoversNameNewUntrackedAndIgnoredEntries(t *testing.T) {
	root := t.TempDir()
	output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput()
	require.NoError(t, err, string(output))
	requireWriteFile(t, filepath.Join(root, ".gitignore"), "node_modules/\nplaywright-report/\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755))
	requireWriteFile(t, filepath.Join(root, "notes.txt"), "existing untracked file")
	before := checkoutEntries(root)
	require.NotNil(t, before)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "playwright-report"), 0755))
	requireWriteFile(t, filepath.Join(root, "playwright-report", "index.html"), "report")
	requireWriteFile(t, filepath.Join(root, "coverage.json"), "{}")
	requireWriteFile(t, filepath.Join(root, "node_modules", "pkg", "cache"), "inside an existing ignored directory")
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
	requireWriteFile(t, validationPath(root), "{}")
	require.Equal(t, []string{"coverage.json", "playwright-report/"}, checkoutLeftovers(before, checkoutEntries(root)))
}

func TestCheckoutLeftoversNeedGit(t *testing.T) {
	require.Nil(t, checkoutEntries(t.TempDir()))
	require.Nil(t, checkoutLeftovers(nil, map[string]bool{"new": true}))
}

func TestHandoffNamesCheckoutLeftovers(t *testing.T) {
	result := pairedReport("leftovers", "passed")
	result.Cleanup.Leftovers = []string{"playwright-report/"}
	require.Contains(t, validationHandoff(result), "Cleanup: passed (ddtest sessions only). Left in the checkout: playwright-report/; remove them unless they belong there.")
}
