package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writePythonEnvironmentFile(t *testing.T, name, value string) {
	t.Helper()
	require.NoError(t, os.WriteFile(name, []byte(value), 0600))
}

func TestPythonEnvironmentSelection(t *testing.T) {
	for _, tc := range []struct {
		name, active   string
		lock, explicit bool
		want           string
	}{
		{name: "PATH", want: "python"},
		{name: "uv lock", lock: true, want: "uv"},
		{name: "explicit overrides uv", lock: true, explicit: true, want: "custom-python"},
		{name: "activated venv", active: "VIRTUAL_ENV"},
		{name: "uv overrides activated venv", lock: true, active: "VIRTUAL_ENV", want: "uv"},
		{name: "activated conda", active: "CONDA_PREFIX"},
		{name: "uv overrides activated conda", lock: true, active: "CONDA_PREFIX", want: "uv"},
		{name: "explicit overrides uv and activated venv", lock: true, active: "VIRTUAL_ENV", explicit: true, want: "custom-python"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("VIRTUAL_ENV", "")
			t.Setenv("CONDA_PREFIX", "")
			if tc.lock {
				writePythonEnvironmentFile(t, filepath.Join(root, "uv.lock"), "")
			}
			want := tc.want
			if tc.active != "" {
				t.Setenv(tc.active, root)
				if want == "" {
					want = PythonEnvironmentExecutable(root)
				}
			}
			command := "python"
			if tc.explicit {
				command = "custom-python"
			}
			got, args, _ := PythonTestCommand(root, command, []string{"-m", "pytest"}, tc.explicit)
			require.Equal(t, want, got)
			if got == "uv" {
				require.Equal(t, []string{"run", "--no-sync", "python", "-m", "pytest"}, args)
			}
		})
	}
}

func TestUVLockDiscoveryStopsAtCheckoutBoundary(t *testing.T) {
	root := t.TempDir()
	writePythonEnvironmentFile(t, filepath.Join(root, "uv.lock"), "")
	member := filepath.Join(root, "packages", "member")
	require.NoError(t, os.MkdirAll(member, 0755))
	require.Equal(t, root, uvLockRoot(member))
	writePythonEnvironmentFile(t, filepath.Join(member, ".git"), "gitdir: elsewhere")
	require.Empty(t, uvLockRoot(member))
}

func TestParentLockSelectsUVWithoutProbing(t *testing.T) {
	root := t.TempDir()
	writePythonEnvironmentFile(t, filepath.Join(root, "uv.lock"), "")
	child := filepath.Join(root, "packages", "member")
	require.NoError(t, os.MkdirAll(child, 0755))
	t.Setenv("PATH", "")
	t.Setenv("VIRTUAL_ENV", "")
	t.Setenv("CONDA_PREFIX", "")
	command, args, source := PythonTestCommand(child, "python", []string{"-m", "pytest"}, false)
	require.Equal(t, "uv", command)
	require.Equal(t, []string{"run", "--no-sync", "python", "-m", "pytest"}, args)
	require.Equal(t, "uv project (uv.lock)", source)
}
