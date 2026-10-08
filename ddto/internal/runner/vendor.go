package runner

import (
	"context"
	"encoding/json"
	goversion "go/version"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func compareGoVersion(left, right string) int { return goversion.Compare("go"+left, "go"+right) }

func writeProvisionOverlay(temp string, opts *options, replacements map[string]string) error {
	data, err := json.Marshal(Overlay{Replace: replacements})
	if err != nil {
		return err
	}
	path := filepath.Join(temp, "provision-overlay.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	flags := opts.buildFlags[:0]
	for _, flag := range opts.buildFlags {
		if !strings.HasPrefix(flag, "-overlay=") {
			flags = append(flags, flag)
		}
	}
	opts.buildFlags = append(flags, "-overlay="+path)
	return nil
}

// moduleRoot follows Go's module discovery. A temporary workspace uses the
// original root, so imports, -C, relative replacements and package flags retain
// their meaning.
func moduleRoot(dir string) string {
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// moduleWorkspaceInput represents one module without changing its language or
// GODEBUG defaults. The caller may overlay a selected -modfile first.
func moduleWorkspaceInput(ctx context.Context, dir, root, temp string, replacements map[string]string) (string, error) {
	source := filepath.Join(temp, "module-input.work")
	module, err := readModuleFile(filepath.Join(root, "go.mod"), replacements)
	if err != nil {
		return "", err
	}
	modfile := filepath.Join(temp, "module-settings.mod")
	if err := os.WriteFile(modfile, module, 0600); err != nil {
		return "", err
	}
	out, err := goTool(ctx, dir, nil, "mod", "edit", "-print", "-modfile="+modfile)
	if err != nil {
		return "", err
	}
	settingsGo := moduleDirective([]byte(out), "go")
	settingsDebug, err := formattedGoDebug(out)
	if err != nil {
		return "", err
	}
	// Go compares workspace module roots with its native working-directory
	// spelling. Absolute slash-normalized paths fail that comparison on Windows.
	workspaceGo := settingsGo
	if compareGoVersion(workspaceGo, "1.25.0") < 0 {
		workspaceGo = "1.25.0"
	}
	data := []byte("go " + workspaceGo + "\nuse " + strconv.Quote(root) + "\n")
	hasDefault := false
	for _, setting := range settingsDebug {
		hasDefault = hasDefault || setting.Key == "default"
		data = append(data, []byte("godebug "+setting.Key+"="+setting.Value+"\n")...)
	}
	if !hasDefault && workspaceGo != settingsGo && settingsGo != "" {
		parts := strings.Split(settingsGo, ".")
		if len(parts) >= 2 {
			data = append(data, []byte("godebug default=go"+strings.Join(parts[:2], ".")+"\n")...)
		}
	}
	if err := os.WriteFile(source, data, 0600); err != nil {
		return "", err
	}
	return source, nil
}

func provideMiniVendorWorkspace(ctx context.Context, dir, root, temp string, replacements map[string]string) (string, error) {
	source, err := moduleWorkspaceInput(ctx, dir, root, temp, replacements)
	if err != nil {
		return "", err
	}
	work, err := provideMiniWorkspace(ctx, dir, source, temp, replacements)
	if err != nil {
		return "", err
	}
	if err := snapshotVendor(filepath.Join(root, "vendor"), filepath.Join(temp, "vendor"), replacements); err != nil {
		return "", err
	}
	return work, nil
}

// snapshotVendor preserves patched sources. Hard links avoid copying large
// vendor trees; files are never edited. A copy is used across filesystems or
// where hard links are unavailable. The modules.txt copy is always independent
// because Go reads that file outside its overlay filesystem.
func snapshotVendor(source, target string, replacements map[string]string) error {
	// Include virtual files added by an overlay, not just files visited on disk.
	for path, backing := range replacements {
		rel, err := filepath.Rel(source, path)
		if err == nil && rel != "modules.txt" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			replacements[filepath.Join(target, rel)] = backing
		}
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		logical := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(logical, 0700)
		}
		backing := path
		if replacement, ok := replacements[path]; ok {
			if replacement == "" {
				return nil
			}
			backing = replacement
			// Retarget the user's source overlay for the temporary vendor directory.
			if rel != "modules.txt" {
				replacements[logical] = replacement
			}
		}
		if rel == "modules.txt" {
			data, err := os.ReadFile(backing)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(string(data), "## workspace") {
				data = append([]byte("## workspace\n"), data...)
			}
			return os.WriteFile(logical, data, 0600)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(backing)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil // Go does not traverse symlink directories in package scans.
			}
			backing, err = filepath.EvalSymlinks(backing)
			if err != nil {
				return err
			}
		}
		if err := os.Link(backing, logical); err == nil {
			return nil
		}
		input, err := os.Open(backing)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(logical, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
