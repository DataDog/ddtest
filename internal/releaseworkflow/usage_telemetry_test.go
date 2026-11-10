package releaseworkflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestReleaseWorkflowsPassPublicClientToken(t *testing.T) {
	for _, name := range []string{"release", "prerelease"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("../../.github/workflows/" + name + ".yml")
			require.NoError(t, err)
			var workflow struct{ Jobs map[string]ciTestJob }
			require.NoError(t, yaml.Unmarshal(data, &workflow))
			for _, step := range workflow.Jobs["release"].Steps {
				if step.Name == "Build binaries" {
					require.Equal(t, "${{ secrets.DDTEST_CLIENT_PUBLIC_TOKEN }}", step.Env["DDTEST_CLIENT_PUBLIC_TOKEN"])
					require.Contains(t, step.Run, "make release VERSION=")
					return
				}
			}
			t.Fatal("release has no binary build step")
		})
	}
}

func TestReleaseBuildPassesPublicClientTokenToEveryPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release workflows run in Ubuntu bash")
	}
	makefile, err := os.ReadFile("../../Makefile")
	require.NoError(t, err)
	for _, token := range []string{"", "pub-release-test-placeholder"} {
		name := "configured"
		if token == "" {
			name = "unconfigured"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range map[string]string{
				"Makefile": string(makefile), "scripts/install.sh": "", "scripts/install.ps1": "", "ddto/.keep": "",
				"bin/go": `#!/usr/bin/env bash
set -eu
[[ "$1" == -C ]]
cd "$2"
shift 2
flags= target= out=
while [[ $# -gt 0 ]]; do
  case "$1" in
    -ldflags) flags=$2; shift ;;
    -target) target=$2; shift ;;
    -o) out=$2; shift ;;
  esac
  shift
done
printf '%s\t%s\n' "$target" "$flags" >> "$RELEASE_FLAGS"
mkdir -p "$out"
suffix=
if [[ "$target" == windows/* ]]; then suffix=.exe; fi
touch "$out/ddtest-${target/\//-}$suffix"
`,
			} {
				path = filepath.Join(dir, path)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
			}
			flagsPath := filepath.Join(dir, "flags")
			t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GOBIN", filepath.Join(dir, "bin"))
			t.Setenv("DDTEST_CLIENT_PUBLIC_TOKEN", token)
			t.Setenv("RELEASE_FLAGS", flagsPath)
			cmd := exec.CommandContext(t.Context(), "make", "release", "VERSION=v1.8.0")
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			data, err := os.ReadFile(flagsPath)
			require.NoError(t, err)
			var targets []string
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				target, flags, ok := strings.Cut(line, "\t")
				require.True(t, ok, line)
				targets = append(targets, target)
				require.Equal(t, "-X github.com/DataDog/ddtest/internal/buildinfo.Version=v1.8.0 -X github.com/DataDog/ddtest/internal/usagetelemetry.ClientToken="+token, flags)
			}
			require.ElementsMatch(t, []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"}, targets)
		})
	}
}
