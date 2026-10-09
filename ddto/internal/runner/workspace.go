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
	"strconv"
	"strings"
	"time"
)

// workspaceFile follows Go's workspace discovery without changing process-wide
// environment. Each child command receives the task's temporary GOWORK value.
func workspaceFile(dir string) string {
	if work := os.Getenv("GOWORK"); work != "" && work != "auto" {
		if work == "off" {
			return ""
		}
		return work
	}
	for {
		path := filepath.Join(dir, "go.work")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

type goDebugSetting struct{ Key, Value string }

// formattedGoDebug reads only Go's canonical edit -print output: one setting per
// line, in a single directive or a block. Quoted settings keep their values.
func formattedGoDebug(text string) ([]goDebugSetting, error) {
	var settings []goDebugSetting
	block := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "godebug "); ok {
			line = strings.TrimSpace(rest)
			if line == "(" {
				block = true
				continue
			}
		} else if !block {
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if fields[0] == ")" {
			block = false
			continue
		}
		value := fields[0]
		if strings.HasPrefix(line, `"`) || strings.HasPrefix(line, "`") {
			quoted, err := strconv.QuotedPrefix(line)
			if err != nil {
				return nil, fmt.Errorf("read formatted GODEBUG setting: %w", err)
			}
			value, err = strconv.Unquote(quoted)
			if err != nil {
				return nil, err
			}
		}
		key, value, ok := strings.Cut(value, "=")
		if ok {
			settings = append(settings, goDebugSetting{key, value})
		}
	}
	return settings, nil
}

// provideOlderModuleWorkspace keeps an older module's language and runtime
// defaults while making Mini available as a separate workspace main module.
func provideOlderModuleWorkspace(ctx context.Context, dir string, opts *options, plan *Plan, replacements map[string]string) error {
	if opts.environment.GOMOD == "" || opts.environment.GOMOD == os.DevNull {
		return nil
	}
	root := filepath.Dir(opts.environment.GOMOD)
	source := opts.modfile
	if source == "" {
		source = filepath.Join(root, "go.mod")
	} else if !filepath.IsAbs(source) {
		source = filepath.Join(dir, source)
	}
	data, err := readModuleFile(source, replacements)
	if err != nil || compareGoVersion(moduleDirective(data, "go"), "1.25.0") >= 0 {
		return err
	}
	if opts.mod == "mod" {
		// Native -mod=mod may resolve missing client requirements. Do that
		// before adding Mini, so only the client's imports can change its files.
		args := append([]string{"list", "-e", "-test", "-json=ImportPath,Error"}, opts.buildFlags...)
		args = append(args, opts.packages...)
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		if _, err := readPackages(ctx, cmd, "resolve client requirements"); err != nil {
			return err
		}
		data, err = readModuleFile(source, replacements)
		if err != nil || compareGoVersion(moduleDirective(data, "go"), "1.25.0") >= 0 {
			return err
		}
	}
	if opts.modfile != "" {
		if err := overlaySelectedModfile(source, root, plan.Dir, data, plan, replacements); err != nil {
			return err
		}
	}
	input, err := moduleWorkspaceInput(ctx, dir, root, plan.Dir, replacements)
	if err != nil {
		return err
	}
	plan.Workfile, err = provideMiniWorkspace(ctx, dir, input, plan.Dir, replacements)
	if err != nil {
		return err
	}
	opts.workfile = plan.Workfile
	return useModuleWorkspaceFlags(opts, plan)
}

// overlaySelectedModfile supplies the caller's -modfile, and its checksum file,
// as the module's go.mod and go.sum: workspace mode rejects -modfile itself.
func overlaySelectedModfile(source, root, temp string, data []byte, plan *Plan, replacements map[string]string) error {
	backing := filepath.Join(temp, "client-input.mod")
	if err := os.WriteFile(backing, data, 0600); err != nil {
		return err
	}
	replacements[filepath.Join(root, "go.mod")] = backing
	backingSum := filepath.Join(temp, "client-input.sum")
	if err := copyModuleFile(strings.TrimSuffix(source, ".mod")+".sum", backingSum, replacements); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		replacements[filepath.Join(root, "go.sum")] = ""
	} else {
		replacements[filepath.Join(root, "go.sum")] = backingSum
	}
	plan.modfileOverlay = true
	return nil
}

// useModuleWorkspaceFlags adapts the caller's module-mode flags, from both the
// command line and GOFLAGS, for a temporary workspace.
func useModuleWorkspaceFlags(opts *options, plan *Plan) error {
	flags, err := splitFlags(os.Getenv("GOFLAGS"))
	if err != nil {
		return err
	}
	if plan.workspaceGoFlags, err = quoteToolWords(workspaceModuleFlags(flags)); err != nil {
		return err
	}
	plan.moduleWorkspace = true
	opts.buildFlags = workspaceModuleFlags(opts.buildFlags)
	opts.workspaceGoFlags = &plan.workspaceGoFlags
	return nil
}

// workspaceReplacedModules lists the module versions that a go.work file
// replaces; an empty version replaces every version of that module.
func workspaceReplacedModules(ctx context.Context, dir, work string) (map[moduleVersion]bool, error) {
	out, err := goTool(ctx, dir, nil, "work", "edit", "-json", work)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Replace []struct {
			Old struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("parse workspace edit JSON (%d bytes): %w", len(out), err)
	}
	replaced := map[moduleVersion]bool{}
	for _, replace := range parsed.Replace {
		replaced[moduleVersion{replace.Old.Path, replace.Old.Version}] = true
	}
	return replaced, nil
}

// workspaceModuleFlags adapts only flags that Go forbids in workspace mode.
// The selected modfile is supplied by the overlay, and workspace loading owns
// its checksum file. Other build flags retain their original order and values.
func workspaceModuleFlags(flags []string) []string {
	result := make([]string, 0, len(flags))
	for i := 0; i < len(flags); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(flags[i], "-"), "=")
		if name == "modfile" {
			if !hasValue && i+1 < len(flags) {
				i++
			}
			continue
		}
		if name == "mod" {
			if !hasValue && i+1 < len(flags) {
				value = flags[i+1]
				if value == "mod" {
					result = append(result, flags[i], "readonly")
					i++
					continue
				}
			} else if value == "mod" {
				result = append(result, "-mod=readonly")
				continue
			}
		}
		result = append(result, flags[i])
	}
	return result
}

// provideMiniWorkspace preserves every use/replace directive and each module's
// own language version. Mini is a main module in the temporary workspace, so
// importing it adds no requirements to any client module.
func provideMiniWorkspace(ctx context.Context, dir, work, temp string, replacements map[string]string) (string, error) {
	if !filepath.IsAbs(work) {
		return "", fmt.Errorf("GOWORK must name an absolute path: %s", work)
	}
	data, err := readModuleFile(work, replacements)
	if err != nil {
		return "", err
	}
	target := filepath.Join(temp, "go.work")
	if err := os.WriteFile(target, data, 0600); err != nil {
		return "", err
	}
	if err := copyModuleFile(work+".sum", target+".sum", replacements); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	out, err := goTool(ctx, dir, nil, "work", "edit", "-json", target)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Go      string
		GoDebug []goDebugSetting
		Use     []struct{ DiskPath string }
		Replace []struct {
			Old, New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return "", fmt.Errorf("parse workspace edit JSON (%d bytes): %w", len(out), err)
	}
	// Go 1.25 accepts GODEBUG directives but omits them from edit -json.
	// The formatted output supplies those settings without parsing module syntax.
	if len(parsed.GoDebug) == 0 && bytes.Contains(data, []byte("godebug")) {
		printed, err := goTool(ctx, dir, nil, "work", "edit", "-print", target)
		if err != nil {
			return "", err
		}
		parsed.GoDebug, err = formattedGoDebug(printed)
		if err != nil {
			return "", err
		}
	}
	base := filepath.Dir(work)
	args := []string{"work", "edit"}
	if parsed.Go == "" {
		args = append(args, "-go=1.21")
	}
	for _, use := range parsed.Use {
		if !filepath.IsAbs(use.DiskPath) {
			args = append(args, "-dropuse="+use.DiskPath, "-use="+filepath.Join(base, use.DiskPath))
		}
	}
	for _, replace := range parsed.Replace {
		if replace.New.Version == "" && !filepath.IsAbs(replace.New.Path) {
			old := replace.Old.Path
			if replace.Old.Version != "" {
				old += "@" + replace.Old.Version
			}
			args = append(args, "-replace="+old+"="+filepath.Join(base, replace.New.Path))
		}
	}

	// Use the effective local Mini sources as a main module. A carrier requiring
	// Mini would force Go to load unrelated dependency versions that a workspace
	// normally resolves directly from its main modules.
	// Resolve against the copied workspace before introducing Mini. This keeps
	// existing requirements, remote replacements and conflicting replacements
	// subject to Go's own module selection rules.
	if len(args) > 2 {
		if _, err := goTool(ctx, dir, nil, append(args, target)...); err != nil {
			return "", err
		}
	}
	miniRoot, err := selectedWorkspaceMini(ctx, dir, target, temp, replacements)
	if err != nil {
		return "", err
	}
	selected := miniRoot != ""
	workspaceReplacement := false
	for _, replace := range parsed.Replace {
		if replace.Old.Path != miniModule {
			continue
		}
		workspaceReplacement = true
		if !selected {
			miniRoot, err = workspaceReplacementRoot(ctx, dir, base, replace.New.Path, replace.New.Version)
			if err != nil {
				return "", err
			}
		}
		old := replace.Old.Path
		if replace.Old.Version != "" {
			old += "@" + replace.Old.Version
		}
		args = append(args, "-dropreplace="+old)
	}
	for i, use := range parsed.Use {
		root := use.DiskPath
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		moduleData, e := readModuleFile(filepath.Join(root, "go.mod"), replacements)
		if e != nil {
			return "", e
		}
		if modulePath(moduleData) == miniModule {
			miniRoot = root
			break
		}
		if workspaceReplacement || selected {
			continue
		}
		copy := filepath.Join(temp, fmt.Sprintf("client-%d.mod", i))
		if err := os.WriteFile(copy, moduleData, 0600); err != nil {
			return "", err
		}
		jsonData, e := goTool(ctx, root, nil, "mod", "edit", "-json", "-modfile="+copy)
		if e != nil {
			return "", e
		}
		var client struct {
			Replace []struct {
				Old, New struct{ Path, Version string }
			}
		}
		if err := json.Unmarshal([]byte(jsonData), &client); err != nil {
			return "", fmt.Errorf("parse client edit JSON (%d bytes): %w", len(jsonData), err)
		}
		for _, replace := range client.Replace {
			if replace.Old.Path != miniModule {
				continue
			}
			candidate, err := workspaceReplacementRoot(ctx, dir, root, replace.New.Path, replace.New.Version)
			if err != nil {
				return "", err
			}
			if miniRoot != "" && filepath.Clean(miniRoot) != filepath.Clean(candidate) {
				return "", fmt.Errorf("conflicting workspace replacements for %s", miniModule)
			}
			miniRoot = candidate
		}
	}
	if miniRoot == "" {
		var cliVersion string
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == miniModule && publishedVersion(info.Main.Version) {
			cliVersion = info.Main.Version
		}
		cache, err := goTool(ctx, dir, nil, "env", "GOMODCACHE")
		if err != nil {
			return "", err
		}
		_, source, _, _ := runtime.Caller(0)
		miniRoot = miniSourceRoot(source, strings.TrimSpace(cache), cliVersion)
		if miniRoot == "" && cliVersion != "" {
			miniRoot, err = workspaceReplacementRoot(ctx, dir, base, miniModule, cliVersion)
			if err != nil {
				return "", err
			}
		}
	}
	if !validMiniSource(miniRoot) {
		return "", fmt.Errorf("Mini sources unavailable for workspace provisioning")
	}
	miniData, err := readModuleFile(filepath.Join(miniRoot, "go.mod"), replacements)
	if err != nil {
		return "", err
	}
	nativeGo := parsed.Go
	if nativeGo == "" {
		nativeGo = defaultGoWorkVersion
	}
	if required := moduleDirective(miniData, "go"); compareGoVersion(required, nativeGo) > 0 {
		args = append(args, "-go="+required)
		// A workspace's go directive also controls program compatibility defaults.
		// Preserve those defaults if a selected runtime needs a newer workspace.
		hasDefault := false
		for _, setting := range parsed.GoDebug {
			hasDefault = hasDefault || setting.Key == "default"
		}
		if value := goDebugDefault(nativeGo); !hasDefault && value != "" {
			args = append(args, "-godebug=default="+value)
		}
	}
	args = append(args, "-use="+miniRoot, target)
	_, err = goTool(ctx, dir, nil, args...)
	return target, err
}

// selectedWorkspaceMini probes only the runtime module. Its checksum writes
// belong to the copied workspace; readonly protects the client module files.
func selectedWorkspaceMini(ctx context.Context, dir, work, temp string, replacements map[string]string) (result string, err error) {
	phase := debugFromContext(ctx).start("resolve workspace runtime")
	defer func() { phase.finish(err) }()
	args := []string{"list", "-m", "-e", "-json", "-mod=readonly"}
	if len(replacements) != 0 {
		data, err := json.Marshal(Overlay{Replace: replacements})
		if err != nil {
			return "", err
		}
		path := filepath.Join(temp, "workspace-probe-overlay.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return "", err
		}
		args = append(args, "-overlay="+path)
	}
	args = append(args, miniModule)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOWORK="+work, "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve workspace Mini: %w", err)
	}
	// With multiple workspace main modules, go list -m -e can omit an unknown
	// explicit module completely: exit 0, no JSON and no stderr. Provision it.
	if len(bytes.TrimSpace(out)) == 0 {
		return "", nil
	}
	var module struct {
		Path, Version, Dir string
		Replace            *struct{ Path, Version, Dir string }
		Error              *struct{ Err string }
	}
	if err := json.Unmarshal(out, &module); err != nil {
		return "", fmt.Errorf("parse workspace module JSON: %w", err)
	}
	if module.Error != nil {
		if strings.Contains(module.Error.Err, "not a known dependency") {
			return "", nil
		}
		return "", fmt.Errorf("resolve workspace Mini: %s", module.Error.Err)
	}
	if module.Replace != nil {
		if module.Replace.Dir != "" {
			return module.Replace.Dir, nil
		}
		return workspaceReplacementRoot(ctx, dir, dir, module.Replace.Path, module.Replace.Version)
	}
	if module.Dir != "" {
		return module.Dir, nil
	}
	return workspaceReplacementRoot(ctx, dir, dir, module.Path, module.Version)
}

func workspaceReplacementRoot(ctx context.Context, dir, base, path, version string) (string, error) {
	if version == "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		return path, nil
	}
	// Download outside all client modules and workspaces. Explicit module
	// queries populate only GOMODCACHE, without writing caller checksum files.
	scratch, err := os.MkdirTemp("", "ddto-module-download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "-json", path+"@"+version)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = scratch
	cmd.Env = append(cmd.Environ(), "GOWORK=off", "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	phase := debugFromContext(ctx).start("go mod download")
	out, err := cmd.Output()
	phase.finish(err)
	if err != nil {
		return "", fmt.Errorf("download workspace Mini: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var module struct{ Dir string }
	if err := json.Unmarshal(out, &module); err != nil {
		return "", fmt.Errorf("parse workspace module JSON (%d bytes): %w", len(out), err)
	}
	return module.Dir, nil
}
