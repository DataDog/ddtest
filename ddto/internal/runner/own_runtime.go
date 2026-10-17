package runner

import (
	"context"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// ownRuntimeKey holds, for one invocation, the roots of the CLI's own runtime
// sources that cannot differ from the revision that built the CLI and that
// cliMiniRoot returned: the sources that a release build carries, extracted
// only by provisioning, or the CLI's exact version in GOMODCACHE. A checkout
// can hold local edits or another revision, so it is never one of them.
type ownRuntimeKey struct{}

type ownRuntimeRoots struct {
	mu    sync.Mutex
	roots []string
}

func withOwnRuntimeRoots(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownRuntimeKey{}, &ownRuntimeRoots{})
}

// noteOwnRuntimeRoot records root, unless it is empty or ctx records none.
func noteOwnRuntimeRoot(ctx context.Context, root string) {
	roots, _ := ctx.Value(ownRuntimeKey{}).(*ownRuntimeRoots)
	if roots == nil || root == "" {
		return
	}
	roots.mu.Lock()
	defer roots.mu.Unlock()
	roots.roots = append(roots.roots, filepath.Clean(root))
}

// cliModuleVersion returns the published version that built the CLI, such as
// one that go install fetched, or "".
func cliModuleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == miniModule && publishedVersion(info.Main.Version) {
		return info.Main.Version
	}
	return ""
}

// cliMiniRoots returns the CLI's exact version in moduleCache, when it
// exists. The module cache never changes a version's files, and a client's
// requirement can select it without provisioning.
func cliMiniRoots(moduleCache, version string) []string {
	if root := miniSourceRoot("", moduleCache, version); root != "" {
		return []string{filepath.Clean(root)}
	}
	return nil
}

// markOwnRuntime marks the listed packages of Mini's module whose module root
// is one of the CLI's own sources: a release's sources that provisioning used,
// or the CLI's exact version in moduleCache. It returns how many it marked.
// Another runtime that the client selects has another root, even inside the
// CLI's checkout, and stays unmarked.
func markOwnRuntime(ctx context.Context, listed []goPackage, moduleCache, version string) int {
	own := cliMiniRoots(moduleCache, version)
	if noted, _ := ctx.Value(ownRuntimeKey{}).(*ownRuntimeRoots); noted != nil {
		noted.mu.Lock()
		own = append(own, noted.roots...)
		noted.mu.Unlock()
	}
	marked := 0
	for i := range listed {
		p := &listed[i]
		if p.Module == nil || p.Module.Path != miniModule {
			continue
		}
		moduleRoot := p.Module.Dir
		if p.Module.Replace != nil && p.Module.Replace.Dir != "" {
			moduleRoot = p.Module.Replace.Dir
		}
		if moduleRoot == "" {
			continue
		}
		for _, root := range own {
			if filepath.Clean(moduleRoot) == root {
				p.ownRuntime = true
				marked++
				break
			}
		}
	}
	return marked
}

// ownSources reports whether p's files are the CLI's own runtime sources,
// which cannot differ from the revision that built the CLI, with no overlay
// entry in p's directory: none replaces, adds or deletes one of its files. The
// CLI's tests check those sources against every runtime check
// (TestMiniRuntimePassesEveryRuntimeCheck), so the checks need not read them
// again on every invocation. Some hooks only set Mini's variables, so a
// runtime without them would still link: the checks still run for a
// checkout, which can hold local edits or another revision.
func ownSources(p *goPackage, replacements map[string]string) bool {
	if !p.ownRuntime {
		return false
	}
	dir := filepath.Clean(p.Dir)
	for path := range replacements {
		if filepath.Dir(path) == dir {
			return false
		}
	}
	return true
}
