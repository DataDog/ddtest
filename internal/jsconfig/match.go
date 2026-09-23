package jsconfig

import (
	"fmt"
	"regexp"
	"strings"
)

// Ordered globs preserve Jest's negative-pattern/reinclusion semantics. Patterns
// outside this subset use native discovery, not a different glob dialect.
type glob struct {
	regex    *regexp.Regexp
	negative bool
}

func compileGlobs(patterns []string) ([]glob, error) {
	result := make([]glob, 0, len(patterns))
	for _, pattern := range patterns {
		negative := false
		for strings.HasPrefix(pattern, "!") && !strings.HasPrefix(pattern, "!(") {
			negative = !negative
			pattern = pattern[1:]
		}
		body, err := globRegex(pattern)
		if err != nil {
			return nil, err
		}
		r, err := regexp.Compile("^(?:" + body + ")$")
		if err != nil {
			return nil, err
		}
		result = append(result, glob{r, negative})
	}
	return result, nil
}

func matchGlobs(globs []glob, path string) bool {
	kept, allNegative := false, len(globs) > 0
	for _, g := range globs {
		if !g.negative {
			allNegative = false
		}
	}
	if allNegative {
		kept = true
	}
	for _, g := range globs {
		if g.regex.MatchString(path) {
			kept = !g.negative
		}
	}
	return kept
}

func globRegex(pattern string) (string, error) {
	if len(pattern) > 4096 {
		return "", fmt.Errorf("glob is too long")
	}
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if i+1 < len(pattern) && pattern[i+1] == '(' && strings.ContainsRune("?*+@!", rune(c)) {
			if c == '!' {
				return "", fmt.Errorf("negative extglob needs native discovery: %s", pattern)
			}
			end, depth := i+2, 1
			for ; end < len(pattern) && depth > 0; end++ {
				if pattern[end] == '(' {
					depth++
				}
				if pattern[end] == ')' {
					depth--
				}
			}
			if depth != 0 {
				return "", fmt.Errorf("unclosed extglob: %s", pattern)
			}
			parts, err := globAlternatives(pattern[i+2:end-1], '|')
			if err != nil {
				return "", err
			}
			b.WriteString("(?:" + parts + ")")
			if c != '@' {
				b.WriteByte(c)
			}
			i = end - 1
			continue
		}
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// Globstar has directory semantics only as a complete segment.
				if i > 0 && pattern[i-1] != '/' {
					return "", fmt.Errorf("embedded globstar needs native discovery")
				}
				i++
				if i+1 < len(pattern) {
					if pattern[i+1] != '/' {
						return "", fmt.Errorf("embedded globstar needs native discovery")
					}
					b.WriteString("(?:.*/)?")
					i++
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			end, depth := i+1, 1
			for ; end < len(pattern) && depth > 0; end++ {
				if pattern[end] == '{' {
					depth++
				}
				if pattern[end] == '}' {
					depth--
				}
			}
			if depth != 0 {
				return "", fmt.Errorf("unclosed brace glob")
			}
			body := pattern[i+1 : end-1]
			if strings.Contains(body, "..") || !strings.Contains(body, ",") {
				return "", fmt.Errorf("brace range needs native discovery")
			}
			parts, err := globAlternatives(body, ',')
			if err != nil {
				return "", err
			}
			b.WriteString("(?:" + parts + ")")
			i = end - 1
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return "", fmt.Errorf("unclosed character class")
			}
			end += i + 1
			body := pattern[i+1 : end]
			if body == "" || strings.ContainsAny(body, "\\[:!^/") {
				return "", fmt.Errorf("unsupported glob character class")
			}
			// picomatch also permits a literal '[abc]' match. That behavior is
			// intentionally retained alongside character-class matching.
			b.WriteString("(?:[" + body + "]|" + regexp.QuoteMeta(pattern[i:end+1]) + ")")
			i = end
		case '\\', '(', ')', '|', '}', ']':
			return "", fmt.Errorf("unsupported glob syntax: %s", pattern)
		default:
			if c >= 0x80 {
				b.WriteByte(c)
			} else {
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
	}
	return b.String(), nil
}

func globAlternatives(body string, separator byte) (string, error) {
	parts := []string{}
	start, depth := 0, 0
	for i := 0; i <= len(body); i++ {
		if i == len(body) || body[i] == separator && depth == 0 {
			part, err := globRegex(body[start:i])
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
			start = i + 1
			continue
		}
		if body[i] == '(' || body[i] == '{' {
			depth++
		}
		if body[i] == ')' || body[i] == '}' {
			depth--
		}
	}
	return strings.Join(parts, "|"), nil
}

func compileRegexes(patterns []string) ([]*regexp.Regexp, error) {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		// RE2 and JavaScript share this subset. Reject RE2-only syntax; JS
		// lookarounds and backreferences fail Compile and use native discovery.
		if strings.Contains(p, "(?") || strings.Contains(p, "[[:") {
			return nil, fmt.Errorf("regex needs native discovery: %s", p)
		}
		for i := 0; i+1 < len(p); i++ {
			if p[i] == '\\' {
				i++
				if strings.ContainsRune("AZzQEpPCsS", rune(p[i])) {
					return nil, fmt.Errorf("regex escape needs native discovery: %s", p)
				}
			}
		}
		r, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("regex needs native discovery: %w", err)
		}
		result = append(result, r)
	}
	return result, nil
}

func matchRegexes(patterns []*regexp.Regexp, path string) bool {
	for _, p := range patterns {
		if p.MatchString(path) {
			return true
		}
	}
	return false
}
