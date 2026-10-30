package testdrive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPytestRejectsEditableImportsFromOriginalCheckout(t *testing.T) {
	python := feedbackPython(t)
	dependencies, err := exec.CommandContext(t.Context(), python, "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])").Output()
	require.NoError(t, err)
	for _, mode := range []string{"pth", "legacy", "finder", "external editable", "noneditable"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.Mkdir("src", 0755))
			requireWriteFile(t, "src/app.py", "from pathlib import Path\ndef run():\n    Path(__file__).with_suffix('.executed').write_text('ran')\n")
			requireWriteFile(t, "test_app.py", "from app import run\ndef test_app():\n    run()\n")
			requireWriteFile(t, "pytest.ini", "[pytest]\n")
			environment := filepath.Join(t.TempDir(), "venv")
			output, err := exec.CommandContext(t.Context(), python, "-m", "venv", "--without-pip", environment).CombinedOutput()
			require.NoError(t, err, string(output))
			selected := environmentPython(environment)
			site, err := exec.CommandContext(t.Context(), selected, "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])").Output()
			require.NoError(t, err)
			packages := strings.TrimSpace(string(site))
			requireWriteFile(t, filepath.Join(packages, "dependencies.pth"), strings.TrimSpace(string(dependencies))+"\n")
			if mode == "pth" || mode == "legacy" {
				requireWriteFile(t, filepath.Join(packages, "project.pth"), filepath.Join(root, "src")+"\n")
				if mode == "legacy" {
					requireWriteFile(t, filepath.Join(packages, "project.egg-link"), root+"\n.\n")
				}
			} else {
				project := root
				if mode == "external editable" {
					project = t.TempDir()
				}
				projectURL := filepath.ToSlash(project)
				if !strings.HasPrefix(projectURL, "/") {
					projectURL = "/" + projectURL
				}
				direct, err := json.Marshal(map[string]any{"url": (&url.URL{Scheme: "file", Path: projectURL}).String(), "dir_info": map[string]bool{"editable": mode != "noneditable"}})
				require.NoError(t, err)
				metadata := filepath.Join(packages, "project-1.0.dist-info")
				require.NoError(t, os.Mkdir(metadata, 0755))
				requireWriteFile(t, filepath.Join(metadata, "METADATA"), "Name: project\nVersion: 1.0\n")
				requireWriteFile(t, filepath.Join(metadata, "direct_url.json"), string(direct))
				if mode == "finder" {
					mapping, err := json.Marshal(filepath.Join(root, "src", "app.py"))
					require.NoError(t, err)
					requireWriteFile(t, filepath.Join(packages, "project.pth"), "import project_finder; project_finder.install()\n")
					requireWriteFile(t, filepath.Join(packages, "project_finder.py"), "import importlib.util, sys\nclass Finder:\n    @classmethod\n    def find_spec(cls, name, path=None, target=None):\n        if name == 'app':\n            return importlib.util.spec_from_file_location(name, "+string(mapping)+")\ndef install():\n    sys.meta_path.append(Finder)\n")
				}
			}
			blocked := mode != "external editable" && mode != "noneditable"
			if blocked {
				output, err := exec.CommandContext(t.Context(), selected, "-c", "import app; print(app.__file__)").Output()
				require.NoError(t, err)
				require.Contains(t, string(output), filepath.Join(root, "src"), "fixture must reproduce the original-checkout import")
			}
			drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: selected, args: []string{"-m", "pytest"}, executor: &ext.DefaultCommandExecutor{}, startIntake: func(string, intake.Scenario) (localIntake, error) {
				t.Fatal("editable checkout must be rejected before execution")
				return nil, nil
			}}
			drive.preflight = drive.frameworkPreflight()
			if blocked {
				var output bytes.Buffer
				result, err := drive.runPreparedConfiguration(t.Context(), &output, &configurationResult{TestCommand: onboard.TestCommand{Directory: "."}})
				require.ErrorContains(t, err, "editable installation")
				require.Equal(t, "incompatible", result.Preflight.Verdict.Status)
				require.Empty(t, result.Runs)
			} else {
				drive.configurationSourceRoot = root
				require.NoError(t, drive.checkPytestPreflight(t.Context(), io.Discard, &validationResult{}))
			}
			require.NoFileExists(t, filepath.Join(root, "src", "app.executed"))
		})
	}
}
