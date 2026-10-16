package toolwrap_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
)

// The wrapper imports only the standard library, so it repeats these names.
func TestConstantsMatchTheirOwners(t *testing.T) {
	for _, pair := range [][2]string{
		{toolwrap.GitStateHandoff, goenv.GitStateHandoff},
		{toolwrap.LinkingFile, utils.CLIGitLinkingFile},
		{toolwrap.TestifySuiteImport, instrument.TestifySuiteImport},
		{toolwrap.GoleakImport, instrument.GoleakImport},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%q != %q", pair[0], pair[1])
		}
	}
	mod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may end lines with CRLF.
	if first, _, _ := strings.Cut(string(mod), "\n"); strings.TrimSuffix(first, "\r") != "module "+toolwrap.MiniModule {
		t.Errorf("go.mod starts with %q, not module %s", first, toolwrap.MiniModule)
	}
}

// The wrapper's module holds every source file of this package but the one
// that embeds them, as they are, and a main package with its key and prefix.
func TestSources(t *testing.T) {
	files, key := toolwrap.Sources("prefix")
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"go.mod": true, "main.go": true}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "sources.go" {
			continue
		}
		want["toolwrap/"+name] = true
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(files["toolwrap/"+name], data) {
			t.Errorf("toolwrap/%s differs from %s", name, name)
		}
	}
	for name := range files {
		if !want[name] {
			t.Errorf("unexpected %s", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("missing %s", name)
	}
	if main := string(files["main.go"]); !strings.Contains(main, strconv.Quote(key)) || !strings.Contains(main, strconv.Quote("prefix")) {
		t.Errorf("main.go lacks the key or the prefix:\n%s", main)
	}
	if _, other := toolwrap.Sources("other prefix"); other == key || len(key) != 64 {
		t.Errorf("key %q does not depend on the prefix", key)
	}
}

// Modes name the library transforms and cover at most once each, in
// Plan.toolMode's order.
func TestValidMode(t *testing.T) {
	for mode, want := range map[string]bool{
		"testify": true, "cover": true, "goleak-cover": true, "testify-goleak-cover": true,
		"singleflight": true, "goleak-singleflight": true, "testify-goleak-singleflight-cover": true,
		"singleflight-cover-link": true, "mini-sdk-singleflight": true, "orchestrion-singleflight": true,
		"": false, "cover-testify": false, "singleflight-goleak": false, "goleak-goleak": false, "race": false,
	} {
		if got := toolwrap.ValidMode(mode); got != want {
			t.Errorf("ValidMode(%q) = %t, want %t", mode, got, want)
		}
	}
}

// The singleflight mode needs the plan for x/sync's compilation and its test
// variants, which only drop the cache marker.
func TestNeedsPlanForSingleflight(t *testing.T) {
	compile := []string{"/go/pkg/tool/compile", "-p", "x"}
	for _, pkg := range []string{toolwrap.SingleflightImport, toolwrap.SingleflightImport + "_test", toolwrap.SingleflightImport + ".test"} {
		if !toolwrap.NeedsPlan("singleflight", compile, pkg+" [x.test]") {
			t.Errorf("%s does not need the plan", pkg)
		}
	}
	if toolwrap.NeedsPlan("singleflight", compile, "example.com/other") || toolwrap.NeedsPlan("goleak", compile, toolwrap.SingleflightImport) {
		t.Error("another package, or another mode, needs the singleflight plan")
	}
}
