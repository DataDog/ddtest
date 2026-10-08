package runner

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// prepareGoleak returns a warning instead of an entry when the selected goleak
// is unsupported; its checks then run without the CI worker filters.
func prepareGoleak(pkg *goPackage, replacements map[string]string, temp string) (*LibraryEntry, string, error) {
	if pkg == nil {
		return nil, "", nil
	}
	version := libraryVersion(pkg)
	reason := "requires v1.3.0 or a later v1 release"
	entry, err := (*LibraryEntry)(nil), error(nil)
	if supportedGoleakVersion(version) {
		entry, err = prepareGoleakEntry(pkg, replacements, temp)
		if errors.Is(err, instrument.ErrUnsupportedAPI) {
			reason, err = err.Error(), nil
		}
	}
	if entry == nil && err == nil {
		if pkg.Module != nil && pkg.Module.Replace != nil {
			replacement := pkg.Module.Replace
			if replacement.Path != "" && replacement.Path != pkg.Module.Path {
				reason += "; replacement " + replacement.Path
				if replacement.Version != "" {
					reason += " " + replacement.Version
				}
			}
		}
		return nil, fmt.Sprintf("goleak %s is not instrumented (%s); its leak checks run without CI goroutine filters and can report CI workers", version, reason), nil
	}
	return entry, "", err
}

func prepareGoleakEntry(pkg *goPackage, replacements map[string]string, temp string) (*LibraryEntry, error) {
	tool := &LibraryEntry{Package: instrument.GoleakImport, Sources: map[string]string{}, HookFile: filepath.Join(temp, "goleak-hook.go")}
	hash := sha256.New()
	hash.Write([]byte("ddtest-goleak-find-v1"))
	hook := instrument.GoleakEntryHook()
	hash.Write([]byte(hook))
	names := append([]string(nil), pkg.GoFiles...)
	sort.Strings(names)
	for _, name := range names {
		logical := filepath.Join(pkg.Dir, name)
		actual := logical
		if replacement, ok := replacements[logical]; ok {
			actual = replacement
		}
		if actual == "" {
			continue
		}
		src, err := os.ReadFile(actual)
		if err != nil {
			return nil, err
		}
		out, found, err := instrument.TransformGoleakEntry(logical, src)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		backing := filepath.Join(temp, "goleak-entry.go")
		if err := os.WriteFile(backing, out, 0600); err != nil {
			return nil, err
		}
		tool.Sources[logical], tool.Sources[actual] = backing, backing
		hash.Write(out)
	}
	if len(tool.Sources) == 0 {
		return nil, fmt.Errorf("%w: goleak.Find entry was not found", instrument.ErrUnsupportedAPI)
	}
	if err := os.WriteFile(tool.HookFile, []byte(hook), 0600); err != nil {
		return nil, err
	}
	tool.Fingerprint = fmt.Sprintf("%x", hash.Sum(nil))
	return tool, nil
}

func supportedGoleakVersion(version string) bool {
	var minor, patch int
	if _, err := fmt.Sscanf(strings.TrimPrefix(version, "v1."), "%d.%d", &minor, &patch); err != nil || !strings.HasPrefix(version, "v1.") {
		return false
	}
	return minor > 3 || minor == 3 && (patch > 0 || !strings.Contains(version, "-"))
}

// Goleak does not import testing, so its cache key cannot inherit our testing
// marker as Testify does. A package-scoped gcflags marker keys this transform
// without changing other packages. The tool wrapper removes our exact marker
// before invoking the compiler; user include paths remain intact.
// Go supplies every import through -importcfg; the sentinel is never read.
func goleakCacheFlag(dir string, opts options, pkg *goPackage, fingerprint string) (string, error) {
	return packageCompilerCacheFlag(dir, opts, pkg, "-I=ddtest-goleak-"+fingerprint)
}

// removeGoleakCacheMarker filters an owned argument slice. Go has already
// included the marker in the cache key; it is not a compiler include path.
func removeGoleakCacheMarker(args []string, fingerprint string) []string {
	return removeCompilerCacheMarker(args, "-I=ddtest-goleak-"+fingerprint)
}

// removeCompilerCacheMarker filters an owned argument slice.
func removeCompilerCacheMarker(args []string, marker string) []string {
	kept := args[:0]
	for _, arg := range args {
		if arg != marker {
			kept = append(kept, arg)
		}
	}
	return kept
}
