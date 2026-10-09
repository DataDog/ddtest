package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCoverageSelection(t *testing.T) {
	root := t.TempDir()
	client := goPackage{Dir: root, ImportPath: "example.com/client"}
	helper := goPackage{Dir: filepath.Join(root, "helpers"), ImportPath: "example.com/client/helpers"}
	for _, tc := range []struct {
		name    string
		args    []string
		env     string
		changed []goPackage
		want    bool
	}{
		{"off", nil, "", []goPackage{helper}, false},
		{"test files only", []string{"-cover"}, "", nil, false},
		{"default excludes helper", []string{"-cover"}, "", []goPackage{helper}, false},
		{"default includes root", []string{"-cover"}, "", []goPackage{client}, true},
		{"root pattern", []string{"-coverpkg=."}, "", []goPackage{helper}, false},
		{"recursive pattern", []string{"-coverpkg=./..."}, "", []goPackage{helper}, true},
		{"import pattern", []string{"-coverpkg=example.com/client/..."}, "", []goPackage{client}, true},
		{"outside relative tree", []string{"-coverpkg=./..."}, "", []goPackage{{Dir: filepath.Join(filepath.Dir(root), "goroot", "src", "testing"), ImportPath: "testing"}}, false},
		{"other pattern", []string{"-coverpkg=example.com/other/..."}, "", []goPackage{helper}, false},
		{"GOFLAGS", nil, "-coverpkg=./...", []goPackage{helper}, true},
		{"empty coverpkg", []string{"-coverpkg="}, "", []goPackage{client}, true},
		{"profile", []string{"-coverprofile=result.out"}, "", []goPackage{client}, true},
		{"custom args", []string{"-args", "-coverpkg=./..."}, "", []goPackage{helper}, false},
		{"false", []string{"-cover=false"}, "", []goPackage{client}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseOptions(tc.args, tc.env)
			if err != nil {
				t.Fatal(err)
			}
			if got := needsCoverOverlay(root, opts, []goPackage{client}, tc.changed); got != tc.want {
				t.Fatalf("activation=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestCoverInputTranslation(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original.go")
	actual := filepath.Join(dir, "rewritten.go")
	untouched := filepath.Join(dir, "untouched.go")
	overlay := filepath.Join(dir, "overlay.json")
	data, _ := json.Marshal(Overlay{Replace: map[string]string{original: actual}})
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	outList := filepath.Join(dir, "outputs.txt")
	args := []string{"cover", "-pkgcfg=config", "-outfilelist", outList, original, untouched}
	saved := append([]string(nil), args...)
	got, err := translateCoverInputs(overlay, args)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, saved) {
		t.Fatal("mutated caller arguments")
	}
	want := []string{"cover", "-pkgcfg=config", "-outfilelist", outList, actual, untouched}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inputs=%v, want %v", got, want)
	}
	if _, err := translateCoverInputs(filepath.Join(dir, "missing.json"), args); err == nil {
		t.Fatal("missing plan accepted")
	}
}

func TestCoverToolForwardingAndErrors(t *testing.T) {
	output, err := exec.Command("go", "env", "GOTOOLDIR").Output()
	if err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if strings.HasSuffix(os.Args[0], ".exe") {
		suffix = ".exe"
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	data, _ := json.Marshal(Overlay{Replace: map[string]string{}})
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	cover := filepath.Join(strings.TrimSpace(string(output)), "cover"+suffix)
	want, err := exec.Command(cover, "-V=full").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	// The identity probe needs no plan: unrelated packages never read it.
	code := RunCoverTool(context.Background(), "missing-overlay", []string{cover, "-V=full"}, nil, &stdout, &stderr)
	if code != 0 || stdout.String() != appendCoverIdentity(string(want), coverFingerprint()) || stderr.Len() != 0 {
		t.Fatalf("cover identity: %d %s %s", code, stdout.String(), stderr.String())
	}
	if code := RunCoverTool(context.Background(), "missing-overlay", nil, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing tool exit=%d", code)
	}
	if code := RunCoverTool(context.Background(), "missing-overlay", []string{cover, "-mode=set", "file.go"}, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing overlay exit=%d", code)
	}
	// A forwarded tool failure must retain its native exit status.
	err = exec.Command(cover, "-invalid-ddtest-flag").Run()
	native, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected native failure: %v", err)
	}
	if got := RunCoverTool(context.Background(), overlay, []string{cover, "-invalid-ddtest-flag"}, nil, &stdout, &stderr); got != native.ExitCode() {
		t.Fatalf("exit=%d, native=%d", got, native.ExitCode())
	}
}

func TestCoverCommandQuoting(t *testing.T) {
	for _, paths := range [][2]string{{"/path with spaces/ddtest", "/path/overlay.json"}, {`C:\Program Files\ddtest.exe`, `C:\build\overlay.json`}, {"/path/it's/ddtest", `/path/\"quoted\"/overlay.json`}, {`/path'with"both/ddtest`, "/path/overlay.json"}} {
		command, err := toolCommand(paths[0], paths[1], "cover")
		if err != nil {
			t.Fatal(err)
		}
		got, err := splitFlags(command)
		want := []string{paths[0], "tool-overlay", "cover", paths[1]}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("quoted command=%q: %v, %v", command, got, err)
		}
	}
	if _, err := toolCommand(`path with'both"`, "overlay", "cover"); err == nil {
		t.Fatal("ambiguous quoting accepted")
	}
}

func TestCoverIdentityTracksTheContract(t *testing.T) {
	if coverFingerprint() != coverFingerprint() || len(coverFingerprint()) != 64 {
		t.Fatal("cover identity is not a stable contract hash")
	}
	for _, version := range []string{"cover version go1.27.1\n", "cover version devel go1.28 buildID=action/content\n"} {
		tagged := appendCoverIdentity(version, "fixture")
		if !strings.Contains(tagged, "fixture") || !strings.HasPrefix(tagged, strings.TrimSpace(version)) {
			t.Fatalf("invalid tool identity: %q", tagged)
		}
	}
}
