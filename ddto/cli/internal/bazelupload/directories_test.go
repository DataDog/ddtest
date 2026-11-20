package bazelupload

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Sources share their open directories, and a directory reached through a
// link elsewhere in the root still works, while one that leaves the root
// does not.
func TestSourcesInNestedDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
	directory, outside := t.TempDir(), t.TempDir()
	for _, dir := range []string{"a/tests", "b/tests", "d"} {
		if err := os.MkdirAll(filepath.Join(directory, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range [][2]string{{"../b/tests", "d/link"}, {outside, "escape"}} {
		if err := os.Symlink(link[0], filepath.Join(directory, link[1])); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, directory, "CODEOWNERS", "* @team\n")
	writeFixture(t, outside, "five.json", inputPayload)
	var plan []Source
	for id, file := range map[string]string{"one": "a/tests/one.json", "two": "a/tests/two.json", "three": "b/tests/three.json", "four": "d/link/four.json"} {
		writeFixture(t, directory, file, inputPayload)
		plan = append(plan, testSource(id, file))
	}
	plan = append(plan, testSource("five", "escape/five.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer server.Close()

	for _, batch := range []bool{false, true} {
		report := runFixture(t, directory, testPlan(plan...), Options{Batch: batch, AgentlessURL: server.URL, APIKey: "local-test"})
		for _, result := range report.Sources {
			switch {
			case result.ID == "five":
				if result.Status != statusFailed || result.Failure != "test_payload_read_failed" {
					t.Errorf("batch=%v: a source outside the root: %+v", batch, result)
				}
			case result.Status != statusSucceeded || !result.Deleted:
				t.Errorf("batch=%v: %s: %+v", batch, result.ID, result)
			}
		}
		if _, err := os.Stat(filepath.Join(outside, "five.json")); err != nil {
			t.Fatal("the file outside the root changed:", err)
		}
		for _, file := range []string{"a/tests/one.json", "a/tests/two.json", "b/tests/three.json", "d/link/four.json"} {
			writeFixture(t, directory, file, inputPayload)
		}
	}
}
