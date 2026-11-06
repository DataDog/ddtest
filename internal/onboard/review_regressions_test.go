package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonDefaultReleaseIsPinnedInHandoff(t *testing.T) {
	usePythonRelease(t, "4.20.2", nil)
	gitLabPythonRepository(t, "test:\n  image: python:3.12\n  script: python -m pytest\n")
	var output bytes.Buffer
	_ = StartWithOptions(&output, StartOptions{Format: "json"})
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Contains(t, output.String(), `PYTHON_TRACER_VERSION=\"4.20.2\"`)
	require.NotEmpty(t, result.ResumeCommand, output.String())
	for _, command := range [][]string{result.NextCommand, result.ResumeCommand, result.AcceptResumeCommand} {
		if len(command) == 0 {
			continue
		}
		index := slices.Index(command, "--tracer-version")
		require.NotEqual(t, -1, index, "%v must retain the prescribed release", command)
		require.Equal(t, "4.20.2", command[index+1])
	}
}

func TestPythonSetupUsesInstalledTracerFromSelectedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Python executables")
	}
	for _, mode := range []string{"PATH", "activated", "uv"} {
		for _, requested := range []string{"", "latest", "4.19.0"} {
			t.Run(mode+"/"+requested, func(t *testing.T) {
				reads := usePythonRelease(t, "4.20.2", nil)
				t.Cleanup(SetPythonInstalledReleaseResolver(installedPythonRelease))
				root := t.TempDir()
				t.Chdir(root)
				writeFile(t, root, ".git", "gitdir: fixture\n")
				tools := filepath.Join(root, "tools")
				fake := func(path, release string, uv bool) {
					script := "#!/bin/sh\n"
					if uv {
						script += "[ \"$1\" = run ] && [ \"$2\" = --no-sync ] && [ \"$3\" = python ] || exit 9\n"
					}
					script += "for last; do :; done\nprintf '%s' '" + release + "' > \"$last\"\n"
					writeFile(t, root, path, script)
					require.NoError(t, os.Chmod(filepath.Join(root, path), 0755))
				}
				fake("tools/python", "4.12.1", false)
				fake("environment/bin/python", "4.16.1", false)
				fake("tools/uv", "4.17.1", true)
				t.Setenv("PATH", tools)
				t.Setenv("VIRTUAL_ENV", "")
				t.Setenv("CONDA_PREFIX", "")
				want := "4.12.1"
				if mode != "PATH" {
					t.Setenv("VIRTUAL_ENV", filepath.Join(root, "environment"))
					want = "4.16.1"
				}
				if mode == "uv" {
					writeFile(t, root, "uv.lock", "")
					want = "4.17.1"
				}
				selected, err := SelectPythonRelease(requested)
				require.NoError(t, err)
				require.Equal(t, want, selected)
				require.Zero(t, *reads, "an installed tracer needs no PyPI lookup")
				require.Contains(t, pythonDiscovery{}.agentInstructions("pytest", ""), "python-tracer-version: '"+want+"'")
				require.Equal(t, want, pythonSetupRequest(root, "pytest", "", gitLabCI{}.setupTerms()).TracerVersion)
			})
		}
	}
}

func TestJavaScriptUnresolvedSiteRemainsACaveat(t *testing.T) {
	workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "languages: js", "languages: js\n          site: ${{ vars.DD_SITE }}", 1)
	root := newJestRepository(t, workflow)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.16.0", Node: ">=18"}, nil
	}).WithSiteCaveats("datadoghq.eu")
	require.Equal(t, StatusCompatible, check.Status)
	caveats := strings.Join(CISetupCaveats(&check), "\n")
	require.Contains(t, caveats, "Datadog site:")
	require.Contains(t, caveats, "vars.DD_SITE")
	require.Contains(t, caveats, "could not be resolved")
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, workflow, string(data))
}

func TestMavenDaemonSpecificOptionsDoNotChangeReplayScope(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pom.xml", strings.Replace(javaJUnit5POM, "</project>", "<build><defaultGoal>test</defaultGoal></build></project>", 1))
	for _, executable := range mavenDaemonExecutables {
		require.True(t, javaStep(t, root, executable).Matched, "the fixture default goal runs tests")
		require.Equal(t, []string{"mvn", "--log-file", "--stop", "test"}, mavenDaemonAsMaven([]string{executable, "--log-file", "--stop", "test"}))
		resolution := javaStep(t, root, executable+" --raw-streams -B test")
		require.True(t, resolution.Matched, resolution.Reason)
		require.Len(t, resolution.Tests, 1)
		require.Equal(t, "mvn -B test", resolution.Tests[0].Command)
		for _, control := range []string{"--status", "--stop"} {
			command := executable + " " + control
			resolution = javaStep(t, root, command)
			require.False(t, resolution.Matched, command)
			require.Empty(t, resolution.Reason, command)
			require.False(t, javaShellMayRunTests(root, root, command, nil, true, 0), command)
		}
	}
}
