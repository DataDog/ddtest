package runner

import (
	"fmt"
	"os"
	"strings"
)

// packageCompilerCacheFlag changes only this package's cache key, preserving
// the last matching user gcflags. Its wrapper removes the marker before compile.
func packageCompilerCacheFlag(dir string, opts options, pkg *goPackage, marker string) (string, error) {
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
		// Package arguments also accept absolute directories. They have already
		// been resolved by Go; per-package flag patterns have different rules.
		match := pkg.commandLine
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
	effective = append(effective, marker)
	quoted := make([]string, len(effective))
	for i, flag := range effective {
		if strings.ContainsAny(flag, " \t\r\n") {
			if !strings.Contains(flag, "'") {
				flag = "'" + flag + "'"
			} else if !strings.Contains(flag, "\"") {
				flag = "\"" + flag + "\""
			} else {
				return "", fmt.Errorf("cannot quote compiler flag")
			}
		}
		quoted[i] = flag
	}
	return "-gcflags=" + pkg.ImportPath + "=" + strings.Join(quoted, " "), nil
}
