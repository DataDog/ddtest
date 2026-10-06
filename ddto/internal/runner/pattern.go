package runner

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// matchPackagePattern reports whether p matches pattern as cmd/go matches the
// patterns of per-package build flags and -coverpkg. Relative patterns are
// resolved against cwd and never match outside that tree. Absolute paths are
// not directory patterns. Wildcards never cross a vendor directory unless the
// pattern names it. The semantics follow load.MatchPackage and
// pkgpattern.MatchPattern from cmd/go (Copyright The Go Authors, BSD-3-Clause).
func matchPackagePattern(pattern, cwd string, p *goPackage) bool {
	switch {
	case pattern == "." || pattern == ".." || strings.HasPrefix(pattern, "./") || strings.HasPrefix(pattern, "../"):
		dir, rest := pattern, ""
		if i := strings.Index(pattern, "..."); i >= 0 {
			j := strings.LastIndex(pattern[:i], "/")
			dir, rest = pattern[:j], pattern[j+1:]
		}
		dir = filepath.Join(cwd, dir)
		if rest == "" {
			return p.Dir == dir
		}
		rel, err := filepath.Rel(dir, p.Dir)
		if err != nil {
			return false // For example, different Windows volumes.
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return false
		}
		return matchImportPattern(rest, rel)
	case pattern == "all":
		return true
	case pattern == "std":
		return p.Standard
	case pattern == "cmd":
		return p.Standard && strings.HasPrefix(p.ImportPath, "cmd/")
	case pattern == "tool":
		// Only main-module tool packages match; instrumented libraries never do.
		return false
	case pattern == "work":
		return p.Module != nil && p.Module.Main
	default:
		return matchImportPattern(pattern, p.ImportPath)
	}
}

// matchImportPattern implements Go's "..." wildcard: a trailing "/..." also
// matches the empty suffix, and a wildcard element never matches a vendor
// element unless the pattern spells that vendor directory out.
func matchImportPattern(pattern, name string) bool {
	const vendor = "\x00"
	if strings.Contains(pattern, vendor) || strings.Contains(name, vendor) || !utf8.ValidString(pattern) {
		return false
	}
	re := replaceVendorElements(regexp.QuoteMeta(pattern), vendor)
	switch {
	case strings.HasSuffix(re, `/`+vendor+`/\.\.\.`):
		re = strings.TrimSuffix(re, `/`+vendor+`/\.\.\.`) + `(/vendor|/` + vendor + `/\.\.\.)`
	case re == vendor+`/\.\.\.`:
		re = `(/vendor|/` + vendor + `/\.\.\.)`
	}
	if strings.HasSuffix(re, `/\.\.\.`) {
		re = strings.TrimSuffix(re, `/\.\.\.`) + `(/\.\.\.)?`
	}
	re = strings.ReplaceAll(re, `\.\.\.`, `[^`+vendor+`]*`)
	return regexp.MustCompile(`^` + re + `$`).MatchString(replaceVendorElements(name, vendor))
}

// replaceVendorElements replaces every non-final "vendor" path element.
func replaceVendorElements(path, replacement string) string {
	if !strings.Contains(path, "vendor") {
		return path
	}
	elements := strings.Split(path, "/")
	for i := 0; i < len(elements)-1; i++ {
		if elements[i] == "vendor" {
			elements[i] = replacement
		}
	}
	return strings.Join(elements, "/")
}
