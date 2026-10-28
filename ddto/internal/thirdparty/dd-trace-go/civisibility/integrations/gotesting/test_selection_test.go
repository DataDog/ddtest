// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const selectionFixture = `package selection

import (
	"fmt"
	"testing"
)

func TestAlpha(t *testing.T)     { t.Run("sub", func(t *testing.T) {}) }
func TestAlphaBeta(t *testing.T) {}
func TestBeta(t *testing.T)      { t.Run("x", func(t *testing.T) {}) }
func TestGamma_1(t *testing.T)   {}
func TestDelta(t *testing.T)     {}

func ExampleAlpha() {
	fmt.Println("alpha")
	// Output: alpha
}

func ExampleGamma() {
	fmt.Println("gamma")
	// Output: gamma
}

func FuzzAlpha(f *testing.F) {
	f.Add(1)
	f.Fuzz(func(t *testing.T, n int) {})
}
`

var selectionFixtureNames = []string{"TestAlpha", "TestAlphaBeta", "TestBeta", "TestGamma_1", "TestDelta", "ExampleAlpha", "ExampleGamma", "FuzzAlpha"}

// The selection agrees with the top-level tests, examples and fuzz seeds that
// a real test binary runs for each -test.run and -test.skip pair: unbracketed
// slashes and bars split elements, a later element applies only to subtests,
// a skip alternative wins only when it matches completely, and patterns are
// rewritten as testing does.
func TestTestingSelectionMatchesTestingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a test binary")
	}
	dir := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":            "module example.com/selection\n\ngo 1.25\n",
		"selection_test.go": selectionFixture,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(dir, "selection.test")
	build := exec.Command("go", "test", "-c", "-vet=off", "-o", binary, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	for _, tc := range []struct{ run, skip string }{
		{"", ""},
		{"Alpha", ""},
		{"^TestAlpha$", ""},
		{"Alpha/sub", ""},
		{"Alpha/nothing", ""},
		{"Alpha|Beta", ""},
		{"Alpha/x|Beta", ""},
		{"Test[/]Alpha", ""},
		{"(Alpha/x)", ""},
		{"Beta(/x)?", ""},
		{`Alpha\/sub`, ""},
		{"/sub", ""},
		{"Gamma 1", ""},
		{"Gamma_1", ""},
		{"^Example", ""},
		{"Fuzz", ""},
		{"", "Alpha"},
		{"", "Alpha/sub"},
		{"", "Alpha/x|Alpha"},
		{"", "Alpha|Alpha/x"},
		{"", "Beta$|Delta"},
		{"", "Gamma 1"},
		{"", "[/]"},
		{"Alpha", "Beta"},
		{"Alpha|Delta", "AlphaBeta/sub"},
		{"Alpha|Delta", "^TestAlpha$"},
		{"^$", ""},
	} {
		args := []string{"-test.v", "-test.run=" + tc.run, "-test.skip=" + tc.skip}
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		out, _ := cmd.CombinedOutput()
		var ran []string
		for _, line := range strings.Split(string(out), "\n") {
			if name, ok := strings.CutPrefix(line, "=== RUN   "); ok && !strings.Contains(name, "/") {
				ran = append(ran, name)
			}
		}
		selection := newTestingSelectionFromPatterns(tc.run, tc.skip, "")
		if selection == nil {
			t.Fatalf("run=%q skip=%q: patterns rejected", tc.run, tc.skip)
		}
		var selected []string
		for _, name := range selectionFixtureNames {
			if strings.HasPrefix(name, "Fuzz") && selection.selectsFuzzTarget(name) || !strings.HasPrefix(name, "Fuzz") && selection.selects(name) {
				selected = append(selected, name)
			}
		}
		slices.Sort(ran)
		slices.Sort(selected)
		if !slices.Equal(ran, selected) {
			t.Errorf("run=%q skip=%q: testing ran %v, selection %v\n%s", tc.run, tc.skip, ran, selected, out)
		}
	}
}

func TestTestingSelectionEdges(t *testing.T) {
	var all *testingSelection
	if !all.selects("TestAnything") || !all.selectsFuzzTarget("FuzzAnything") {
		t.Fatal("a nil selection must select everything")
	}
	if newTestingSelectionFromPatterns("(", "", "") != nil || newTestingSelectionFromPatterns("", "a/[", "") != nil {
		t.Fatal("an invalid pattern must select everything")
	}
	fuzzing := newTestingSelectionFromPatterns("^TestOnly$", "", "FuzzOther")
	if fuzzing.selects("FuzzAlpha") || !fuzzing.selectsFuzzTarget("FuzzAlpha") {
		t.Fatal("-test.fuzz must keep counting fuzz targets")
	}
	for _, tc := range []struct {
		count, cpu string
		once       bool
	}{
		{"1", "", true},
		{"0x1", " 4 ", true},
		{"1", "1,", true},
		{"2", "", false},
		{"0", "", false},
		{"1", "1,2", false},
		{"x", "", false},
	} {
		if got := testingRunsOnce(tc.count, tc.cpu); got != tc.once {
			t.Fatalf("count=%q cpu=%q: runs once %t", tc.count, tc.cpu, got)
		}
	}
	if testingMUsesStandardDeps(nil) || testingMUsesStandardDeps(&testing.M{}) {
		t.Fatal("an M without testdeps must select everything")
	}
	var claim *testingMInstrumentationClaim
	if claim.selectionOrAll() != nil {
		t.Fatal("a nil claim must select everything")
	}
}

func newTestingLikeFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(new(strings.Builder))
	fs.Bool("test.v", false, "")
	fs.Bool("test.short", false, "")
	fs.String("test.run", "", "")
	fs.String("test.skip", "", "")
	fs.String("test.fuzz", "", "")
	fs.Uint("test.count", 1, "")
	fs.String("test.cpu", "", "")
	fs.Duration("test.timeout", 0, "")
	return fs
}

// Before M.Run parses flags, the copies read the values flag.Parse will set,
// without changing any flag.
func TestTestingFlagValuesMatchFlagParse(t *testing.T) {
	names := []string{"test.run", "test.skip", "test.fuzz", "test.count", "test.cpu"}
	for _, args := range [][]string{
		nil,
		{"-test.run=Alpha"},
		{"-test.run", "Alpha", "-test.skip", "Beta"},
		{"--test.run=Alpha", "--test.skip", "Beta"},
		{"-test.v", "-test.run", "Alpha"},
		{"-test.v=true", "-test.short", "-test.run=A", "-test.run=B"},
		{"-test.count", "3", "-test.timeout=1s", "-test.fuzz=FuzzX", "-test.cpu=1,2"},
		{"-test.run=Alpha", "positional", "-test.skip=Beta"},
		{"-test.run=Alpha", "--", "-test.skip=Beta"},
		{"-test.unknown=1", "-test.run=Alpha"},
		{"-test.run"},
		{"-h"},
	} {
		source := newTestingLikeFlagSet()
		got, ok := testingFlagValues(source, args, false, names...)
		source.VisitAll(func(f *flag.Flag) {
			if f.Value.String() != f.DefValue {
				t.Fatalf("%q changed flag %s", args, f.Name)
			}
		})

		oracle := newTestingLikeFlagSet()
		err := oracle.Parse(args)
		if (err == nil) != ok {
			t.Fatalf("%q: ok=%t, flag.Parse error %v", args, ok, err)
		}
		if err != nil {
			continue
		}
		for _, name := range names {
			if want := oracle.Lookup(name).Value.String(); got[name] != want {
				t.Fatalf("%q: %s=%q, want %q", args, name, got[name], want)
			}
		}
	}

	parsed := newTestingLikeFlagSet()
	if err := parsed.Parse([]string{"-test.run=Parsed"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := testingFlagValues(parsed, []string{"-test.run=Ignored"}, true, names...); !ok || got["test.run"] != "Parsed" {
		t.Fatalf("parsed flags: %v %t", got, ok)
	}
	if _, ok := testingFlagValues(flag.NewFlagSet("empty", flag.ContinueOnError), nil, false, names...); ok {
		t.Fatal("missing testing flags must select everything")
	}
}
