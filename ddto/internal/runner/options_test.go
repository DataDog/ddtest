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
	plan := Plan{File: "/plan/overlay.json", goleakCache: "-gcflags=go.uber.org/goleak=-I=marker"}
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
			"ddtest tool-overlay goleak plan",
			[]string{"test", "-overlay=/plan/overlay.json", "-toolexec=ddtest tool-overlay goleak plan", "-gcflags=all=-N", "./...", "-gcflags", "-l", "-gcflags=go.uber.org/goleak=-I=marker", "-run", "X"},
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
