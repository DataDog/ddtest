package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMiniCheckoutIdentity(t *testing.T) {
	dir, driver, bin := gitStateFixture(t)
	head, stderr, code := command(t, dir, testEnv(), "git", "rev-parse", "HEAD")
	if code != 0 {
		t.Fatalf("HEAD: %s %s", head, stderr)
	}
	head = strings.TrimSpace(head)
	if out, errout, code := command(t, dir, testEnv(), "git", "checkout", "--detach", head); code != 0 {
		t.Fatalf("detach: %s %s", out, errout)
	}
	for _, tc := range []struct {
		name, workspace, providerSHA, branch, repository, custom string
		handoff                                                  bool
	}{
		{name: "matched detached mirror", workspace: dir, providerSHA: head, branch: "topic", repository: "https://github.com/example/workflow.git"},
		{name: "different revision", workspace: dir, providerSHA: strings.Repeat("f", 40), repository: "https://example.invalid/acme/fixture.git"},
		{name: "nested checkout", workspace: filepath.Dir(dir), providerSHA: head, repository: "https://example.invalid/acme/fixture.git", custom: "true"},
		{name: "nested worker handoff", workspace: filepath.Dir(dir), providerSHA: head, repository: "https://example.invalid/acme/fixture.git", custom: "true", handoff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := []string{"DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=", "DD_GIT_BRANCH=", "DD_GIT_TAG=",
				"GITHUB_ACTIONS=true", "GITHUB_SHA=" + tc.providerSHA, "GITHUB_REF=refs/pull/123/merge", "GITHUB_HEAD_REF=topic",
				"GITHUB_REPOSITORY=example/workflow", "GITHUB_SERVER_URL=https://github.com", "GITHUB_WORKSPACE=" + tc.workspace}
			if tc.handoff {
				state := runGitWorker(t, dir, driver, "", extra...)
				extra = append(extra, "DDTEST_INSTRUMENT_GIT_STATE="+state)
			}
			capture := runCIWireCase(t, dir, bin, []string{"-test.run=^TestPass$"}, extra, false, false)
			seen := map[string]bool{}
			for _, event := range capture.events {
				content, _ := event["content"].(map[string]any)
				meta, _ := content["meta"].(map[string]any)
				kind, _ := event["type"].(string)
				if kind != "test" && kind != "test_suite_end" && kind != "test_module_end" && kind != "test_session_end" {
					continue
				}
				seen[kind] = true
				for key, want := range map[string]string{"git.commit.sha": head, "git.repository_url": tc.repository, "git.branch": tc.branch,
					"ci.workspace_path": tc.workspace, "git.checkout.root": filepath.ToSlash(dir), "git.checkout.is_custom": tc.custom, "ci.git.merge_commit_sha": tc.providerSHA} {
					got, _ := meta[key].(string)
					if got != want {
						t.Errorf("%s %s=%q, want %q", kind, key, got, want)
					}
				}
			}
			for _, kind := range []string{"test", "test_suite_end", "test_module_end", "test_session_end"} {
				if !seen[kind] {
					t.Errorf("missing %s", kind)
				}
			}
		})
	}
}
