package bazeldoctor

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
)

// The doctor runs these POSIX path rules; on Windows the Python doctor
// validates the outputs, since ntpath resolves paths differently.

// purePath is str(PurePosixPath(text)): duplicate separators and "."
// components dropped, no trailing separator, and "." for an empty path.
func purePath(text string) string {
	root, rest := "", text
	switch {
	case !strings.HasPrefix(text, "/"):
	case strings.HasPrefix(text, "//") && !strings.HasPrefix(text, "///"):
		root, rest = "//", text[2:]
	default:
		root, rest = "/", text[1:]
	}
	var parts []string
	for _, part := range strings.Split(rest, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	joined := root + strings.Join(parts, "/")
	if joined == "" {
		return "."
	}
	return joined
}

// joinPath is str(Path(base) / name).
func joinPath(base string, names ...string) string {
	result := base
	for _, name := range names {
		if strings.HasPrefix(name, "/") {
			result = name
		} else {
			result = result + "/" + name
		}
	}
	return purePath(result)
}

// pathName is Path(text).name.
func pathName(text string) string {
	return path.Base(purePath(text))
}

// pathParent is str(Path(text).parent).
func pathParent(text string) string {
	clean := purePath(text)
	if clean == "/" || clean == "//" || clean == "." {
		return clean
	}
	index := strings.LastIndex(clean, "/")
	switch {
	case index < 0:
		return "."
	case index == 0:
		return "/"
	case index == 1 && strings.HasPrefix(clean, "//"):
		return "//"
	}
	return clean[:index]
}

// pathParts is Path(text).parts, which Python compares when it sorts paths.
func pathParts(text string) []string {
	clean := purePath(text)
	var parts []string
	switch {
	case strings.HasPrefix(clean, "//") && !strings.HasPrefix(clean, "///"):
		parts, clean = []string{"//"}, clean[2:]
	case strings.HasPrefix(clean, "/"):
		parts, clean = []string{"/"}, clean[1:]
	}
	for _, part := range strings.Split(clean, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}

// lessPath is Path(a) < Path(b) on POSIX.
func lessPath(a, b string) bool {
	x, y := pathParts(a), pathParts(b)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// resolvePath is str(Path(text).resolve()): os.path.realpath, which keeps
// missing components, and Path.resolve's symlink-loop check, which raises
// and is left to the Python doctor.
func resolvePath(text string) string {
	cwd, err := os.Getwd()
	if err != nil {
		unsupported("working directory: " + err.Error())
	}
	resolved, ok := joinRealPath("", text, map[string]*string{})
	if !ok {
		unsupported("symlink loop at " + text)
	}
	if !strings.HasPrefix(resolved, "/") {
		resolved = cwd + "/" + resolved
	}
	resolved = normPath(resolved)
	if _, err := os.Stat(resolved); errors.Is(err, syscall.ELOOP) {
		unsupported("symlink loop at " + text)
	}
	return resolved
}

// joinRealPath is posixpath._joinrealpath with strict=False.
func joinRealPath(current, rest string, seen map[string]*string) (string, bool) {
	if strings.HasPrefix(rest, "/") {
		rest, current = rest[1:], "/"
	}
	for rest != "" {
		name, remainder, _ := strings.Cut(rest, "/")
		rest = remainder
		if name == "" || name == "." {
			continue
		}
		if name == ".." {
			if current != "" {
				head, tail := splitPath(current)
				current = head
				if tail == ".." {
					current = posixJoin(current, "..", "..")
				}
			} else {
				current = ".."
			}
			continue
		}
		next := posixJoin(current, name)
		info, err := os.Lstat(next)
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			current = next
			continue
		}
		if known, ok := seen[next]; ok {
			if known != nil {
				current = *known
				continue
			}
			return posixJoin(next, rest), false
		}
		seen[next] = nil
		target, err := os.Readlink(next)
		if err != nil {
			unsupported("readlink " + next + ": " + err.Error())
		}
		resolved, ok := joinRealPath(current, target, seen)
		if !ok {
			return posixJoin(resolved, rest), false
		}
		current = resolved
		seen[next] = &resolved
	}
	return current, true
}

// posixJoin is posixpath.join.
func posixJoin(base string, names ...string) string {
	result := base
	for _, name := range names {
		switch {
		case strings.HasPrefix(name, "/"):
			result = name
		case result == "" || strings.HasSuffix(result, "/"):
			result += name
		default:
			result += "/" + name
		}
	}
	return result
}

// splitPath is posixpath.split.
func splitPath(text string) (string, string) {
	index := strings.LastIndex(text, "/") + 1
	head, tail := text[:index], text[index:]
	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head, tail
}

// normPath is posixpath.normpath for an absolute path.
func normPath(text string) string {
	initial := 0
	if strings.HasPrefix(text, "/") {
		initial = 1
		if strings.HasPrefix(text, "//") && !strings.HasPrefix(text, "///") {
			initial = 2
		}
	}
	var parts []string
	for _, part := range strings.Split(text, "/") {
		switch {
		case part == "" || part == ".":
		case part != "..":
			parts = append(parts, part)
		case initial == 0 && (len(parts) == 0 || parts[len(parts)-1] == ".."):
			parts = append(parts, part)
		case len(parts) > 0:
			parts = parts[:len(parts)-1]
		}
	}
	result := strings.Repeat("/", initial) + strings.Join(parts, "/")
	if result == "" {
		return "."
	}
	return result
}

// expandUser is Path(text).expanduser().
func expandUser(text string) string {
	if !strings.HasPrefix(text, "~") {
		return text
	}
	name, rest, _ := strings.Cut(text[1:], "/")
	if name != "" {
		// ~user needs the password database; leave it to Python.
		unsupported("~user path")
	}
	home := os.Getenv("HOME")
	if home == "" {
		unsupported("no HOME")
	}
	if rest == "" {
		return home
	}
	return strings.TrimRight(home, "/") + "/" + rest
}

// stat is Path.stat() as Path.exists(), is_file() and is_dir() use it: a
// missing path, a file used as a directory or a symlink loop is absent, and
// any other error raises in Python, which the Python doctor reports.
func stat(text string, follow bool) (fs.FileInfo, bool) {
	var info fs.FileInfo
	var err error
	if follow {
		info, err = os.Stat(text)
	} else {
		info, err = os.Lstat(text)
	}
	if err == nil {
		return info, true
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EBADF) ||
		errors.Is(err, syscall.ELOOP) {
		return nil, false
	}
	unsupported("stat " + text + ": " + err.Error())
	return nil, false
}

func isFile(text string) bool {
	info, ok := stat(text, true)
	return ok && info.Mode().IsRegular()
}

func isDir(text string) bool {
	info, ok := stat(text, true)
	return ok && info.IsDir()
}

func exists(text string) bool {
	_, ok := stat(text, true)
	return ok
}

func isSymlink(text string) bool {
	info, ok := stat(text, false)
	return ok && info.Mode()&fs.ModeSymlink != 0
}

var windowsDrivePath = regexp.MustCompile(`^/[A-Za-z]:[/\\]`)

// localArtifactPath is _local_artifact_path_from_reference.
func localArtifactPath(value string) string {
	raw, err := bazelinspect.StripFileURI(value)
	if err != nil {
		unsupported("artifact reference " + value)
	}
	if windowsDrivePath.MatchString(raw) {
		raw = raw[1:]
	}
	return purePath(raw)
}

// resolveLocalArtifact is _resolve_bep_local_artifact_path, which reports
// a missing artifact with false.
func (d *doctor) resolveLocalArtifact(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	artifact := expandUser(localArtifactPath(value))
	var candidates []string
	if filepath.IsAbs(artifact) {
		candidates = append(candidates, artifact)
	} else {
		candidates = append(candidates, joinPath(d.workspace, artifact))
		for _, root := range d.request.ExecrootRoots {
			candidates = append(candidates, joinPath(root, artifact))
		}
		for _, root := range d.request.OutputBaseRoots {
			candidates = append(candidates, joinPath(root, artifact))
		}
	}
	for _, candidate := range candidates {
		if exists(candidate) {
			return resolvePath(candidate), true
		}
	}
	return "", false
}
