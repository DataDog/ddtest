// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import "testing"

func TestProcessRetrySubtreeRunPatternPreservesSelection(t *testing.T) {
	for _, tc := range []struct{ name, original, root, want string }{
		{"all", "", "TestRoot/managed", "^TestRoot$/^managed$"},
		{"short", "Root", "TestRoot/managed", "^TestRoot$/^managed$"},
		{"descendant", "^TestRoot$/^managed$/^Selected$", "TestRoot/managed", "^TestRoot$/^managed$/^Selected$"},
		{"partial-prefix", "Root/man/Selected", "TestRoot/managed", "^TestRoot$/^managed$/Selected"},
		{"alternatives", "TestOther/man/Other|Root/man/Selected|Root/man/Second", "TestRoot/managed", "^TestRoot$/^managed$/Selected|^TestRoot$/^managed$/Second"},
		{"short-alternative", "Root/man/Selected|Root", "TestRoot/managed", "^TestRoot$/^managed$"},
		{"grouped", "^(TestRoot|TestOther)$/^(managed|other)$/^(Selected|Second)$", "TestRoot/managed", "^TestRoot$/^managed$/^(Selected|Second)$"},
		{"class-delimiters", "Root/[m/|]+/Selected", "TestRoot/m", "^TestRoot$/^m$/Selected"},
		{"grouped-slash", "Root/man/(Selected/row|Second)", "TestRoot/managed", "^TestRoot$/^managed$/(Selected/row|Second)"},
		{"empty-segment", "Root//Selected", "TestRoot/managed", "^TestRoot$/^managed$/Selected"},
		{"native-name-rewrite", "Root/managed row/Selected", "TestRoot/managed_row", "^TestRoot$/^managed_row$/Selected"},
		{"escaped-pipe", `Root/man\|aged/Selected`, "TestRoot/man|aged", `^TestRoot$/^man\|aged$/Selected`},
		{"literal-root", ".*/.*/Selected", "TestRoot/a.b[0]", `^TestRoot$/^a\.b\[0\]$/Selected`},
		{"unselected-root", "^TestOther$/managed/Selected", "TestRoot/managed", "^$"},
		{"invalid", "[", "TestRoot/managed", "^$"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := processRetrySubtreeRunPattern(tc.original, tc.root); got != tc.want {
				t.Fatalf("selector=%q want%q", got, tc.want)
			}
		})
	}
}

func TestProcessRetrySubtreeRunPatternKeepsDescendantAdmission(t *testing.T) {
	for _, original := range []string{"", "Root", "Root//Selected", "Root/man/Selected|Root/man/Second", "Root/man/(Selected|Second)$", "Other/man/Rejected|Root/man/Selected/row", "Root/man/Selected|Root", "Root/man/Selected/row|Root/man/Second/other"} {
		t.Run(original, func(t *testing.T) {
			native := newTestingSelectionFromPatterns(original, "", "")
			narrowed := newTestingSelectionFromPatterns(processRetrySubtreeRunPattern(original, "TestRoot/managed"), "", "")
			for _, name := range []string{"TestRoot/managed", "TestRoot/managed/Selected", "TestRoot/managed/Selected/row", "TestRoot/managed/Selected/other", "TestRoot/managed/Second", "TestRoot/managed/Second/row", "TestRoot/managed/Second/other", "TestRoot/managed/Rejected"} {
				if got, want := narrowed.selects(name), native.selects(name); got != want {
					t.Fatalf("descendant %q admitted=%t native=%t", name, got, want)
				}
			}
			for _, name := range []string{"TestOther", "TestOther/managed/Selected", "TestRoot/other", "TestRoot/other/Selected"} {
				if narrowed.selects(name) {
					t.Fatalf("narrowed worker admitted unrelated %q", name)
				}
			}
		})
	}
}
