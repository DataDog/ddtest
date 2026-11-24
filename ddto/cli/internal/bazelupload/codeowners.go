package bazelupload

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/codeownership"
)

type ownerLookup struct {
	rules     *codeownership.CodeOwners
	workspace string
	cache     map[[2]string]string
}

func readOwners(root *os.Root, file, dialect string) (*codeownership.CodeOwners, error) {
	if file == "" {
		return nil, nil
	}
	info, err := root.Lstat(file)
	if err != nil {
		return nil, fmt.Errorf("read CODEOWNERS: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read CODEOWNERS: expected a regular file")
	}

	file = filepath.ToSlash(file)
	parser := codeownership.GitHub
	if dialect == "gitlab" || (dialect == "" && path.Dir(file) == ".gitlab") {
		parser = codeownership.GitLab
	}
	rules, err := codeownership.LoadFS(root.FS(), file, parser)
	if err != nil {
		return nil, fmt.Errorf("read CODEOWNERS: %w", err)
	}
	return rules, nil
}

type ownerPaths struct {
	drive    *regexp.Regexp
	runfiles []*regexp.Regexp
}

var ownerExpressions = sync.OnceValue(func() ownerPaths {
	return ownerPaths{
		drive: regexp.MustCompile(`^[A-Za-z]:/`),
		runfiles: []*regexp.Regexp{
			regexp.MustCompile(`/execroot/[^/]+/_main/(.+)$`),
			regexp.MustCompile(`/execroot/[^/]+/(.+)$`),
			regexp.MustCompile(`\.runfiles/_main/(.+)$`),
			regexp.MustCompile(`\.runfiles/[^/]+/(.+)$`),
		},
	}
})

func normalizeSource(source string) string {
	source = strings.TrimPrefix(source, "file://")
	if decoded, err := url.PathUnescape(source); err == nil &&
		!strings.ContainsRune(decoded, 0) && utf8.ValidString(decoded) {
		source = decoded
	}
	source = strings.ReplaceAll(source, "\\", "/")
	if strings.HasPrefix(source, "/") && ownerExpressions().drive.MatchString(source[1:]) {
		source = source[1:]
	}
	depth := 0
	for part := range strings.SplitSeq(source, "/") {
		switch part {
		case "", ".":
		case "..":
			if depth == 0 {
				return ""
			}
			depth--
		default:
			depth++
		}
	}
	cleaned := path.Clean(source)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func (lookup *ownerLookup) enrich(content, meta Object, contextWorkspace string) {
	if lookup.rules == nil {
		return
	}
	if _, exists := meta["test.codeowners"]; exists {
		return
	}
	sourceFields := object(content["source"])
	source := firstString(stringValue(meta, "test.source.file"), stringValue(meta, "test.source.path"),
		stringValue(meta, "source.file"), stringValue(meta, "source.path"), stringValue(sourceFields, "file"),
		stringValue(sourceFields, "path"))
	if source == "" {
		return
	}
	key := [2]string{source, contextWorkspace}
	result, cached := lookup.cache[key]
	if !cached {
		result = lookup.match(source, contextWorkspace)
		if len(lookup.cache) < 4096 {
			lookup.cache[key] = result
		}
	}
	if result != "" {
		meta["test.codeowners"] = result
	}
}

func (lookup *ownerLookup) match(source, contextWorkspace string) string {
	source = normalizeSource(source)
	for _, base := range []string{contextWorkspace, lookup.workspace} {
		base = normalizeSource(base)
		if base == "" {
			continue
		}
		prefix := strings.TrimSuffix(base, "/") + "/"
		matches := strings.HasPrefix(source, prefix)
		if runtime.GOOS == "windows" && len(source) >= len(prefix) {
			matches = strings.EqualFold(source[:len(prefix)], prefix)
		}
		if matches {
			if owners, found := lookup.matchCandidate(source[len(prefix):]); found {
				return owners
			}
		}
	}
	patterns := ownerExpressions()
	for _, expression := range patterns.runfiles {
		candidate := expression.FindStringSubmatch(source)
		if len(candidate) > 1 && !strings.HasPrefix(candidate[1], "external/") &&
			!strings.HasPrefix(candidate[1], "_main/external/") {
			if owners, found := lookup.matchCandidate(candidate[1]); found {
				return owners
			}
		}
	}
	if !strings.HasPrefix(source, "/") && !patterns.drive.MatchString(source) {
		owners, _ := lookup.matchCandidate(source)
		return owners
	}
	return ""
}

func (lookup *ownerLookup) matchCandidate(candidate string) (string, bool) {
	if candidate == "" || strings.HasPrefix(candidate, "bazel-out/") {
		return "", false
	}
	owners, matched := lookup.rules.Match(candidate)
	return owners.Tag(), matched
}
