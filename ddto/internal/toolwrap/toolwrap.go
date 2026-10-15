// Package toolwrap runs go's tool invocations for ddto's -toolexec. ddto
// builds it into a small executable of its own, from the sources that Sources
// returns, so that each invocation starts without ddto's package graph: most
// invocations only run the tool. ddto itself runs the same code when that
// executable is unavailable. This package imports only the standard library.
package toolwrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// UserToolexecEnv carries the user's -toolexec after ddto's input
	// transformation. Orchestrion skips packages that ddto owns; unrelated
	// wrappers run every tool.
	UserToolexecEnv = "DDTO_USER_TOOLEXEC"
	// OrchestrionBypassEnv names the runtime whose packages Orchestrion skips.
	OrchestrionBypassEnv = "DDTO_ORCHESTRION_BYPASS"
	// GitStateHandoff is goenv.GitStateHandoff: ddto's Git worker state.
	GitStateHandoff = "DDTO_GIT_STATE"
	// LinkingFile is the SDK's CLIGitLinkingFile in that state.
	LinkingFile = "linking"
	// MiniModule is the module of ddto and its Mini runtime.
	MiniModule = "github.com/tonyredondo/dd-ci-testing-poc"
	// TestifySuiteImport and GoleakImport are the packages that ddto rewrites.
	TestifySuiteImport = "github.com/stretchr/testify/suite"
	GoleakImport       = "go.uber.org/goleak"
	// The SDK packages whose compiles carry ddto's guards.
	SDKCIConfigPackage      = "github.com/DataDog/dd-trace-go/v2/internal/config"
	SDKCIEnvironmentPackage = "github.com/DataDog/dd-trace-go/v2/internal/civisibility/envconfig"
	SDKTracerPackage        = "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	// KeyFlag makes the wrapper executable print its key, which ddto checks
	// before it uses the executable.
	KeyFlag = "-ddto-toolwrap-key"
)

// Main runs the wrapper executable, given its arguments after its name: ddto's
// executable, the tool mode, the plan and the tool's command line. Tool
// invocations that need the plan run ddto's tool-overlay; the others run the
// tool, as ddto would. prefix starts ddto's error messages.
func Main(args []string, key, prefix string) int {
	if len(args) == 1 && args[0] == KeyFlag {
		fmt.Println(key)
		return 0
	}
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, prefix+" ERROR: invalid tool wrapper arguments")
		return 2
	}
	ddto, mode, plan, tool := args[0], args[1], args[2], args[3:]
	if !ValidMode(mode) {
		fmt.Fprintln(os.Stderr, prefix+" ERROR: invalid tool mode")
		return 2
	}
	NoteTestLink(mode, tool)
	if !NeedsPlan(mode, tool, os.Getenv("TOOLEXEC_IMPORTPATH")) {
		return ExecNative(tool, prefix)
	}
	return runDDTO(ddto, append([]string{"tool-overlay", mode, plan}, tool...))
}

// ValidMode validates the private tool entrypoint's mode before dispatch.
func ValidMode(mode string) bool {
	// The link signal combines with any mode.
	if mode == "link" {
		return true
	}
	mode = strings.TrimSuffix(mode, "-link")
	if strings.HasPrefix(mode, "mini-") {
		if mode == "mini-sdk" || mode == "mini-sdk-nomirror" {
			return true
		}
		if !strings.HasPrefix(mode, "mini-sdk-") {
			return false
		}
		mode = strings.TrimPrefix(mode, "mini-sdk-")
		mode = strings.TrimPrefix(mode, "nomirror-")
	}
	if mode == "orchestrion" {
		return true
	}
	mode = strings.TrimPrefix(mode, "orchestrion-")
	switch mode {
	case "testify", "cover", "testify-cover", "goleak", "goleak-cover", "testify-goleak", "testify-goleak-cover":
		return true
	}
	return false
}

// NeedsPlan is the allocation-free dispatch before any plan I/O. Go probes
// compile/link versions without a package identity; those skip plan I/O and
// still reach the selected user tool, including Orchestrion.
func NeedsPlan(mode string, args []string, importPath string) bool {
	if len(args) == 0 {
		return false
	}
	tool := filepath.Base(args[0])
	tool = strings.TrimSuffix(tool, ".exe")
	if tool == "cover" {
		if !strings.Contains(mode, "cover") {
			return false
		}
		if len(args) == 2 && args[1] == "-V=full" {
			return true
		}
		// Cover opens logical paths; ddto translates the standard-library
		// packages whose overlay it rewrites.
		pkg, _, _ := strings.Cut(importPath, " [")
		return pkg == "testing" || pkg == "os/signal"
	}
	if tool == "compile" && (mode == "mini-sdk" || strings.HasPrefix(mode, "mini-sdk-")) && !(len(args) == 2 && args[1] == "-V=full") {
		if pkg, _ := SDKCompilePackage(importPath); pkg != "" && (pkg != SDKTracerPackage || !strings.Contains(mode, "nomirror")) {
			return true
		}
	}
	if tool != "compile" || !strings.Contains(mode, "testify") && !strings.Contains(mode, "goleak") {
		return false
	}
	if len(args) == 2 && args[1] == "-V=full" {
		return false
	}
	pkg, _, _ := strings.Cut(importPath, " [")
	return pkg == TestifySuiteImport && strings.Contains(mode, "testify") || (pkg == GoleakImport || pkg == GoleakImport+"_test" || pkg == GoleakImport+".test") && strings.Contains(mode, "goleak")
}

// IsSDKCIPackage reports whether pkg is an SDK package that ddto guards.
func IsSDKCIPackage(pkg string) bool {
	return pkg == SDKCIEnvironmentPackage || pkg == SDKCIConfigPackage
}

// SDKCompilePackage returns the guarded SDK package whose compiler cache
// marker a compile receives. Go applies a package's gcflags to its internal
// test variant, "pkg [pkg.test]", and also to its external test package and
// test main, so all of them carry the marker. own reports whether the inputs
// are the package's sources, which the guards rewrite; the external test and
// the test main only need the marker removed, as for goleak.
func SDKCompilePackage(importPath string) (pkg string, own bool) {
	path, _, _ := strings.Cut(importPath, " [")
	own = true
	if base, ok := strings.CutSuffix(path, "_test"); ok {
		path, own = base, false
	} else if base, ok := strings.CutSuffix(path, ".test"); ok {
		path, own = base, false
	}
	if IsSDKCIPackage(path) || path == SDKTracerPackage {
		return path, own
	}
	return "", false
}

// NoteTestLink tells ddto's Git worker, through its state, that go test links
// a test binary. go test links one only when the binary's result is not in its
// cache, so a test process will run, and the worker can request for it while
// the binary links.
func NoteTestLink(mode string, args []string) {
	if mode != "link" && !strings.HasSuffix(mode, "-link") || len(args) < 2 || strings.TrimSuffix(filepath.Base(args[0]), ".exe") != "link" {
		return
	}
	output := ""
	for i, arg := range args[1:] {
		if value, ok := strings.CutPrefix(arg, "-o="); ok {
			output = value
		} else if arg == "-o" && i+2 < len(args) {
			output = args[i+2]
		}
	}
	state := os.Getenv(GitStateHandoff)
	if state == "" || !strings.HasSuffix(strings.TrimSuffix(output, ".exe"), ".test") {
		return
	}
	if mark, err := os.OpenFile(filepath.Join(state, LinkingFile), os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = mark.Close()
	}
}

// ChainUserToolexec puts the user's -toolexec in front of args, unless
// Orchestrion must skip the package.
func ChainUserToolexec(args []string) ([]string, error) {
	chain := os.Getenv(UserToolexecEnv)
	if chain == "" || BypassOrchestrionPackage(args) {
		return args, nil
	}
	words, err := SplitFlags(chain)
	if err != nil {
		return nil, fmt.Errorf("ddto: invalid -toolexec %q: %w", chain, err)
	}
	return append(words, args...), nil
}

// BypassOrchestrionPackage reports whether Orchestrion must not see a tool
// invocation. ddto owns testing and Testify. Applying testing advice twice
// redeclares linkname hooks; weaving Mini itself can also trace delivery.
// Version probes and application packages still go through Orchestrion.
func BypassOrchestrionPackage(args []string) bool {
	if len(args) == 2 && args[1] == "-V=full" {
		return false
	}
	scope := os.Getenv(OrchestrionBypassEnv)
	if scope != "mini" && scope != "sdk" {
		return false
	}
	pkg, _, _ := strings.Cut(os.Getenv("TOOLEXEC_IMPORTPATH"), " [")
	return pkg == "testing" || pkg == TestifySuiteImport ||
		scope == "mini" && (IsSDKCIPackage(pkg) || pkg == MiniModule || strings.HasPrefix(pkg, MiniModule+"/"))
}

// SplitFlags splits GOFLAGS and -toolexec values like cmd/go's quoted.Split:
// a field may be wrapped in single or double quotes, without unescaping.
func SplitFlags(s string) ([]string, error) {
	var out []string
	for {
		s = strings.TrimLeft(s, " \t\n\r")
		if s == "" {
			return out, nil
		}
		if quote := s[0]; quote == '\'' || quote == '"' {
			end := strings.IndexByte(s[1:], quote)
			if end < 0 {
				return nil, fmt.Errorf("unterminated %c string", quote)
			}
			out = append(out, s[1:end+1])
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t\n\r")
		if end < 0 {
			end = len(s)
		}
		out = append(out, s[:end])
		s = s[end:]
	}
}
