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
		{name: "writer denied", env: map[string]string{"MOCK_ROLE": "write"}, want: "Maintain or Admin"},
		{name: "reader denied", env: map[string]string{"MOCK_ROLE": "read"}, want: "Maintain or Admin"},
		{name: "rerun denied", env: map[string]string{"MOCK_RERUN_ROLE": "write"}, want: "Maintain or Admin"},
		{name: "permission API failure", env: map[string]string{"MOCK_PERMISSION_FAILURE": "1"}, want: "permission lookup failed"},
		{name: "tag API failure", env: map[string]string{"MOCK_TAG_FAILURE": "1"}, want: "tag lookup failed"},
		{name: "duplicate tag", env: map[string]string{"MOCK_REFS": "refs/tags/v1.8.0"}, want: "already exists"},
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
case "$*" in
  *collaborators/original/permission*)
    if [[ "$MOCK_PERMISSION_FAILURE" == 1 ]]; then echo 'permission lookup failed' >&2; exit 1; fi
    echo "$MOCK_ROLE" ;;
  *collaborators/rerunner/permission*) echo "$MOCK_RERUN_ROLE" ;;
  *matching-refs*)
    if [[ "$MOCK_TAG_FAILURE" == 1 ]]; then echo 'tag lookup failed' >&2; exit 1; fi
    echo "$MOCK_REFS" ;;
  *git/refs*)
    if [[ "$MOCK_CREATE_FAILURE" == 1 ]]; then echo 'tag creation failed' >&2; exit 1; fi ;;
  'release create '*) ;;
  'release view '*) echo 'https://github.com/DataDog/ddtest/releases/tag/v1.8.0' ;;
  *) echo "unexpected call: $*" >&2; exit 1 ;;
esac
`)
			write("git", "#!/usr/bin/env bash\n[[ \"$*\" == 'rev-parse HEAD' ]] || exit 1\necho checked-out-main-sha\n")
			env := map[string]string{
				"PATH":              dir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GITHUB_REPOSITORY": "DataDog/ddtest", "GITHUB_REF": "refs/heads/main",
				"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_ACTOR": "original",
				"GITHUB_TRIGGERING_ACTOR": "rerunner", "VERSION": "v1.8.0",
				"GITHUB_STEP_SUMMARY": filepath.Join(dir, "summary"), "MOCK_LOG": filepath.Join(dir, "calls"),
				"MOCK_ROLE": "maintain", "MOCK_RERUN_ROLE": "maintain", "MOCK_REFS": "",
				"MOCK_PERMISSION_FAILURE": "0", "MOCK_TAG_FAILURE": "0", "MOCK_CREATE_FAILURE": "0",
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
				for _, want := range []string{
					"git/refs -f ref=refs/tags/v1.8.0 -f sha=checked-out-main-sha",
					"release create v1.8.0 --verify-tag --target checked-out-main-sha --title v1.8.0 --draft --generate-notes dist/*",
				} {
					if !strings.Contains(string(calls), want) {
						t.Errorf("missing %q in calls: %s", want, calls)
					}
				}
			} else if strings.Contains(string(calls), "release create") {
				t.Fatalf("unexpected release creation: %s", calls)
			}
		})
	}
}
