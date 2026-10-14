package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// userToolexecEnv carries the user's -toolexec after ddto's input transformation.
// Orchestrion skips packages that ddto owns; unrelated wrappers run every tool.
const userToolexecEnv = toolwrap.UserToolexecEnv

func chainUserToolexec(args []string) ([]string, error) {
	return toolwrap.ChainUserToolexec(args)
}

func (p Plan) toolMode() string {
	var modes []string
	if p.sdkCI {
		modes = append(modes, "mini-sdk")
		if p.sdkMirrorDisabled {
			modes = append(modes, "nomirror")
		}
	}
	if p.orchestrion {
		modes = append(modes, "orchestrion")
	}
	if p.testify {
		modes = append(modes, "testify")
	}
	if p.goleak {
		modes = append(modes, "goleak")
	}
	if p.coverOverlay {
		modes = append(modes, "cover")
	}
	if p.linkSignal {
		modes = append(modes, "link")
	}
	return strings.Join(modes, "-")
}

// NoteTestLink tells ddto's Git worker, through its state, that go test links
// a test binary. go test links one only when the binary's result is not in its
// cache, so a test process will run, and the worker can request for it while
// the binary links.
func NoteTestLink(mode string, args []string) {
	toolwrap.NoteTestLink(mode, args)
}

// ToolNeedsPlan is the allocation-free dispatch before any plan I/O. Go probes
// compile/link versions without a package identity; those skip plan I/O and
// still reach the selected user tool, including Orchestrion.
func ToolNeedsPlan(mode string, args []string, importPath string) bool {
	return toolwrap.NeedsPlan(mode, args, importPath)
}

// RunTool handles only a selected transform. CLI bypasses unrelated tools
// before entering here, with native process replacement on Unix.
func RunTool(ctx context.Context, overlay string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR: missing tool executable")
		return 2
	}
	if tool := strings.TrimSuffix(filepath.Base(args[0]), ".exe"); tool == "cover" {
		return RunCoverTool(ctx, overlay, args, stdin, stdout, stderr)
	}
	if pkg, own := sdkCompilePackage(os.Getenv("TOOLEXEC_IMPORTPATH")); pkg != "" {
		if !own {
			// External tests and the test main have no SDK sources to guard.
			forwarded := removeCompilerCacheMarker(append([]string(nil), args...), sdkCompilerCacheMarker(pkg))
			return runChainedTool(ctx, forwarded, stdin, stdout, stderr)
		}
		forwarded, cleanup, err := prepareSDKCICompile(args, pkg)
		if err != nil {
			if pkg == sdkTracerPackage && errors.Is(err, instrument.ErrUnsupportedAPI) {
				// Orchestrion can introduce a tracer after package resolution.
				// Original inputs remain intact; CI gates are separate transforms.
				fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN: SDK span copies disabled:", err)
				forwarded := removeCompilerCacheMarker(append([]string(nil), args...), sdkCompilerCacheMarker(pkg))
				return runChainedTool(ctx, forwarded, stdin, stdout, stderr)
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		defer cleanup()
		return runChainedTool(ctx, forwarded, stdin, stdout, stderr)
	}
	data, err := os.ReadFile(overlay)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var plan Overlay
	if err := json.Unmarshal(data, &plan); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	entry := plan.Testify
	pkg, _, _ := strings.Cut(os.Getenv("TOOLEXEC_IMPORTPATH"), " [")
	if pkg == instrument.GoleakImport+"_test" || pkg == instrument.GoleakImport+".test" {
		// Go applies the package's gcflags to its external tests and test main. Both
		// need only the cache marker removed, without rewriting goleak.Find.
		if plan.Goleak == nil {
			fmt.Fprintln(stderr, "missing optional library tool plan")
			return 2
		}
		forwarded := removeGoleakCacheMarker(append([]string(nil), args...), plan.Goleak.Fingerprint)
		return runChainedTool(ctx, forwarded, stdin, stdout, stderr)
	}
	if pkg == instrument.GoleakImport {
		entry = plan.Goleak
	}
	forwarded, cleanup, err := prepareLibraryCompile(entry, args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer cleanup()
	return runChainedTool(ctx, forwarded, stdin, stdout, stderr)
}

func runChainedTool(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	forwarded, err := chainUserToolexec(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cmd := exec.CommandContext(ctx, forwarded[0], forwarded[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}
