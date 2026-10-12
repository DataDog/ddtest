package runner

import (
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestPackageAndFlagSelection(t *testing.T) {
	cases := []struct{ args, packages, flags []string }{
		{[]string{"-race", "-tags", "integration", "-run", "TestOne", "./...", "-args", "./not-a-package"}, []string{"./..."}, []string{"-race", "-tags", "integration"}},
		// As in go test, a non-flag after the package list and a flag starts
		// the test binary arguments: ./two and the cover flags are not ours.
		{[]string{"./one", "-count=2", "./two", "-covermode=atomic", "-cover"}, []string{"./one"}, nil},
		{[]string{"-json", "-timeout", "10s"}, []string{"."}, nil},
		{[]string{"-test.v", "-test.run=TestPass", "."}, []string{"."}, nil},
		{[]string{"--race", "--tags=x", "./..."}, []string{"./..."}, []string{"--race", "--tags=x"}},
		// An unknown flag ends the package list; a following non-flag may be its value.
		{[]string{"-update", "./one"}, []string{"."}, nil},
		{[]string{"./one", "-update", "golden", "-race"}, []string{"./one"}, []string{"-race"}},
		{[]string{"-benchmem", "-vet=off", "-exec", "env", "-artifacts", "-modcacherw", "-buildmode=default", "-linkshared", "-bench=.", "./..."}, []string{"./..."}, []string{"-modcacherw", "-buildmode=default", "-linkshared"}},
		{[]string{"--", "-update", "./one"}, []string{"."}, nil},
		{[]string{"--args", "-update"}, []string{"."}, nil},
		{[]string{"./a_test.go", "./b_test.go"}, []string{"./a_test.go", "./b_test.go"}, nil},
	}
	for _, c := range cases {
		got, err := parseOptions(c.args, "")
		if err != nil {
			t.Fatal(c.args, err)
		}
		if !reflect.DeepEqual(got.packages, c.packages) || !reflect.DeepEqual(got.buildFlags, c.flags) {
			t.Errorf("%v: packages %v, flags %v", c.args, got.packages, got.buildFlags)
		}
		var raw []string
		for _, argument := range got.arguments {
			raw = append(raw, argument.raw...)
		}
		if !reflect.DeepEqual(raw, c.args) {
			t.Errorf("%v: forwarded arguments changed: %v", c.args, raw)
		}
	}
}

func TestSpecialFlags(t *testing.T) {
	opts, err := parseOptions([]string{"-C", "sub", "-v", "."}, "")
	if err != nil || opts.chdir != "sub" || !reflect.DeepEqual(opts.packages, []string{"."}) {
		t.Fatalf("-C: %+v, %v", opts, err)
	}
	for _, args := range [][]string{{"--C=sub"}, {"-C=sub"}, {"--C", "sub"}} {
		if opts, err := parseOptions(args, ""); err != nil || opts.chdir != "sub" {
			t.Fatalf("%v: %q, %v", args, opts.chdir, err)
		}
	}
	for _, args := range [][]string{{"-v", "-C", "sub"}, {"-tags"}, {"-cover=maybe"}} {
		if _, err := parseOptions(args, ""); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
	for _, help := range []string{"-h", "--help", "-?"} {
		if opts, err := parseOptions([]string{"-v", help}, ""); err != nil || !opts.help {
			t.Fatalf("%s: %+v, %v", help, opts, err)
		}
	}
	opts, err = parseOptions([]string{"--overlay", "user.json", "-toolexec='orchestrion toolexec'"}, `-toolexec=other -cover -overlay=env.json`)
	if err != nil || opts.overlay != "user.json" || opts.toolexec != "'orchestrion toolexec'" || !opts.coverage {
		t.Fatalf("overlay/toolexec: %+v, %v", opts, err)
	}
	if opts, err := parseOptions(nil, `'-toolexec=orchestrion toolexec' -unknown`); err != nil || opts.toolexec != "orchestrion toolexec" {
		t.Fatalf("GOFLAGS toolexec: %+v, %v", opts, err)
	}
	if _, err := parseOptions(nil, "-C=dir"); err == nil {
		t.Fatal("GOFLAGS -C accepted")
	}
}

func TestSplitFlagsMatchesQuotedSplit(t *testing.T) {
	for input, want := range map[string][]string{
		`a 'b c' "d 'e"`:     {"a", "b c", "d 'e"},
		"  -x\t-y\r\n-z  ":   {"-x", "-y", "-z"},
		`a'b c'`:             {"a'b", "c'"},
		`'C:\Program Files'`: {`C:\Program Files`},
		"":                   nil,
	} {
		if got, err := splitFlags(input); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("splitFlags(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := splitFlags(`a "b`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}

func TestGoTestArgumentsReplaceUserOverlayAndToolexec(t *testing.T) {
	plan := Plan{File: "/plan/overlay.json", compilerCache: []string{"-gcflags=go.uber.org/goleak=-I=marker"}}
	for _, tc := range []struct {
		args []string
		tool string
		want []string
	}{
		{
			[]string{"--overlay", "user.json", "-v", "--overlay=other.json", "./...", "-args", "-overlay=binary"},
			"",
			[]string{"test", "-overlay=/plan/overlay.json", "-gcflags=go.uber.org/goleak=-I=marker", "-v", "./...", "-args", "-overlay=binary"},
		},
		{
			[]string{"-gcflags=all=-N", "-toolexec", "user tool", "./...", "-gcflags", "-l", "-run", "X"},
			"ddto tool-overlay goleak plan",
			[]string{"test", "-overlay=/plan/overlay.json", "-toolexec=ddto tool-overlay goleak plan", "-gcflags=all=-N", "./...", "-gcflags", "-l", "-gcflags=go.uber.org/goleak=-I=marker", "-run", "X"},
		},
		{
			[]string{"-toolexec=user", "./..."},
			"",
			[]string{"test", "-overlay=/plan/overlay.json", "-gcflags=go.uber.org/goleak=-I=marker", "-toolexec=user", "./..."},
		},
		{
			[]string{"-update", "golden", "--", "-gcflags=-N"},
			"",
			[]string{"test", "-overlay=/plan/overlay.json", "-gcflags=go.uber.org/goleak=-I=marker", "-update", "golden", "--", "-gcflags=-N"},
		},
	} {
		opts, err := parseOptions(tc.args, "")
		if err != nil {
			t.Fatal(err)
		}
		if got := goTestArguments(plan, opts, tc.tool); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q:\n got %q\nwant %q", tc.args, got, tc.want)
		}
	}
}

// TestFlagTableMatchesToolchain keeps the table in step with the go command
// running the tests: every documented flag is known, with its value kind.
func TestFlagTableMatchesToolchain(t *testing.T) {
	line := regexp.MustCompile(`(?m)^\t-([A-Za-z][A-Za-z0-9.-]*)( [^\n]+)?$`)
	for _, topic := range []string{"build", "test", "testflag"} {
		output, err := exec.Command("go", "help", topic).Output()
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, match := range line.FindAllStringSubmatch(string(output), -1) {
			name, takesValue := match[1], match[2] != ""
			if name == "args" {
				continue // Not a flag: the start of the test binary arguments.
			}
			found++
			spec, ok := goTestFlags[name]
			if !ok {
				t.Errorf("go help %s documents -%s, which is missing from goTestFlags", topic, name)
				continue
			}
			if want := map[bool]flagKind{true: valueFlag, false: boolFlag}[takesValue]; spec.kind != want {
				t.Errorf("-%s kind=%v, go help %s says value=%v", name, spec.kind, topic, takesValue)
			}
		}
		if found < 3 {
			t.Fatalf("go help %s: parsed only %d flags", topic, found)
		}
	}
	for name := range testBinaryFlags {
		if _, ok := goTestFlags[name]; !ok || strings.Contains(name, ".") {
			t.Errorf("test binary alias %s has no go test flag", name)
		}
	}
}

// As in go test, GOFLAGS apply first and the last -race wins. go test reports
// an invalid value itself.
func TestRaceOption(t *testing.T) {
	for _, c := range []struct {
		args    []string
		goflags string
		race    bool
	}{
		{[]string{"-race", "."}, "", true},
		{[]string{"--race=true", "."}, "", true},
		{[]string{"."}, "-race", true},
		{[]string{"-race=false", "."}, "-race", false},
		{[]string{"-race", "-race=0", "."}, "", false},
		{[]string{"-race=maybe", "."}, "-race", true},
		{[]string{".", "-args", "-race"}, "", false},
	} {
		opts, err := parseOptions(c.args, c.goflags)
		if err != nil || opts.race != c.race {
			t.Errorf("%v with GOFLAGS=%q: race %t, %v", c.args, c.goflags, opts.race, err)
		}
	}
}

// go test takes no result from its cache in local directory mode, or when its
// test binaries get an argument outside the cacheable flags, from the command
// line or GOFLAGS. Only then does the Git worker request for the test
// processes before one starts.
func TestRunsEveryTest(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		goflags string
		want    bool
	}{
		{[]string{"./..."}, "", false},
		{nil, "", true},
		{[]string{"-v", "-run", "^TestA$", "-race", "-json", "-coverprofile=c.out", "-timeout=1m", "./..."}, "", false},
		{[]string{"-count=1", "./..."}, "", true},
		{[]string{"-test.count=2", "./..."}, "", true},
		{[]string{"-test.run=X", "./..."}, "", false},
		{[]string{"-bench=.", "./..."}, "", true},
		{[]string{"-shuffle=on", "./..."}, "", true},
		{[]string{"-custom", "./..."}, "", true},
		{[]string{"./...", "-custom"}, "", true},
		{[]string{"./...", "-args", "value"}, "", true},
		{[]string{"./...", "-args"}, "", false},
		{[]string{"./...", "-args", "-test.run=^TestA$", "-test.v=true"}, "", false},
		{[]string{"./...", "--args", "-test.run=^TestA$"}, "", false},
		{[]string{"./...", "-args", "-test.count=1"}, "", true},
		{[]string{"./...", "-args", "-test.run"}, "", true},
		{[]string{"./...", "-args", "-run=^TestA$"}, "", true},
		{[]string{"./...", "--", "value"}, "", true},
		{[]string{"./...", "--"}, "", true},
		{[]string{"./...", "-v", "value"}, "", true},
		{[]string{"./..."}, "-count=1", true},
		{[]string{"./..."}, "-v -race", false},
	} {
		opts, err := parseOptions(tc.args, tc.goflags)
		if err != nil {
			t.Fatal(err)
		}
		if opts.runsEveryTest != tc.want {
			t.Errorf("go test %v with GOFLAGS=%q: runsEveryTest = %t, want %t", tc.args, tc.goflags, opts.runsEveryTest, tc.want)
		}
	}
}

// The debug log says why go test takes no result from its cache, without a
// value from the command line, and which flag links cached results.
func TestCacheRuleReasons(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		goflags, want string
	}{
		{[]string{"./..."}, "", ""},
		{nil, "", "local directory mode"},
		{[]string{"-count=1", "-shuffle=on", "./..."}, "", "test binary flag -count"},
		{[]string{"./..."}, "-count=1", "test binary flag -count in GOFLAGS"},
		{[]string{"-secret=value", "./..."}, "", "an unknown flag, which the test binary gets"},
		{[]string{"./...", "-v", "secret"}, "", "test binary arguments after the packages"},
		{[]string{"./...", "-args", "secret"}, "", "a test binary argument after -args"},
		{[]string{"./...", "--", "secret"}, "", "test binary arguments after --"},
	} {
		opts, err := parseOptions(tc.args, tc.goflags)
		if err != nil {
			t.Fatal(err)
		}
		if opts.cacheRule != tc.want || opts.runsEveryTest != (tc.want != "") {
			t.Errorf("go test %v with GOFLAGS=%q: cacheRule %q, runsEveryTest %t; want %q", tc.args, tc.goflags, opts.cacheRule, opts.runsEveryTest, tc.want)
		}
	}
	for args, want := range map[string]string{"-o=x.test ./...": "-o", "-a ./...": "-a", "-a -o=x.test ./...": "-o", "./...": ""} {
		opts, err := parseOptions(strings.Fields(args), "")
		if err != nil {
			t.Fatal(err)
		}
		if got := opts.linkRule(); got != want {
			t.Errorf("go test %s: linkRule %q, want %q", args, got, want)
		}
	}
}

// -o and -a make go test link test binaries even for results in its cache, so
// a link no longer tells that a test process will run.
func TestLinksCachedResults(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		goflags string
		want    bool
	}{
		{[]string{"./..."}, "", false},
		{[]string{"-o", "pkg.test", "./pkg"}, "", true},
		{[]string{"-o=pkg.test", "./pkg"}, "", true},
		{[]string{"-a", "./..."}, "", true},
		{[]string{"-a=false", "./..."}, "", false},
		{[]string{"./..."}, "-a", true},
		{[]string{"-race", "-v", "./..."}, "", false},
		// The last value wins, as in go test, GOFLAGS first.
		{[]string{"-a=false", "./..."}, "-a", false},
		{[]string{"-o=first.test", "-o=", "./pkg"}, "", false},
		{[]string{"-a=false", "-a", "./..."}, "", true},
	} {
		opts, err := parseOptions(tc.args, tc.goflags)
		if err != nil {
			t.Fatal(err)
		}
		if opts.linksCachedResults() != tc.want {
			t.Errorf("go test %v with GOFLAGS=%q: linksCachedResults = %t, want %t", tc.args, tc.goflags, opts.linksCachedResults(), tc.want)
		}
	}
}
