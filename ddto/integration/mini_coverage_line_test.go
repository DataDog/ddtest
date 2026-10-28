package integration

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Without -coverprofile, the SDK writes the session's coverage profile through
// testing's tear-down, which also prints the coverage line. ddto prints it as
// often as go test: once after the tests, and not after -list, even with
// -coverprofile.
func TestMiniPrintsCoverageLineOnce(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	receiver := &capture{}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
	// The fixture's own package parses the flags in TestMain, before ddto sets
	// a profile; ./other, without TestMain, parses them in M.Run, after.
	profile := "-coverprofile=" + filepath.Join(t.TempDir(), "coverage.out")
	for _, args := range [][]string{
		{"-v", "-cover", "-run=^TestPass$", "."},
		{"-json", "-cover", "-run=^TestPass$", "."},
		{"-v", "-cover", "-list=^TestPass$", "."},
		{"-v", "-cover", "-list=^TestPass$", profile, "."},
		{"-v", "-cover", "-run=^TestOther$", "./other"},
		{"-json", "-cover", "-run=^TestOther$", "./other"},
		{"-v", "-cover", "-list=^TestOther$", "./other"},
	} {
		args = append([]string{"-count=1"}, args...)
		native, nativeErr, nativeCode := command(t, dir, testEnv(), "go", append([]string{"test"}, args...)...)
		mini, miniErr, miniCode := command(t, dir, env, driver, append([]string{"test", "--runtime=mini"}, args...)...)
		want, got := strings.Count(native, "coverage: "), strings.Count(mini, "coverage: ")
		if nativeCode != 0 || miniCode != 0 || got != want {
			t.Fatalf("%v: ddto printed the coverage line %d times, go test %d\nddto (exit %d):\n%s%s\ngo test (exit %d):\n%s%s", args, got, want, miniCode, mini, miniErr, nativeCode, native, nativeErr)
		}
	}
}
