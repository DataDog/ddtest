package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// With -race, Mini's functions are compiled without race instrumentation and
// keep their source positions; the client's code is instrumented as before,
// so its races are still reported.
func TestMiniRaceInstrumentsClientOnly(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test

import "testing"

func TestClientDataRace(t *testing.T) {
	shared := 0
	done := make(chan struct{})
	go func() {
		shared++
		close(done)
	}()
	shared++
	<-done
}
`
	if err := os.WriteFile(filepath.Join(dir, "race_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	// The first execution passes; its retries race.
	retry := `package fixture_test

import (
	"os"
	"testing"
)

func TestRaceOnRetry(t *testing.T) {
	marker := os.Getenv("POC_RETRY_COUNTER") + ".race"
	if _, err := os.Stat(marker); err != nil {
		_ = os.WriteFile(marker, nil, 0o600)
		return
	}
	shared := 0
	done := make(chan struct{})
	go func() {
		shared++
		close(done)
	}()
	shared++
	<-done
}

// As TestRaceOnRetry, in a subtest, which testing reports the race for.
func TestRaceInSubtestOnRetry(t *testing.T) {
	marker := os.Getenv("POC_RETRY_COUNTER") + ".subtest-race"
	if _, err := os.Stat(marker); err != nil {
		_ = os.WriteFile(marker, nil, 0o600)
		return
	}
	t.Run("racy", func(t *testing.T) {
		shared := 0
		done := make(chan struct{})
		go func() {
			shared++
			close(done)
		}()
		shared++
		<-done
	})
}
`
	if err := os.WriteFile(filepath.Join(dir, "race_retry_test.go"), []byte(retry), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("race.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-race", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile: %d %s %s", code, out, stderr)
	}
	got := execute(t, dir, bin, []string{"-test.run=^TestClientDataRace$"}, true, false)
	if got.code == 0 || !strings.Contains(got.out+got.stderr, "WARNING: DATA RACE") || !strings.Contains(got.out, "race detected during execution of test") {
		t.Fatalf("client race not reported: %d %s %s", got.code, got.out, got.stderr)
	}
	// A race that an Early Flake Detection retry in a child process detects
	// fails the package although the first execution passed.
	for _, name := range []string{"TestRaceOnRetry", "TestRaceInSubtestOnRetry"} {
		if got := executeProfile(t, dir, bin, []string{"-test.run=^" + name + "$"}, true, false, "efd"); got.code == 0 {
			t.Fatalf("%s: a race in a retry child passed: %s %s", name, got.out, got.stderr)
		}
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	span, err := os.ReadFile(filepath.Join(root, "internal", "minitracer", "span.go"))
	if err != nil {
		t.Fatal(err)
	}
	declaration := strings.Index(string(span), "\nfunc (s *Span) SetTag(")
	if declaration < 0 {
		t.Fatal("Span.SetTag not found")
	}
	line := strings.Count(string(span[:declaration+1]), "\n") + 1
	for _, c := range []struct {
		symbol, position string
		instrumented     bool
	}{
		{`^github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer.\(\*Span\).SetTag$`, "span.go:" + strconv.Itoa(line), false},
		{`^example.com/dd-ci-testing-fixture_test.TestClientDataRace$`, "race_test.go:5", true},
	} {
		disassembly, stderr, code := command(t, dir, testEnv(), "go", "tool", "objdump", "-s", c.symbol, bin)
		if code != 0 || !strings.Contains(disassembly, "TEXT ") {
			t.Fatalf("%s: %d %s %s", c.symbol, code, disassembly, stderr)
		}
		if first := regexp.MustCompile(`(?m)^\s+(\S+\.go:\d+)\s`).FindStringSubmatch(disassembly); first == nil || first[1] != c.position {
			t.Errorf("%s starts at %v, want %s", c.symbol, first, c.position)
		}
		if instrumented := strings.Contains(disassembly, "runtime.racefuncenter"); instrumented != c.instrumented {
			t.Errorf("%s: race instrumentation %t, want %t", c.symbol, instrumented, c.instrumented)
		}
	}
}

// A client that calls Mini's API keeps Mini instrumented: a race on memory it
// passes to Mini, here read by Span.SetTag, is reported as under go test. The
// test may import Mini itself or through a helper package that only tests
// import, which the package query does not list.
func TestMiniRaceKeepsMiniInstrumentedForAPIClients(t *testing.T) {
	const helper = `package racehelper

import (
	"context"

	"github.com/tonyredondo/dd-ci-testing-poc/testopt"
)

// Tag hands value to a Mini span, which reads it.
func Tag(value *int) error {
	client, err := testopt.New(testopt.Config{Transport: testopt.TransportConfig{Endpoint: "http://127.0.0.1:9"}})
	if err != nil {
		return err
	}
	span, _ := client.StartSpan(context.Background(), "race")
	span.SetTag("shared", value)
	return nil
}
`
	const test = `package fixture_test

import (
	"testing"

	"example.com/dd-ci-testing-fixture/racehelper"
)

func TestRaceThroughMiniAPI(t *testing.T) {
	shared := 0
	done := make(chan struct{})
	go func() {
		shared++
		close(done)
	}()
	if err := racehelper.Tag(&shared); err != nil {
		t.Fatal(err)
	}
	<-done
}
`
	direct := strings.Replace(strings.Replace(test, `"example.com/dd-ci-testing-fixture/racehelper"`, `"context"

	"github.com/tonyredondo/dd-ci-testing-poc/testopt"`, 1), `	if err := racehelper.Tag(&shared); err != nil {
		t.Fatal(err)
	}
`, `	client, err := testopt.New(testopt.Config{Transport: testopt.TransportConfig{Endpoint: "http://127.0.0.1:9"}})
	if err != nil {
		t.Fatal(err)
	}
	span, _ := client.StartSpan(context.Background(), "race")
	span.SetTag("shared", &shared)
`, 1)
	for name, files := range map[string]map[string]string{
		"direct": {"api_race_test.go": direct},
		"helper": {"api_race_test.go": test, filepath.Join("racehelper", "helper.go"): helper},
	} {
		t.Run(name, func(t *testing.T) {
			dir, driver := prepareMiniFixture(t)
			for path, content := range files {
				path = filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(t.TempDir(), executableName("api-race.test"))
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-race", "-c", "-o", bin, ".")
			if code != 0 {
				t.Fatalf("compile: %d %s %s", code, out, stderr)
			}
			got := execute(t, dir, bin, []string{"-test.run=^TestRaceThroughMiniAPI$"}, false, false)
			report := got.out + got.stderr
			if got.code == 0 || !strings.Contains(report, "WARNING: DATA RACE") || !strings.Contains(report, "internal/minitracer") {
				t.Fatalf("race through Mini's API not reported: %d %s", got.code, report)
			}
		})
	}
}
