package integration

import "testing"

func TestParallelNormalizationPreservesOutput(t *testing.T) {
	a := "main.before\n=== CONT  TestParallel/0\n=== CONT  TestParallel/1\n--- PASS: TestParallel (0.01s)\n    --- PASS: TestParallel/0 (0.01s)\n    --- PASS: TestParallel/1 (0.01s)\nmain.after 0\n"
	b := "main.before\n=== CONT  TestParallel/1\n=== CONT  TestParallel/0\n--- PASS: TestParallel (0.02s)\n    --- PASS: TestParallel/1 (0.02s)\n    --- PASS: TestParallel/0 (0.02s)\nmain.after 0\n"
	if normalizedOutput(a) != normalizedOutput(b) {
		t.Fatal("legal Go scheduling differences retained")
	}
	for _, other := range []string{a + "extra log\n", a + "    --- PASS: TestParallel/0 (0.01s)\n", "main.after 0\n" + a} {
		if normalizedOutput(other) == normalizedOutput(a) {
			t.Fatal("lost observable output")
		}
	}
}
