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
	version := ""
	if pkg.Module != nil {
		version = pkg.Module.Version
		if pkg.Module.Replace != nil && pkg.Module.Replace.Version != "" {
			version = pkg.Module.Replace.Version
		}
	}
	reason := "requires v1.3.0 or a later v1 release"
	entry, err := (*LibraryEntry)(nil), error(nil)
	if supportedGoleakVersion(version) {
		entry, err = prepareGoleakEntry(pkg, replacements, temp)
		if errors.Is(err, instrument.ErrUnsupportedAPI) {
			reason, err = err.Error(), nil
		}
	}
	if entry == nil && err == nil {
		if version == "" {
			version = "(unknown version)"
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
// marker as Testify does. A package-scoped, unused import-search directory keys
// this transform without changing the compiler identity for other packages.
// Go supplies every import through -importcfg; the sentinel is never read.
func goleakCacheFlag(dir string, opts options, pkg *goPackage, fingerprint string) (string, error) {
	flags, err := splitFlags(os.Getenv("GOFLAGS"))
	if err != nil {
		return "", err
	}
	flags = append(flags, opts.buildFlags...)
	var effective []string
	for i := 0; i < len(flags); i++ {
		name, value, assigned := strings.Cut(strings.TrimLeft(flags[i], "-"), "=")
		if name != "gcflags" {
			continue
		}
		if !assigned {
			i++
			if i == len(flags) {
				return "", fmt.Errorf("missing gcflags value")
			}
			value = flags[i]
		}
		value = strings.TrimSpace(value)
		match := false // unqualified flags apply only to command-line packages
		for _, selected := range opts.packages {
			match = match || matchPackagePattern(selected, dir, pkg)
		}
		if value != "" && !strings.HasPrefix(value, "-") {
			pattern, rest, ok := strings.Cut(value, "=")
			if !ok {
				return "", fmt.Errorf("invalid gcflags pattern")
			}
			match = matchPackagePattern(strings.TrimSpace(pattern), dir, pkg)
			value = rest
		}
		if match {
			effective, err = splitFlags(value)
			if err != nil {
				return "", err
			}
		}
	}
	effective = append(effective, "-I=ddtest-goleak-"+fingerprint)
	quoted := make([]string, len(effective))
	for i, flag := range effective {
		if strings.ContainsAny(flag, " \t\r\n") {
			if !strings.Contains(flag, "'") {
				flag = "'" + flag + "'"
			} else if !strings.Contains(flag, "\"") {
				flag = "\"" + flag + "\""
			} else {
				return "", fmt.Errorf("cannot quote goleak compiler flag")
			}
		}
		quoted[i] = flag
	}
	return "-gcflags=" + instrument.GoleakImport + "=" + strings.Join(quoted, " "), nil
}
