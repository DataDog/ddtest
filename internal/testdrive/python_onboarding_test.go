package testdrive

import (
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestPythonStandalonePreparationKeepsUnsupportedCIAdvisory(t *testing.T) {
	python := feedbackPython(t)
	for _, mode := range []string{"default", "command", "all", "check-only"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
			requireWriteFile(t, filepath.Join(root, "test_app.py"), "def test_app():\n    pass\n")
			requireWriteFile(t, filepath.Join(root, ".gitlab-ci.yml"), "test:\n  script: python -m pytest\n")
			onboard.UseCIProvider("gitlab")
			t.Cleanup(func() { onboard.UseCIProvider("") })
			previous := settings.Get().Command
			t.Cleanup(func() { settings.Get().Command = previous })
			settings.Get().Command = ""
			if mode == "command" {
				settings.Get().Command = shellquote.Join(python, "-m", "pytest")
			}
			_, err := Prepare(PrepareOptions{All: mode == "all", CheckOnly: mode == "check-only", TracerVersion: "4.11.0"})
			if mode == "all" || mode == "check-only" {
				require.ErrorContains(t, err, "GitLab")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
