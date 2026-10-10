// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// testingSelection reports which top-level tests, examples and fuzz targets
// M.Run will start, with testing's own -test.run and -test.skip rules. A suite
// or module closes when the count of its workloads that have not finished
// reaches zero, so a workload that never starts must not be counted. A nil
// selection selects everything, as the SDK assumed.
type testingSelection struct {
	filter, skip testingFilter
	patterns     map[string]*regexp.Regexp
	fuzzing      bool // -test.fuzz may fuzz a target that -test.run rejects
}

// newTestingSelection reads testing's flags for m. It returns nil, selecting
// everything, unless m matches names with the standard testdeps regexp
// matcher, every flag and pattern can be read, and tests run once.
func newTestingSelection(m *testing.M) *testingSelection {
	if !testingMUsesStandardDeps(m) {
		return nil
	}
	// testing.M.Run parses flags after this hook; read them the same way.
	values, ok := testingFlagValues(flag.CommandLine, os.Args[1:], flag.Parsed(), "test.run", "test.skip", "test.fuzz", "test.count", "test.cpu")
	if !ok || !testingRunsOnce(values["test.count"], values["test.cpu"]) {
		return nil
	}
	return newTestingSelectionFromPatterns(values["test.run"], values["test.skip"], values["test.fuzz"])
}

// testingRunsOnce reports whether M.Run starts each selected test once: with
// -test.count=N or a -test.cpu list it runs them in N or more rounds. Counting
// is per run, so repeated rounds keep counting every workload once, as the SDK
// does, and suites with filtered tests stay open until exit as before.
func testingRunsOnce(count, cpu string) bool {
	rounds, err := strconv.ParseUint(count, 0, strconv.IntSize) // flag.Uint's parsing
	if err != nil || rounds != 1 {
		return false
	}
	cpus := 0
	for value := range strings.SplitSeq(cpu, ",") {
		if strings.TrimSpace(value) != "" {
			cpus++
		}
	}
	return cpus <= 1
}

// newTestingSelectionFromPatterns mirrors testing's newMatcher. It returns nil
// for an invalid pattern, which testing reports before running anything.
func newTestingSelectionFromPatterns(run, skip, fuzz string) *testingSelection {
	selection := &testingSelection{patterns: map[string]*regexp.Regexp{}, fuzzing: fuzz != ""}
	if run == "" {
		selection.filter = testingSimpleMatch{} // always partial true
	} else {
		selection.filter = splitTestingRegexp(run)
	}
	if skip == "" {
		selection.skip = testingAlternationMatch{} // always false
	} else {
		selection.skip = splitTestingRegexp(skip)
	}
	compile := func(pattern string) error {
		if _, ok := selection.patterns[pattern]; ok {
			return nil
		}
		compiled, err := regexp.Compile(pattern)
		selection.patterns[pattern] = compiled
		return err
	}
	if selection.filter.verify(compile) != nil || selection.skip.verify(compile) != nil {
		return nil
	}
	return selection
}

// selectionOrAll returns the claim's selection; a nil claim, as in direct
// unit calls, selects everything.
func (c *testingMInstrumentationClaim) selectionOrAll() *testingSelection {
	if c == nil {
		return nil
	}
	return c.selection
}

// selects reports whether testing's matcher accepts a top-level name, as
// matcher.fullName does for tests and examples: the filter must match, and a
// skip pattern excludes the name only when it matches completely. A skip
// pattern with more elements applies to subtests only.
func (s *testingSelection) selects(name string) bool {
	if s == nil {
		return true
	}
	elem := strings.Split(name, "/")
	if ok, _ := s.filter.matches(elem, s.matchString); !ok {
		return false
	}
	skip, partialSkip := s.skip.matches(elem, s.matchString)
	return !skip || partialSkip
}

// selectsFuzzTarget reports whether M.Run may start a fuzz target: as a seed
// test selected by -test.run, or, conservatively, whenever -test.fuzz is set.
func (s *testingSelection) selectsFuzzTarget(name string) bool {
	return s == nil || s.fuzzing || s.selects(name)
}

// matchString is testdeps.TestDeps.MatchString with precompiled patterns.
func (s *testingSelection) matchString(pattern, str string) (bool, error) {
	return s.patterns[pattern].MatchString(str), nil
}

// testingFilter, testingSimpleMatch, testingAlternationMatch,
// splitTestingRegexp, rewriteTestingName and isTestingNameSpace are
// testing's filterMatch, simpleMatch, alternationMatch, splitRegexp, rewrite
// and isSpace (testing/match.go, unchanged from Go 1.20 to Go 1.27). verify
// rewrites each pattern in place before compiling it, as testing does.
type testingFilter interface {
	matches(name []string, matchString func(pat, str string) (bool, error)) (ok, partial bool)
	verify(compile func(pattern string) error) error
}

type testingSimpleMatch []string

type testingAlternationMatch []testingFilter

func (m testingSimpleMatch) matches(name []string, matchString func(pat, str string) (bool, error)) (ok, partial bool) {
	for i, s := range name {
		if i >= len(m) {
			break
		}
		if ok, _ := matchString(m[i], s); !ok {
			return false, false
		}
	}
	return true, len(name) < len(m)
}

func (m testingSimpleMatch) verify(compile func(pattern string) error) error {
	for i, s := range m {
		m[i] = rewriteTestingName(s)
	}
	for _, s := range m {
		if err := compile(s); err != nil {
			return err
		}
	}
	return nil
}

func (m testingAlternationMatch) matches(name []string, matchString func(pat, str string) (bool, error)) (ok, partial bool) {
	for _, m := range m {
		if ok, partial = m.matches(name, matchString); ok {
			return ok, partial
		}
	}
	return false, false
}

func (m testingAlternationMatch) verify(compile func(pattern string) error) error {
	for _, filter := range m {
		if err := filter.verify(compile); err != nil {
			return err
		}
	}
	return nil
}

// splitTestingRegexp splits a pattern at slashes and bars outside brackets
// and parentheses.
func splitTestingRegexp(s string) testingFilter {
	a := make(testingSimpleMatch, 0, strings.Count(s, "/"))
	b := make(testingAlternationMatch, 0, strings.Count(s, "|"))
	cs := 0
	cp := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '[':
			cs++
		case ']':
			if cs--; cs < 0 { // An unmatched ']' is legal.
				cs = 0
			}
		case '(':
			if cs == 0 {
				cp++
			}
		case ')':
			if cs == 0 {
				cp--
			}
		case '\\':
			i++
		case '/':
			if cs == 0 && cp == 0 {
				a = append(a, s[:i])
				s = s[i+1:]
				i = 0
				continue
			}
		case '|':
			if cs == 0 && cp == 0 {
				a = append(a, s[:i])
				s = s[i+1:]
				i = 0
				b = append(b, a)
				a = make(testingSimpleMatch, 0, len(a))
				continue
			}
		}
		i++
	}

	a = append(a, s)
	if len(b) == 0 {
		return a
	}
	return append(b, a)
}

func rewriteTestingName(s string) string {
	b := []byte{}
	for _, r := range s {
		switch {
		case isTestingNameSpace(r):
			b = append(b, '_')
		case !strconv.IsPrint(r):
			s := strconv.QuoteRune(r)
			b = append(b, s[1:len(s)-1]...)
		default:
			b = append(b, string(r)...)
		}
	}
	return string(b)
}

func isTestingNameSpace(r rune) bool {
	if r < 0x2000 {
		switch r {
		// Note: not the same as Unicode Z class.
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680:
			return true
		}
	} else {
		if r <= 0x200a {
			return true
		}
		switch r {
		case 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
			return true
		}
	}
	return false
}

// testingMUsesStandardDeps reports whether m matches test names with
// testing/internal/testdeps, the regexp matcher of go test binaries.
func testingMUsesStandardDeps(m *testing.M) bool {
	if m == nil {
		return false
	}
	deps := reflect.ValueOf(m).Elem().FieldByName("deps")
	if !deps.IsValid() || deps.Kind() != reflect.Interface || deps.IsNil() {
		return false
	}
	typ := deps.Elem().Type()
	return typ.PkgPath() == "testing/internal/testdeps" && typ.Name() == "TestDeps"
}

// testingFlagValues returns the values the named flags of fs have, or will
// have once args are parsed. Before flag.Parse, args are parsed into copies
// of every flag of fs, so no flag changes and no output is written. It
// reports false when a flag is missing or parsing fails, as testing would.
func testingFlagValues(fs *flag.FlagSet, args []string, parsed bool, names ...string) (map[string]string, bool) {
	values := make(map[string]string, len(names))
	if parsed {
		for _, name := range names {
			f := fs.Lookup(name)
			if f == nil {
				return nil, false
			}
			values[name] = f.Value.String()
		}
		return values, true
	}
	shadow := flag.NewFlagSet("", flag.ContinueOnError)
	shadow.SetOutput(io.Discard)
	shadow.Usage = func() {}
	copies := map[string]*shadowFlagValue{}
	fs.VisitAll(func(f *flag.Flag) {
		value := &shadowFlagValue{value: f.Value.String()}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			value.boolFlag = true
		}
		shadow.Var(value, f.Name, f.Usage)
		copies[f.Name] = value
	})
	if err := shadow.Parse(args); err != nil {
		return nil, false
	}
	for _, name := range names {
		value := copies[name]
		if value == nil {
			return nil, false
		}
		values[name] = value.value
	}
	return values, true
}

// shadowFlagValue records a flag's value as a string. Bool flags take no
// separate argument, as with the flag they copy.
type shadowFlagValue struct {
	value    string
	boolFlag bool
}

func (v *shadowFlagValue) String() string     { return v.value }
func (v *shadowFlagValue) Set(s string) error { v.value = s; return nil }
func (v *shadowFlagValue) IsBoolFlag() bool   { return v.boolFlag }
