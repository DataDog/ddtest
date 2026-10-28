package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPytestConfigSelectionStaysInsideRepository(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.Mkdir(root, 0755))
	outside := filepath.Join(parent, "outside.ini")
	require.NoError(t, os.WriteFile(outside, []byte("[pytest]\naddopts = -p no:ddtrace\n"), 0644))
	_, err := PytestConfigOptions(root, root, "", []string{"pytest", "-c", "../outside.ini"})
	require.ErrorContains(t, err, "leaves the repository")
	if err := os.Symlink(outside, filepath.Join(root, "pytest.ini")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err = PytestConfigOptions(root, root, "", []string{"pytest"})
	require.ErrorContains(t, err, "leaves the repository")
}
