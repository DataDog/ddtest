package gotesting

import (
	"testing"
)

func TestNativeContextWithoutSDK(t *testing.T) {
	ctx := t.Context()
	if allocs := testing.AllocsPerRun(100, func() { bindNativeTestContext(t, nil) }); allocs != 0 {
		t.Fatalf("SDK-free binding allocated %g objects", allocs)
	}
	if t.Context() != ctx {
		t.Fatal("SDK-free binding replaced the native context")
	}
}

func BenchmarkNativeContextWithoutSDK(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		bindNativeTestContext(b, nil)
	}
}
