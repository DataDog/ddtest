package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

const miniModule = "github.com/tonyredondo/dd-ci-testing-poc"

// provideRuntime makes the selected runtime resolvable when the module does not
// require it, for example after go mod tidy removed an unused requirement. Go
// then reads a temporary copy of go.mod and go.sum through -modfile; the
// module's own files are never modified. It returns that copy's path.
func provideRuntime(ctx context.Context, dir string, opts options, selected Runtime, temp string, replacements map[string]string) (string, error) {
	out, err := goTool(ctx, dir, "env", "-json", "GOMOD", "GOWORK")
	if err != nil {
		return "", err
	}
	var environment struct{ GOMOD, GOWORK string }
	if err := json.Unmarshal([]byte(out), &environment); err != nil {
		return "", fmt.Errorf("read Go module environment: %w", err)
	}
	source := opts.modfile
	if source == "" {
		source = environment.GOMOD
		if source == "" || source == os.DevNull {
			return "", errors.New("run ddtest inside a Go module")
		}
	} else if !filepath.IsAbs(source) {
		source = filepath.Join(dir, source)
	}
	if environment.GOWORK != "" && environment.GOWORK != "off" {
		return "", errors.New("in workspace mode, add the runtime module to go.work or require it in the module")
	}
	vendored := filepath.Join(filepath.Dir(environment.GOMOD), "vendor", "modules.txt")
	if _, err := os.Stat(vendored); err == nil && opts.mod != "mod" && opts.mod != "readonly" {
		return "", errors.New("the module vendors its dependencies: require the runtime with a tools file and run go mod vendor")
	}
	modfile := filepath.Join(temp, "go.mod")
	if err := copyModuleFile(source, modfile, replacements); err != nil {
		return "", err
	}
	sum := strings.TrimSuffix(source, ".mod") + ".sum"
	if err := copyModuleFile(sum, filepath.Join(temp, "go.sum"), replacements); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if selected == SDK {
		// A package query also records checksums for its whole import graph.
		if _, err := goTool(ctx, dir, "get", "-modfile="+modfile, sdkPackage+"@"+SDKVersion); err != nil {
			return "", fmt.Errorf("provide dd-trace-go %s: %w", SDKVersion, err)
		}
		return modfile, nil
	}
	return modfile, requireMini(ctx, dir, modfile)
}

// requireMini prefers the module's own replace directive, then the published
// version that built ddtest, then the checkout ddtest was built from.
func requireMini(ctx context.Context, dir, modfile string) error {
	out, err := goTool(ctx, dir, "mod", "edit", "-json", "-modfile="+modfile)
	if err != nil {
		return err
	}
	var parsed struct {
		Replace []struct {
			Old, New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return err
	}
	for _, r := range parsed.Replace {
		if r.Old.Path != miniModule {
			continue
		}
		version := r.Old.Version
		if version == "" {
			version = "v0.0.0"
		}
		if r.New.Version != "" {
			// A module replacement needs checksums, which go get records.
			_, err = goTool(ctx, dir, "get", "-modfile="+modfile, miniModule+"@"+version)
		} else {
			_, err = goTool(ctx, dir, "mod", "edit", "-modfile="+modfile, "-require="+miniModule+"@"+version)
		}
		return err
	}
	var failures []string
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == miniModule && publishedVersion(info.Main.Version) {
		if _, err := goTool(ctx, dir, "get", "-modfile="+modfile, miniModule+"@"+info.Main.Version); err == nil {
			return nil
		} else {
			failures = append(failures, err.Error())
		}
	}
	if root := sourceCheckout(); root != "" {
		_, err := goTool(ctx, dir, "mod", "edit", "-modfile="+modfile, "-require="+miniModule+"@v0.0.0", "-replace="+miniModule+"="+root)
		return err
	}
	failures = append(failures, "ddtest was not built from a local checkout")
	return fmt.Errorf("cannot provide %s: %s; require it in the module or add a replace directive", miniModule, strings.Join(failures, "; "))
}

// publishedVersion excludes development builds, whose version cannot be fetched.
func publishedVersion(version string) bool {
	return strings.HasPrefix(version, "v") && !strings.Contains(version, "+")
}

// sourceCheckout returns the module root ddtest was compiled from, when the
// binary still records source paths (no -trimpath) and that checkout exists.
func sourceCheckout() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(file) {
		return ""
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file))) // internal/runner/provide.go
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || modulePath(data) != miniModule {
		return ""
	}
	return root
}

// modulePath returns the path in go.mod's module directive. Fields also drop
// the carriage returns of a Windows checkout.
func modulePath(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// goTool runs a go command for preparation. GOFLAGS is cleared: flags meant
// for the user's build, such as -mod=vendor, do not apply to module edits.
func goTool(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOFLAGS=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// copyModuleFile reads the same logical file as Go, including overlay replacement
// or deletion. The temporary -modfile no longer has the original overlay path.
func copyModuleFile(from, to string, replacements map[string]string) error {
	if actual, replaced := replacements[filepath.Clean(from)]; replaced {
		if actual == "" {
			return &os.PathError{Op: "open", Path: from, Err: os.ErrNotExist}
		}
		from = actual
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0600)
}
