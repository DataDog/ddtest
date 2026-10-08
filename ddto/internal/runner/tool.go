package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// userToolexecEnv carries the user's -toolexec after ddtest's input transformation.
// Orchestrion skips packages that ddtest owns; unrelated wrappers run every tool.
const userToolexecEnv = "DDTEST_USER_TOOLEXEC"

func chainUserToolexec(args []string) ([]string, error) {
	chain := os.Getenv(userToolexecEnv)
	if chain == "" || bypassOrchestrionPackage(args) {
		return args, nil
	}
	words, err := splitFlags(chain)
	if err != nil {
		return nil, fmt.Errorf("ddtest: invalid -toolexec %q: %w", chain, err)
	}
	return append(words, args...), nil
}

func (p Plan) toolMode() string {
	var modes []string
	if p.orchestrion {
		if p.mini {
			modes = append(modes, "mini")
		}
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
	return strings.Join(modes, "-")
}

// ToolNeedsPlan is the allocation-free dispatch before any plan I/O. Go probes
// compile/link versions without a package identity; those skip plan I/O and
// still reach the selected user tool, including Orchestrion.
func ToolNeedsPlan(mode string, args []string, importPath string) bool {
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
		pkg, _, _ := strings.Cut(importPath, " [")
		return pkg == "testing"
	}
	if tool == "compile" && strings.HasPrefix(mode, "mini-orchestrion") && isSDKCIPackage(importPath) && !(len(args) == 2 && args[1] == "-V=full") {
		return true
	}
	if tool != "compile" || !strings.Contains(mode, "testify") && !strings.Contains(mode, "goleak") {
		return false
	}
	if len(args) == 2 && args[1] == "-V=full" {
		return false
	}
	pkg, _, _ := strings.Cut(importPath, " [")
	return pkg == instrument.TestifySuiteImport && strings.Contains(mode, "testify") || pkg == instrument.GoleakImport && strings.Contains(mode, "goleak")
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
	if isSDKCIPackage(os.Getenv("TOOLEXEC_IMPORTPATH")) {
		forwarded, cleanup, err := prepareSDKCICompile(args, os.Getenv("TOOLEXEC_IMPORTPATH"))
		if err != nil {
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
