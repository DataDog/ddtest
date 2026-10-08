package integrations

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

func TestCloseActionsKeepLIFOAndRunBarriersFirst(t *testing.T) {
	recorder := &log.RecordLogger{}
	t.Cleanup(log.UseLogger(recorder))
	level := log.GetLevel()
	t.Cleanup(func() { log.SetLevel(level) })
	log.SetLevel(log.LevelDebug)
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	var order []string
	for _, name := range []string{"first", "second"} {
		name := name // Close callbacks run after the registration loop.
		PushCiVisibilityCloseAction(func() { order = append(order, name) })
		if !TryPushCiVisibilityPreCloseAction(func() {
			order = append(order, "barrier-"+name)
			PushCiVisibilityCloseAction(func() { order = append(order, "late-"+name) })
		}) {
			t.Fatal("barrier registration rejected")
		}
	}
	ExitCiVisibility()
	ExitCiVisibility()
	lines := strings.Join(recorder.Logs(), "\n")
	for _, want := range []string{"shutdown barriers finished duration=", "close actions finished duration=", "logger stop finished duration=", "telemetry stop finished duration=", "shutdown finished duration="} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q: %s", want, lines)
		}
	}
	if strings.Count(lines, "owner=true") != 1 || strings.Count(lines, "owner=false") != 1 {
		t.Errorf("duplicate shutdown executed: %s", lines)
	}
	want := []string{"barrier-second", "barrier-first", "late-first", "late-second", "second", "first"}
	if !slices.Equal(order, want) {
		t.Fatalf("close order %v, want %v", order, want)
	}
}

func TestConcurrentCloseActionsRunOnce(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	seen := make([]int, 128)
	var registrations sync.WaitGroup
	for i := range seen {
		i := i // Both the worker and its queued close callback retain this index.
		registrations.Go(func() { PushCiVisibilityCloseAction(func() { seen[i]++ }) })
	}
	registrations.Wait()
	var exits sync.WaitGroup
	for i := 0; i < 4; i++ {
		exits.Go(ExitCiVisibility)
	}
	exits.Wait()
	for i, count := range seen {
		if count != 1 {
			t.Fatalf("action %d executed %d times", i, count)
		}
	}
}

func BenchmarkCloseActionRegistration(b *testing.B) {
	saved := closeActions
	b.Cleanup(func() { closeActions = saved })
	for _, size := range []int{1000, 4000, 16000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				closeActions = nil
				for i := 0; i < size; i++ {
					PushCiVisibilityCloseAction(func() {})
				}
			}
		})
	}
}
