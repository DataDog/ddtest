package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	goversion "go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	pocversion "github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

const miniModule = "github.com/tonyredondo/dd-ci-testing-poc"

// preprovideMini avoids a failed package-graph query when Mini is absent. A
// literal mention of Mini keeps the ordinary one-query path. The module probe
// is necessary: an indirect dependency or workspace can already provide it.
// Uncertain cases use normal package resolution, including subdirectory runs.
func preprovideMini(ctx context.Context, dir string, opts options, temp string, replacements map[string]string, progress io.Writer) (string, error) {
	if opts.mod == "vendor" {
		return "", nil
	}
	if opts.mod == "" {
		vendor := filepath.Join(dir, "vendor", "modules.txt")
		if _, overlaid := replacements[vendor]; overlaid {
			return "", nil
		}
		if _, err := os.Stat(vendor); !errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
	}
	source := opts.modfile
	if source == "" {
		source = filepath.Join(dir, "go.mod")
	} else if !filepath.IsAbs(source) {
		source = filepath.Join(dir, source)
	}
	data, err := readModuleFile(source, replacements)
	if err != nil || bytes.Contains(data, []byte(miniModule)) || bytes.ContainsRune(data, '\\') {
		return "", nil
	}
	// -mod=readonly still permits Go to write checksums. Probe against a
	// temporary copy so discovering a transitive runtime cannot edit the client.
	probe := filepath.Join(temp, "runtime-probe.mod")
	if err := os.WriteFile(probe, data, 0600); err != nil {
		return "", err
	}
	if err := copyModuleFile(strings.TrimSuffix(source, ".mod")+".sum", filepath.Join(temp, "runtime-probe.sum"), replacements); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	phase := debugFromContext(ctx).start("resolve runtime module")
	args := append([]string{"list", "-m", "-e", "-json=Path,Error"}, opts.buildFlags...)
	args = append(args, "-mod=readonly", "-modfile="+probe, miniModule)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	phase.finish(err)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", nil
	}
	var module struct {
		Path  string
		Error *struct{ Err string }
	}
	if json.Unmarshal(output, &module) != nil || module.Path != miniModule || module.Error == nil || !strings.Contains(module.Error.Err, "not a known dependency") {
		return "", nil
	}
	return provideRuntime(ctx, dir, opts, Mini, temp, replacements, progress)
}

// provideRuntime makes the selected runtime resolvable when the module does not
// require it, for example after go mod tidy removed an unused requirement. Go
// then reads a temporary copy of go.mod and go.sum through -modfile; the
// module's own files are never modified. It returns that copy's path.
func provideRuntime(ctx context.Context, dir string, opts options, selected Runtime, temp string, replacements map[string]string, progress io.Writer) (result string, err error) {
	logger := debugFromContext(ctx)
	phase := logger.start("provide runtime")
	defer func() { phase.finish(err) }()
	environment := opts.environment
	if environment == nil {
		environment, err = readGoEnvironment(ctx, dir)
		if err != nil {
			return "", err
		}
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
		if err := goGetRuntime(ctx, dir, modfile, sdkPackage+"@"+SDKVersion, progress); err != nil {
			return "", fmt.Errorf("provide dd-trace-go %s: %w", SDKVersion, err)
		}
		return modfile, nil
	}
	var version string
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == miniModule && publishedVersion(info.Main.Version) {
		version = info.Main.Version
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	root := miniSourceRoot(sourceFile, environment.GOMODCACHE, version)
	// Local replacements are relative to the module root, even when the user
	// runs ddtest from a subpackage or supplies a separate -modfile.
	return modfile, requireMini(ctx, filepath.Dir(environment.GOMOD), modfile, root, version, progress)
}

// requireMini respects the client's replacement before using the CLI's local
// sources or fetching the exact version that built it.
func requireMini(ctx context.Context, dir, modfile, localRoot, version string, progress io.Writer) error {
	out, err := goTool(ctx, dir, nil, "mod", "edit", "-json", "-modfile="+modfile)
	if err != nil {
		return err
	}
	var parsed struct {
		Go      string
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
		debugFromContext(ctx).printf("mini source=client-replacement local=%t", r.New.Version == "")
		if r.New.Version != "" {
			// A module replacement needs checksums, which go get records.
			err = goGetRuntime(ctx, dir, modfile, miniModule+"@"+version, progress)
		} else {
			err = requireLocalMini(ctx, dir, modfile, r.New.Path, version, parsed.Go, false)
		}
		return err
	}
	if localRoot != "" {
		debugFromContext(ctx).printf("mini source=cli-local")
		return requireLocalMini(ctx, dir, modfile, localRoot, "v0.0.0", parsed.Go, true)
	}
	if version != "" {
		debugFromContext(ctx).printf("mini source=published-version")
		return goGetRuntime(ctx, dir, modfile, miniModule+"@"+version, progress)
	}
	return fmt.Errorf("cannot provide %s: ddtest has no available local sources or published version; require it in the module or add a replace directive", miniModule)
}

// requireLocalMini keeps the consumer's source language unchanged. Our module
// supports Go 1.25; older client languages use a temporary workspace instead.
func requireLocalMini(ctx context.Context, dir, modfile, root, selectedVersion, goVersion string, addReplace bool) error {
	path := root
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(filepath.Join(path, "go.mod"))
	if err != nil {
		return fmt.Errorf("read local Mini module: %w", err)
	}
	args := []string{"mod", "edit", "-modfile=" + modfile, "-require=" + miniModule + "@" + selectedVersion}
	if required := moduleDirective(data, "go"); required != "" && goversion.Compare("go"+goVersion, "go"+required) < 0 {
		return fmt.Errorf("selected Mini requires module Go %s; ddtest will not change consumer language Go %s: use a workspace with the selected runtime", required, goVersion)
	}
	if addReplace {
		args = append(args, "-replace="+miniModule+"="+root)
	}
	_, err = goTool(ctx, dir, nil, args...)
	return err
}

// publishedVersion excludes development builds, whose version cannot be fetched.
func publishedVersion(version string) bool {
	return strings.HasPrefix(version, "v") && !strings.Contains(version, "+")
}

// miniSourceRoot finds the sources recorded in the CLI, or its exact version
// in GOMODCACHE when -trimpath removed the absolute source path. It never picks
// a different cached version or scans unrelated checkouts.
func miniSourceRoot(sourceFile, moduleCache, version string) string {
	if filepath.IsAbs(sourceFile) {
		root := filepath.Dir(filepath.Dir(filepath.Dir(sourceFile))) // internal/runner/provide.go
		if validMiniSource(root) {
			return root
		}
	}
	if moduleCache != "" && publishedVersion(version) {
		root := filepath.Join(moduleCache, miniModule+"@"+version)
		if validMiniSource(root) {
			return root
		}
	}
	return ""
}

func validMiniSource(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || modulePath(data) != miniModule {
		return false
	}
	info, err := os.Stat(filepath.Join(root, "testopt"))
	return err == nil && info.IsDir()
}

// modulePath returns the path in go.mod's module directive. Fields also drop
// the carriage returns of a Windows checkout.
func modulePath(data []byte) string {
	return moduleDirective(data, "module")
}

func moduleDirective(data []byte, directive string) string {
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == directive {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// goTool runs a go command for preparation. GOFLAGS is cleared: flags meant
// for the user's build, such as -mod=vendor, do not apply to module edits.
// A non-nil progress writer receives both streams as they arrive; otherwise
// stdout is returned and stderr is included in errors.
func goTool(ctx context.Context, dir string, progress io.Writer, args ...string) (output string, err error) {
	// Only the known operation is logged; arguments may contain private URLs.
	var phase debugPhase
	if logger := debugFromContext(ctx); logger != nil {
		phase = logger.start("go " + args[0])
	}
	defer func() { phase.finish(err) }()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOFLAGS=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if progress != nil {
		// Use the same writer for both streams: os/exec serializes writes even
		// when the caller supplies a buffer rather than an *os.File.
		cmd.Stdout, cmd.Stderr = progress, progress
	}
	if err := cmd.Run(); err != nil {
		if progress != nil {
			// Diagnostics have already been delivered; do not print them twice.
			return "", fmt.Errorf("go %s: %w", args[0], err)
		}
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func goGetRuntime(ctx context.Context, dir, modfile, query string, progress io.Writer) error {
	if progress != nil {
		fmt.Fprintf(progress, pocversion.BuildLogPrefix+" INFO: preparing runtime with go get %s\n", query)
	}
	_, err := goTool(ctx, dir, progress, "get", "-modfile="+modfile, query)
	return err
}

// readModuleFile reads the same logical file as Go, including overlay replacement
// or deletion. A copied -modfile no longer has the original overlay path.
func readModuleFile(from string, replacements map[string]string) ([]byte, error) {
	if actual, replaced := replacements[filepath.Clean(from)]; replaced {
		if actual == "" {
			return nil, &os.PathError{Op: "open", Path: from, Err: os.ErrNotExist}
		}
		from = actual
	}
	return os.ReadFile(from)
}

func copyModuleFile(from, to string, replacements map[string]string) error {
	data, err := readModuleFile(from, replacements)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0600)
}
