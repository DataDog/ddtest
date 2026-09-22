package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the public CLI in a subprocess so Cobra/Viper globals and process
// exits behave exactly as they do for a user. Only the runtime and backend are
// substituted; planning, discovery, saved plans, and execution are real.
func TestAutomaticDetectionCLIProcess(t *testing.T) {
	if os.Getenv("DDTEST_DETECTION_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"ddtest"}, os.Args[i+1:]...)
			break
		}
	}
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPlanAndRunAutomaticallyDetectJavaScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake runtime uses POSIX shell")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"attributes":{"itr_enabled":false,"tests_skipping":false,"require_git":false}}}`)
	}))
	defer server.Close()
	root := t.TempDir()
	for path, contents := range map[string]string{
		"package.json":           `{"devDependencies":{"jest":"29"},"scripts":{"test":"echo do-not-run-this-script"}}`,
		"sample.test.js":         "test('example', () => {})\n",
		"bin/node":               "#!/bin/sh\nif [ \"$1\" = --version ]; then echo v24.0.0; elif [ -n \"$3\" ]; then printf '%s' '{\"runtime.name\":\"node\",\"runtime.version\":\"24.0.0\"}' > \"$3\"; fi\n",
		"node_modules/.bin/jest": "#!/bin/sh\ncase \" $* \" in *' --listTests '*) printf '%s/sample.test.js\\n' \"$PWD\";; *) printf '%s\\n' \"$@\" > executed-args.txt;; esac\n",
	} {
		full := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0755))
	}
	initGit := exec.Command("git", "init", "--quiet", root)
	require.NoError(t, initGit.Run())
	binary, err := os.Executable()
	require.NoError(t, err)
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DD_") && !strings.HasPrefix(entry, "NODE_OPTIONS=") && !strings.HasPrefix(entry, "PATH=") {
			env = append(env, entry)
		}
	}
	env = append(env, "DDTEST_DETECTION_CLI_HELPER=1", "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "DD_TRACE_AGENT_URL="+server.URL, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_GIT_REPOSITORY_URL=https://example.com/detection.git", "DD_GIT_COMMIT_SHA=0123456789012345678901234567890123456789")
	for _, subcommand := range []string{"plan", "run"} {
		args := []string{"-test.run=^TestAutomaticDetectionCLIProcess$", "--", subcommand}
		if subcommand == "plan" {
			args = append(args, "--min-parallelism", "1", "--max-parallelism", "1")
		}
		command := exec.Command(binary, args...)
		command.Dir = root
		command.Env = env
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s: %s", subcommand, output)
		require.Contains(t, string(output), "framework=jest")
	}
	args, err := os.ReadFile(filepath.Join(root, "executed-args.txt"))
	require.NoError(t, err)
	require.Contains(t, string(args), "sample.test.js")
	require.NotContains(t, string(args), "--runInBand")
	// Adding a second runner must require selection, including when a plan exists.
	manifest := map[string]any{"devDependencies": map[string]string{"jest": "29", "vitest": "3"}}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0644))
	for _, subcommand := range []string{"plan", "run"} {
		command := exec.Command(binary, "-test.run=^TestAutomaticDetectionCLIProcess$", "--", subcommand)
		command.Dir = root
		command.Env = env
		output, err := command.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(output), "multiple test frameworks")
	}
	for _, tc := range []struct {
		name, envFramework string
		flags              []string
	}{
		{"environment", "jest", nil},
		{"flag overrides environment", "vitest", []string{"--framework", "jest"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-test.run=^TestAutomaticDetectionCLIProcess$", "--", "run"}, tc.flags...)
			command := exec.Command(binary, args...)
			command.Dir = root
			command.Env = append(append([]string{}, env...), "DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK="+tc.envFramework)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.Contains(t, string(output), "framework=jest")
		})
	}

}
