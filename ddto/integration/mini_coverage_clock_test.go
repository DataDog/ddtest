package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Coverage runs after the preceding test while the next test changes the
// process-wide local zone. The application coverage is identical in the safe
// SDK reference; only the Mini regression enables the concurrent mutation.
const coverageClockTests = `package fixture_test

import (
	"os"
	"runtime"
	"testing"
	"time"

	fixture "example.com/dd-ci-testing-fixture"
)

func TestClockPredecessor(t *testing.T) {
	for range 100 {
		if fixture.Add(1, 2) != 3 {
			t.Fatal("addition")
		}
	}
	changeGlobalZone()
}

func TestClockLocal(t *testing.T) {
	if fixture.Add(2, 3) != 5 {
		t.Fatal("addition")
	}
	changeGlobalZone()
}

func changeGlobalZone() {
	if os.Getenv("DDTO_CHANGE_LOCAL") != "true" {
		return
	}
	previous := time.Local
	defer func() { time.Local = previous }()
	zones := []*time.Location{time.FixedZone("A", 3600), time.FixedZone("B", -3600)}
	for i := range 100000 {
		time.Local = zones[i%len(zones)]
		if i%16 == 0 {
			runtime.Gosched()
		}
	}
}
`

func TestMiniCoverageWithGlobalTimeChanges(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(coverageClockTests), 0600); err != nil {
		t.Fatal(err)
	}
	bins := compileMiniPair(t, dir, driver, "-race", "-covermode=atomic", "-coverpkg=./...")
	args := []string{"-test.run=^TestClock", "-test.count=3"}
	want, sdk := runParityCase(t, dir, bins[0], parityCase{Args: args, Policy: policySettings{Coverage: true}, Coverage: true, Env: []string{"DD_CIVISIBILITY_AGENTLESS_ENABLED=false"}})
	if sdk.code != 0 {
		t.Fatalf("safe SDK coverage reference: %s", sdk.stderr)
	}
	expected := normalizedMiniCoverage(t, &want.miniWireCapture)
	if len(expected) == 0 {
		t.Fatal("SDK reference did not upload coverage")
	}
	for _, cpus := range []int{4, 32} {
		for _, deferred := range []bool{false, true} {
			for _, telemetry := range []bool{false, true} {
				t.Run(fmt.Sprintf("cpus=%d/deferred=%t/telemetry=%t", cpus, deferred, telemetry), func(t *testing.T) {
					got, mini := runParityCase(t, dir, bins[1], parityCase{
						Args: args, Policy: policySettings{Coverage: true}, Coverage: true,
						Env: []string{"DDTO_CHANGE_LOCAL=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", fmt.Sprintf("GOMAXPROCS=%d", cpus), fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred), fmt.Sprintf("DD_INSTRUMENTATION_TELEMETRY_ENABLED=%t", telemetry)},
					})
					if mini.code != 0 {
						t.Fatalf("Mini coverage: exit %d\n%s\n%s", mini.code, mini.out, mini.stderr)
					}
					assertMiniCIAttributes(t, want.events, got.events)
					actual := normalizedMiniCoverage(t, &got.miniWireCapture)
					if !reflect.DeepEqual(expected, actual) {
						t.Fatalf("coverage attribution changed: SDK %v Mini %v", expected, actual)
					}
				})
			}
		}
	}
}
