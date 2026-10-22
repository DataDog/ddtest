package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A callback that a table of b.Run calls runs as several sub-benchmarks gets
// an event for each. The pinned SDK marked the callback as instrumented after
// its first run, so the next sub-benchmarks had none.
func TestMiniSubBenchmarksSharingACallback(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "zz_table_benchmark_test.go"), []byte(`package fixture_test

import "testing"

func work(b *testing.B) {
	for range b.N {
	}
}

func BenchmarkTable(b *testing.B) {
	b.Run("a", work)
	b.Run("b", work)
	b.Run("b", work)
	b.Run("input size=1", work)
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := execute(t, dir, compileMini(t, dir, driver), []string{"-test.run=^$", "-test.bench=^BenchmarkTable$", "-test.benchtime=1x"}, true, false)
	var tests []string
	for _, event := range got.wireEvents {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if event["type"] == "test" {
			tests = append(tests, fmt.Sprint(meta["test.name"]))
		}
	}
	slices.Sort(tests)
	// The names that testing gives them: a repeated name gets #01, a space _.
	if want := []string{"BenchmarkTable", "BenchmarkTable/a", "BenchmarkTable/b", "BenchmarkTable/b#01", "BenchmarkTable/input_size=1"}; got.code != 0 || !slices.Equal(tests, want) {
		t.Fatalf("exit %d, tests %v; want %v\n%s", got.code, tests, want, got.stderr)
	}

	// Without -bench on the command line, the benchmarks that a TestMain
	// selects through flag.Set get their events too.
	if err := os.Mkdir(filepath.Join(dir, "benchmain"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, dir, map[string]string{"benchmain/main_test.go": `package benchmain

import (
	"flag"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	flag.Parse()
	flag.Set("test.bench", "^BenchmarkWork$")
	flag.Set("test.benchtime", "1x")
	os.Exit(m.Run())
}

func BenchmarkWork(b *testing.B) {
	for range b.N {
	}
}
`})
	receiver := &capture{}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
	out, stderr, code := command(t, dir, env, driver, "test", "-count=1", "-run=^$", "./benchmain")
	if events := summarizeSessionEvents(receiver); code != 0 || !slices.Equal(events.tests, []string{"BenchmarkWork pass"}) {
		t.Fatalf("exit %d, tests %v\n%s\n%s", code, events.tests, out, stderr)
	}
}
