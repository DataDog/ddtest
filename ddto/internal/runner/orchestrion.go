package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

const orchestrionBypassEnv = "DDTEST_ORCHESTRION_BYPASS"

// Recognize the actual tool command, not package names or arbitrary arguments.
// A different user wrapper remains fully transparent.
func isOrchestrionToolexec(command string) bool {
	_, ok := orchestrionLauncher(command)
	return ok
}

// The go wrapper owns the job server and its file handles until the build ends.
// Plain toolexec can leave a detached daemon holding WORK open on Windows.
func orchestrionLauncher(command string) (goLauncher, bool) {
	words, err := splitFlags(command)
	if err != nil {
		return goLauncher{}, false
	}
	if len(words) == 4 && strings.TrimSuffix(filepath.Base(words[0]), ".exe") == "go" && words[1] == "tool" && words[2] == "orchestrion" && words[3] == "toolexec" {
		return goLauncher{[]string{words[0], "tool", "orchestrion", "go"}, command}, true
	}
	if len(words) == 2 && strings.TrimSuffix(filepath.Base(words[0]), ".exe") == "orchestrion" && words[1] == "toolexec" {
		return goLauncher{[]string{words[0], "go"}, command}, true
	}
	return goLauncher{}, false
}

// ddtest owns testing and Testify. Applying testing advice twice redeclares
// linkname hooks; weaving Mini itself can also trace delivery.
// Version probes and application packages still go through Orchestrion.
func bypassOrchestrionPackage(args []string) bool {
	if len(args) == 2 && args[1] == "-V=full" {
		return false
	}
	scope := os.Getenv(orchestrionBypassEnv)
	if scope != string(Mini) && scope != string(SDK) {
		return false
	}
	pkg, _, _ := strings.Cut(os.Getenv("TOOLEXEC_IMPORTPATH"), " [")
	return pkg == "testing" || pkg == instrument.TestifySuiteImport ||
		scope == string(Mini) && (isSDKCIPackage(pkg) || pkg == miniModule || strings.HasPrefix(pkg, miniModule+"/"))
}

// Package discovery stays native. The selected Orchestrion executable owns
// its build job server; compiler calls reuse its resolved absolute path.
type goLauncherKey struct{}
type goLauncher struct {
	command []string
	tool    string
}

// RunOrchestrion wraps orchestrion go test, or go tool orchestrion go test.
// Orchestrion receives the final overlay/modfile and retains its pin checks.
func RunOrchestrion(ctx context.Context, args []string, runtime Runtime, goTool bool, stdin io.Reader, stdout, stderr io.Writer) int {
	command := []string{"orchestrion", "go"}
	tool := []string{"orchestrion", "toolexec"}
	if goTool {
		command = []string{"go", "tool", "orchestrion", "go"}
		tool = []string{"go", "tool", "orchestrion", "toolexec"}
	}
	quoted, err := quoteToolWords(tool)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx = context.WithValue(ctx, goLauncherKey{}, goLauncher{command, quoted})
	return RunRuntime(ctx, args, runtime, stdin, stdout, stderr)
}

// quoteToolWords joins words for cmd/go's quoted.Split, which reads -exec,
// -toolexec and GOFLAGS. A word with both quote characters stays unquoted,
// which Split accepts unless it has spaces or starts with a quote; any word
// that Split produced can therefore be passed on.
func quoteToolWords(words []string) (string, error) {
	quoted := make([]string, len(words))
	for i, word := range words {
		switch {
		case !strings.Contains(word, "'"):
			quoted[i] = "'" + word + "'"
		case !strings.Contains(word, `"`):
			quoted[i] = `"` + word + `"`
		case !strings.ContainsAny(word, " \t\n\r") && word[0] != '\'' && word[0] != '"':
			quoted[i] = word
		default:
			return "", fmt.Errorf("cannot quote tool argument containing spaces and both quote characters: %s", word)
		}
	}
	return strings.Join(quoted, " "), nil
}

// Orchestrion's explicit -toolexec overrides GOFLAGS, but a user's command-line
// override keeps priority, as it does with orchestrion go test itself.
func applyGoLauncher(ctx context.Context, opts *options) {
	launcher, ok := ctx.Value(goLauncherKey{}).(goLauncher)
	if !ok {
		return
	}
	for _, arg := range opts.arguments {
		if arg.flag == "toolexec" {
			return
		}
	}
	opts.toolexec = launcher.tool
}

// ValidToolMode validates the CLI private tool entrypoint before dispatch.
func ValidToolMode(mode string) bool {
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

// Resolve module tools from the final module graph once, before any compiler
// changes directory. The bootstrap itself must not inherit the test wrapper.
func prepareOrchestrionLauncher(ctx context.Context, dir string, opts options, plan *Plan, progress io.Writer) error {
	var selected *goLauncher
	if plan.orchestrion {
		launcher, _ := orchestrionLauncher(opts.toolexec)
		binary, err := resolveOrchestrionExecutable(ctx, dir, opts, plan.Modfile, launcher, progress)
		if err != nil {
			return err
		}
		plan.orchestrionChain, err = quoteToolWords([]string{binary, "toolexec"})
		if err != nil {
			return err
		}
		selected = &goLauncher{[]string{binary, "go"}, opts.toolexec}
	}
	if explicit, ok := ctx.Value(goLauncherKey{}).(goLauncher); ok {
		if selected != nil && selected.tool == explicit.tool {
			plan.launcher = selected
			return nil
		}
		binary, err := resolveOrchestrionExecutable(ctx, dir, opts, plan.Modfile, explicit, progress)
		if err != nil {
			return err
		}
		plan.launcher = &goLauncher{[]string{binary, "go"}, explicit.tool}
	} else if os.Getenv("ORCHESTRION_JOBSERVER_URL") == "" {
		plan.launcher = selected
	}
	return nil
}

func resolveOrchestrionExecutable(ctx context.Context, dir string, opts options, modfile string, launcher goLauncher, progress io.Writer) (string, error) {
	if len(launcher.command) == 2 {
		name := launcher.command[0]
		if strings.ContainsAny(name, "/\\") && !filepath.IsAbs(name) {
			name = filepath.Join(dir, name)
		}
		binary, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		return filepath.Abs(binary)
	}
	phase := debugFromContext(ctx).start("resolve Orchestrion executable")
	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, launcher.command[0], "tool", "-n", "orchestrion")
	cmd.Dir = dir
	// Only module selection belongs to the tool bootstrap. Client compile/test
	// flags, ddtest's overlay and a toolexec command could instrument the tool itself.
	flags := []string{"-toolexec="}
	if opts.mod != "" {
		flags = append(flags, "-mod="+opts.mod)
	}
	if modfile == "" {
		modfile = opts.modfile
	}
	if modfile != "" {
		flags = append(flags, "-modfile="+modfile)
	}
	if opts.overlay != "" {
		flags = append(flags, "-overlay="+opts.overlay)
	}
	quoted, err := quoteToolWords(flags)
	if err != nil {
		phase.finish(err)
		return "", err
	}
	cmd.Env = append(cmd.Environ(), "GOFLAGS="+quoted)
	cmd.Stdout, cmd.Stderr = &output, progress
	err = cmd.Run()
	phase.finish(err)
	if err != nil {
		return "", fmt.Errorf("resolve Orchestrion executable: %w", err)
	}
	binary := strings.TrimSpace(output.String())
	if !filepath.IsAbs(binary) {
		return "", fmt.Errorf("go tool did not return an absolute Orchestrion executable: %q", binary)
	}
	return binary, nil
}
