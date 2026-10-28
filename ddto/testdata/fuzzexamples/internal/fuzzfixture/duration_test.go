package fuzzfixture

import (
	"fmt"
	"testing"
)

func TestPrintedDuration(t *testing.T) {
	for _, value := range []string{"0.00", "-0.00", "0.07", "12.34"} {
		t.Run(value, func(t *testing.T) {
			output := fmt.Sprintf("--- PASS: FuzzDuration/seed#0 (%ss)\n", value)
			want := value
			if value == "-0.00" {
				want = "0.00"
			}
			if got := printedDuration(t, output, "FuzzDuration/seed#0"); got != want {
				t.Fatalf("duration = %q, want %q", got, want)
			}
		})
	}
}

func TestPrintedDurationRejectsInvalidOutput(t *testing.T) {
	for _, output := range []string{
		"--- PASS: FuzzDuration/seed#0 (-0.01s)",
		"--- PASS: FuzzDuration/seed#0 (-1.23s)",
		"--- PASS: FuzzDuration/seed#0 (nans)",
		"--- PASS: FuzzDuration/seed#1 (0.00s)",
		"--- FAIL: FuzzDuration/seed#0 (0.00s)",
	} {
		if value, ok := findPrintedDuration(output, "FuzzDuration/seed#0"); ok {
			t.Fatalf("accepted %q as duration %q", output, value)
		}
	}
}
