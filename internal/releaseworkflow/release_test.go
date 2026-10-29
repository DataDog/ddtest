package releaseworkflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelease(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		mode string
		want string
	}{
		{name: "maintainer", mode: "validate"},
		{name: "admin", mode: "validate", env: map[string]string{"MOCK_ROLE": "admin"}},
		{name: "custom maintainer role", mode: "validate", env: map[string]string{"MOCK_ROLE": "dd-repo-owner"}},
		{name: "writer denied", env: map[string]string{"MOCK_ROLE": "write"}, want: "Maintain or Admin"},
		{name: "reader denied", env: map[string]string{"MOCK_ROLE": "read"}, want: "Maintain or Admin"},
		{name: "rerun denied", env: map[string]string{"MOCK_RERUN_ROLE": "write"}, want: "Maintain or Admin"},
		{name: "permission API failure", env: map[string]string{"MOCK_PERMISSION_FAILURE": "1"}, want: "permission lookup failed"},
		{name: "tag API failure", env: map[string]string{"MOCK_TAG_FAILURE": "1"}, want: "tag lookup failed"},
		{name: "duplicate tag", env: map[string]string{"MOCK_REFS": "refs/tags/v1.8.0"}, want: "already exists"},
		{name: "duplicate ddto tag", env: map[string]string{"MOCK_REFS": "refs/tags/ddto/v1.8.0"}, want: "Tag ddto/v1.8.0 already exists"},
		{name: "similar tag", mode: "validate", env: map[string]string{"MOCK_REFS": "refs/tags/v1.8.01"}},
		{name: "branch rejected", env: map[string]string{"GITHUB_REF": "refs/heads/feature"}, want: "dispatched from main"},
		{name: "fork rejected", env: map[string]string{"GITHUB_REPOSITORY": "someone/ddtest"}, want: "dispatched from main"},
		{name: "push rejected", env: map[string]string{"GITHUB_EVENT_NAME": "push"}, want: "dispatched from main"},
		{name: "missing prefix", env: map[string]string{"VERSION": "1.8.0"}, want: "Version must"},
		{name: "leading zero", env: map[string]string{"VERSION": "v01.8.0"}, want: "Version must"},
		{name: "shell input", env: map[string]string{"VERSION": "v1.8.0$(exit 42)"}, want: "Version must"},
		{name: "make input", env: map[string]string{"VERSION": "v1.8.0$(shell exit 42)"}, want: "Version must"},
		{name: "create", mode: "create"},
		{name: "tag creation race", mode: "create", env: map[string]string{"MOCK_CREATE_FAILURE": "1"}, want: "tag creation failed"},
		{name: "no release commit", mode: "create", env: map[string]string{"RELEASE_COMMIT": ""}, want: "RELEASE_COMMIT must be"},
		{name: "release commit elsewhere", mode: "create", env: map[string]string{"MOCK_PARENT": "other-sha"}, want: "RELEASE_COMMIT must be"},
		{name: "different tree", mode: "create", env: map[string]string{"MOCK_TREE": "other-tree"}, want: "is not the release commit's"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write("gh", `#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$MOCK_LOG"
permission() {
  case "$1" in
    maintain|admin|dd-repo-owner) echo true ;;
    *) echo false ;;
  esac
}
case "$*" in
  *collaborators/original/permission*)
    if [[ "$MOCK_PERMISSION_FAILURE" == 1 ]]; then echo 'permission lookup failed' >&2; exit 1; fi
    permission "$MOCK_ROLE" ;;
  *collaborators/rerunner/permission*) permission "$MOCK_RERUN_ROLE" ;;
  *matching-refs*)
    if [[ "$MOCK_TAG_FAILURE" == 1 ]]; then echo 'tag lookup failed' >&2; exit 1; fi
    echo "$MOCK_REFS" ;;
  *git/blobs*) cat >> "$MOCK_LOG"; echo >> "$MOCK_LOG"; echo blob-sha ;;
  *git/trees*) cat >> "$MOCK_LOG"; echo >> "$MOCK_LOG"; echo "$MOCK_TREE" ;;
  *git/commits*) cat >> "$MOCK_LOG"; echo >> "$MOCK_LOG"; echo release-commit-sha ;;
  *git/refs*)
    if [[ "$MOCK_CREATE_FAILURE" == 1 ]]; then echo 'tag creation failed' >&2; exit 1; fi ;;
  'release create '*) ;;
  'release view '*) echo 'https://github.com/DataDog/ddtest/releases/tag/v1.8.0' ;;
  *) echo "unexpected call: $*" >&2; exit 1 ;;
esac
`)
			// The release commit, local-release-commit, changes go.mod on the
			// checked-out commit.
			write("git", `#!/usr/bin/env bash
case "$*" in
  'rev-parse HEAD') echo checked-out-main-sha ;;
  'rev-parse local-release-commit^') echo "$MOCK_PARENT" ;;
  'rev-parse local-release-commit^{tree}') echo verified-tree ;;
  'rev-parse checked-out-main-sha^{tree}') echo main-tree ;;
  'diff --name-only checked-out-main-sha local-release-commit') echo go.mod ;;
  'show local-release-commit:go.mod') echo 'module github.com/DataDog/ddtest' ;;
  'ls-tree local-release-commit -- go.mod') printf '100644 blob 0123\tgo.mod\n' ;;
  *) exit 1 ;;
esac
`)
			env := map[string]string{
				"PATH":              dir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GITHUB_REPOSITORY": "DataDog/ddtest", "GITHUB_REF": "refs/heads/main",
				"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_ACTOR": "original",
				"GITHUB_TRIGGERING_ACTOR": "rerunner", "VERSION": "v1.8.0",
				"GITHUB_STEP_SUMMARY": filepath.Join(dir, "summary"), "MOCK_LOG": filepath.Join(dir, "calls"),
				"MOCK_ROLE": "maintain", "MOCK_RERUN_ROLE": "maintain", "MOCK_REFS": "",
				"MOCK_PERMISSION_FAILURE": "0", "MOCK_TAG_FAILURE": "0", "MOCK_CREATE_FAILURE": "0",
				"RELEASE_COMMIT": "local-release-commit", "MOCK_PARENT": "checked-out-main-sha", "MOCK_TREE": "verified-tree",
			}
			for key, value := range tt.env {
				env[key] = value
			}
			cmd := exec.Command("bash", "../../.github/scripts/release.sh", tt.mode)
			for key, value := range env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			output, err := cmd.CombinedOutput()
			if tt.want != "" {
				if err == nil || !strings.Contains(string(output), tt.want) {
					t.Fatalf("got error %v, output %s; want %s", err, output, tt.want)
				}
			} else if err != nil {
				t.Fatalf("release failed: %v\n%s", err, output)
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			if tt.mode == "create" && tt.want == "" {
				// GitHub makes the release commit from its changed files on the
				// checked-out commit, and both tags name it, ddto's first.
				for _, want := range []string{
					`{"encoding":"base64","content":"bW9kdWxlIGdpdGh1Yi5jb20vRGF0YURvZy9kZHRlc3QK"}`,
					`{"base_tree":"main-tree","tree":[{"path":"go.mod","mode":"100644","type":"blob","sha":"blob-sha"}]}`,
					`{"message":"Release v1.8.0","tree":"verified-tree","parents":["checked-out-main-sha"]}`,
					"git/refs -f ref=refs/tags/ddto/v1.8.0 -f sha=release-commit-sha\n" +
						"api repos/DataDog/ddtest/git/refs -f ref=refs/tags/v1.8.0 -f sha=release-commit-sha",
					"release create v1.8.0 --verify-tag --target release-commit-sha --title v1.8.0 --draft --generate-notes dist/*",
				} {
					if !strings.Contains(string(calls), want) {
						t.Errorf("missing %q in calls: %s", want, calls)
					}
				}
			} else if strings.Contains(string(calls), "release create") || strings.Contains(string(calls), "-f ref=") && tt.name != "tag creation race" {
				t.Fatalf("unexpected tag or release creation: %s", calls)
			}
		})
	}
}

// Every release must publish the executables that customers download directly.
func TestReleaseAssets(t *testing.T) {
	assets := []string{"ddtest-linux-amd64", "ddtest-linux-arm64", "ddtest-darwin-amd64", "ddtest-darwin-arm64",
		"ddtest-windows-amd64.exe", "ddtest-windows-arm64.exe"}
	tests := []struct {
		name, drop, unlisted, want string
	}{
		{name: "complete"},
		{name: "missing executable", drop: "ddtest-linux-arm64", want: "ddtest-linux-arm64 is missing"},
		{name: "missing checksum", unlisted: "ddtest-windows-amd64.exe", want: "no line for ddtest-windows-amd64.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dist := t.TempDir()
			var sums strings.Builder
			for _, asset := range assets {
				if asset != tt.drop {
					if err := os.WriteFile(filepath.Join(dist, asset), []byte(asset), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if asset != tt.unlisted {
					sums.WriteString("0123456789abcdef  " + asset + "\n")
				}
			}
			if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("bash", "../../.github/scripts/check-release-assets.sh", dist).CombinedOutput()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("complete release rejected: %v\n%s", err, output)
				}
				return
			}
			if err == nil || !strings.Contains(string(output), tt.want) {
				t.Fatalf("err = %v, output = %s, want %q", err, output, tt.want)
			}
		})
	}
}
