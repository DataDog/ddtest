package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The process smoke uses real Go timeout machinery. Deadline admission itself
// is tested with fixed times in gotesting, without sleeping in unit tests.
func TestMiniBazelRetryDeadline(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source, err := os.ReadFile(filepath.Join("..", "testdata", "bazel", "ws", "efd", "deadline_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	// The shared smoke runs as an external test of the integration fixture.
	source = bytes.Replace(source, []byte("package efd"), []byte("package fixture_test"), 1)
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	bin := compileMini(t, dir, driver)
	cache := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cache, "cache", "http"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"manifest.txt":                "1\n",
		"cache/http/settings.json":    `{"data":{"attributes":{"known_tests_enabled":true,"early_flake_detection":{"enabled":true,"faulty_session_threshold":100,"slow_test_retries":{"5s":10}},"flaky_test_retries_enabled":false,"test_management":{"enabled":false}}}}`,
		"cache/http/known_tests.json": `{"data":{"attributes":{"tests":{"example.com/known":{"known_test.go":["TestKnown"]}}}}}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(cache, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, mode string
		failfast   bool
	}{
		{name: "in_process", mode: "in_process"},
		{name: "process", mode: "process"},
		{name: "failfast", mode: "in_process", failfast: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := t.TempDir()
			env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true",
				"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true",
				"DD_TEST_OPTIMIZATION_MANIFEST_FILE="+filepath.Join(cache, "manifest.txt"),
				"TEST_UNDECLARED_OUTPUTS_DIR="+output, "TEST_TIMEOUT=5",
				"DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+tc.mode)
			args := []string{"-test.run=^TestBudget$", "-test.timeout=5s"}
			wantCode := 0
			if tc.failfast {
				env = append(env, "DEADLINE_FAIL_SECOND=true")
				args = append(args, "-test.failfast")
				wantCode = 1
			}
			stdout, stderr, code := command(t, dir, env, bin, args...)
			if code != wantCode {
				t.Fatalf("EFD deadline exit %d, want %d:\n%s\n%s", code, wantCode, stdout, stderr)
			}
			capture := readBazelEvents(t, output)
			var tests, failed, sessions, retries, final int
			spans := make(map[json.Number]bool)
			var sessionID any
			var parents []any
			for _, event := range capture.events {
				switch event["type"] {
				case "test":
					tests++
					content := event["content"].(map[string]any)
					meta := content["meta"].(map[string]any)
					span, ok := content["span_id"].(json.Number)
					if !ok || span == "0" || spans[span] {
						t.Fatalf("missing or duplicate sample identity: %v", content["span_id"])
					}
					spans[span] = true
					parents = append(parents, content["test_session_id"])
					if meta["test.name"] != "TestBudget" || meta["test.is_new"] != "true" {
						t.Fatalf("lost sample metadata: %v", meta)
					}
					if meta["test.is_retry"] == "true" {
						retries++
						if meta["test.retry_reason"] != "early_flake_detection" {
							t.Fatalf("wrong retry family: %v", meta)
						}
					}
					if status, present := meta["test.final_status"]; present {
						final++
						if status != "pass" { // EFD retains its first passing sample.
							t.Fatalf("lost the EFD aggregate result: %v", meta)
						}
					}
					if tc.failfast && meta["test.status"] == "fail" {
						failed++
					} else if meta["test.status"] != "pass" {
						t.Fatalf("retry failed: %v\n%s\n%s", meta, stdout, stderr)
					}
				case "test_session_end":
					sessions++
					sessionID = event["content"].(map[string]any)["test_session_id"]
				}
			}
			// Five seconds cannot fit the initial two-second attempt, another
			// child and the existing process teardown reserve. It must retain
			// the initial success without emitting an artificial child timeout.
			if sessions != 1 || tc.mode == "process" && tests != 1 || tc.mode == "in_process" && tests != 2 || tc.failfast && failed != 1 {
				t.Fatalf("unexpected deadline accounting: %d tests and %d sessions\n%s\n%s", tests, sessions, stdout, stderr)
			}
			if sessionID == nil || final != 1 || retries != tests-1 {
				t.Fatalf("unexpected final metadata: session=%v final=%d retries=%d", sessionID, final, retries)
			}
			for _, parent := range parents {
				if parent != sessionID {
					t.Fatalf("sample belongs to session %v, want %v", parent, sessionID)
				}
			}
		})
	}
}
