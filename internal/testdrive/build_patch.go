package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// Apply only literal substitutions. Sed regexes/backreferences need separate
// review rather than a platform-dependent shell invocation or guessed syntax.
func applyLiteralPatch(root, directory string, words []string) error {
	if len(words) == 3 && words[0] == "cp" {
		return copyLiteralFile(root, directory, words[1], words[2])
	}
	if len(words) != 4 || words[0] != "sed" || !strings.HasPrefix(words[1], "-i") {
		return fmt.Errorf("patch needs literal substitution review")
	}
	script := words[2]
	firstLine := strings.HasPrefix(script, "1s")
	if firstLine {
		script = script[1:]
	}
	if len(script) < 4 || script[0] != 's' {
		return fmt.Errorf("patch needs literal substitution review")
	}
	delimiter := script[1]
	if !strings.ContainsRune("/#|", rune(delimiter)) {
		return fmt.Errorf("patch delimiter needs review")
	}
	var parts []string
	var part strings.Builder
	for i := 2; i < len(script); i++ {
		ch := script[i]
		if ch == '\\' {
			i++
			if i >= len(script) {
				return fmt.Errorf("patch escape needs review")
			}
			ch = script[i]
			if ch != delimiter && !strings.ContainsRune(`.[]^$*\&`, rune(ch)) {
				return fmt.Errorf("patch escape needs review")
			}
			part.WriteByte(ch)
			continue
		}
		if ch == delimiter {
			parts = append(parts, part.String())
			part.Reset()
			if len(parts) == 2 {
				parts = append(parts, script[i+1:])
				break
			}
			continue
		}
		if len(parts) == 0 && strings.ContainsRune(".[]^$*", rune(ch)) {
			return fmt.Errorf("patch regular expression requires separate review")
		}
		if len(parts) == 1 && ch == '&' {
			part.WriteString(parts[0])
			continue
		}
		part.WriteByte(ch)
	}
	if len(parts) != 3 || parts[0] == "" || (parts[2] != "" && parts[2] != "g") {
		return fmt.Errorf("patch needs literal substitution review")
	}
	if !filepath.IsLocal(words[3]) {
		return fmt.Errorf("patch target must stay in the configuration copy")
	}
	matches, err := filepath.Glob(filepath.Join(directory, words[3]))
	if err != nil || len(matches) == 0 {
		return fmt.Errorf("patch prerequisite has no local matches: %s", words[3])
	}
	for _, path := range matches {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(workspace.CanonicalRoot(root), resolved)
		if err != nil || !filepath.IsLocal(relative) {
			return fmt.Errorf("patch target escapes configuration copy: %s", words[3])
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("patch target is not a regular file: %s", words[3])
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		lines := strings.SplitAfter(string(data), "\n")
		count := 1
		if parts[2] == "g" {
			count = -1
		}
		for i, line := range lines {
			if firstLine && i > 0 {
				break
			}
			lines[i] = strings.Replace(line, parts[0], parts[1], count)
		}
		if err := os.WriteFile(resolved, []byte(strings.Join(lines, "")), info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// copyLiteralFile replays cp SOURCE TARGET between files of the
// configuration copy, such as a CI step creating config.json from its
// example.
func copyLiteralFile(root, directory, source, target string) error {
	canonical := workspace.CanonicalRoot(root)
	inside := func(path string) (string, error) {
		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("copy path must stay in the configuration copy: %s", path)
		}
		full := filepath.Join(directory, path)
		parent, err := filepath.EvalSymlinks(filepath.Dir(full))
		if err != nil {
			return "", err
		}
		if relative, err := filepath.Rel(canonical, parent); err != nil || !filepath.IsLocal(relative) && relative != "." {
			return "", fmt.Errorf("copy path escapes configuration copy: %s", path)
		}
		return filepath.Join(parent, filepath.Base(full)), nil
	}
	from, err := inside(source)
	if err != nil {
		return err
	}
	to, err := inside(target)
	if err != nil {
		return err
	}
	info, err := os.Lstat(from)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("copy source is not a regular file: %s", source)
	}
	if existing, err := os.Lstat(to); err == nil && !existing.Mode().IsRegular() {
		return fmt.Errorf("copy target is not a regular file: %s", target)
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, info.Mode().Perm())
}
