package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// RunCoverTool handles Go's cover tool for ddtest's private -toolexec
// entrypoint. Unlike the compiler, cover opens logical source paths directly, so
// overlay-backed inputs are translated to their backing files. Compiler and
// linker identities stay native; cover's identity includes our contract.
func RunCoverTool(ctx context.Context, overlay string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR: missing cover tool executable")
		return 2
	}
	if len(args) == 2 && args[1] == "-V=full" {
		return runCoverVersion(ctx, args, stdin, stdout, stderr)
	}
	args, err := translateCoverInputs(overlay, args)
	if err == nil {
		args, err = chainUserToolexec(args)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
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

// Bump this whenever cover translation semantics change without changing the
// rewritten client sources. Otherwise Go could reuse stale coverage objects.
const coverContractVersion = "ddtest-cover-v1"

func coverFingerprint() string {
	hash := sha256.Sum256([]byte(coverContractVersion))
	return fmt.Sprintf("%x", hash)
}

func appendCoverIdentity(native, fingerprint string) string {
	native = strings.TrimSpace(native)
	if strings.Contains(native, " buildID=") {
		// Development toolchains use only the last buildID content component.
		return native + "-ddtest-cover-" + fingerprint + "\n"
	}
	return native + " ddtest-cover=" + fingerprint + "\n"
}

func runCoverVersion(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	args, err := chainUserToolexec(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var native bytes.Buffer
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &native, stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if _, err := io.WriteString(stdout, appendCoverIdentity(native.String(), coverFingerprint())); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

// translateCoverInputs replaces overlaid Go inputs with their backing files and
// keeps every other argument, including cover's input/output ordering, intact.
func translateCoverInputs(overlay string, args []string) ([]string, error) {
	data, err := os.ReadFile(overlay)
	if err != nil {
		return nil, err
	}
	var plan Overlay
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	result := append([]string(nil), args...)
	for i := 1; i < len(result); i++ {
		if strings.HasPrefix(result[i], "-") || !strings.HasSuffix(result[i], ".go") {
			continue
		}
		logical, err := filepath.Abs(result[i])
		if err != nil {
			return nil, err
		}
		if actual := plan.Replace[logical]; actual != "" {
			result[i] = actual
		}
	}
	return result, nil
}

func toolCommand(executable, overlay, mode string) (string, error) {
	quote := func(s string) (string, error) {
		// cmd/go's quoted.Split preserves backslashes; strconv.Quote would double
		// Windows separators. Choose a delimiter absent from the path instead.
		if !strings.Contains(s, "'") {
			return "'" + s + "'", nil
		}
		if !strings.Contains(s, `"`) {
			return `"` + s + `"`, nil
		}
		return "", fmt.Errorf("cannot quote cover tool path containing both quote characters: %s", s)
	}
	exe, err := quote(executable)
	if err != nil {
		return "", err
	}
	path, err := quote(overlay)
	if err != nil {
		return "", err
	}
	return exe + " tool-overlay " + mode + " " + path, nil
}

// Match only the packages whose regular sources changed. Test files are not
// covered. Without -coverpkg, Go covers the requested test packages only.
func needsCoverOverlay(dir string, opts options, targets, rewritten []goPackage) bool {
	if !opts.coverage {
		return false
	}
	for i := range rewritten {
		p := &rewritten[i]
		if len(opts.coverPatterns) == 0 {
			for _, target := range targets {
				if target.ImportPath == p.ImportPath && target.ImportPath != "testing" && target.ImportPath != sdkPackage && target.ImportPath != miniPackage {
					return true
				}
			}
			continue
		}
		for _, pattern := range opts.coverPatterns {
			if matchPackagePattern(pattern, dir, p) {
				return true
			}
		}
	}
	return false
}
