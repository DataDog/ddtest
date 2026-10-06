package integrations

import (
	"fmt"
	"slices"
	"sync"
	"testing"
)

func TestCloseActionsKeepLIFOAndRunBarriersFirst(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	var order []string
	for _, name := range []string{"first", "second"} {
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
		registrations.Go(func() { PushCiVisibilityCloseAction(func() { seen[i]++ }) })
	}
	registrations.Wait()
	var exits sync.WaitGroup
	for range 4 {
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
			for range b.N {
				closeActions = nil
				for range size {
					PushCiVisibilityCloseAction(func() {})
				}
			}
		})
	}
}
