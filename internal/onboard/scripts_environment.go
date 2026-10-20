package onboard

import (
	"errors"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

var assignmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func shellAssignment(word string) bool {
	key, _, found := strings.Cut(word, "=")
	return found && assignmentName.MatchString(key)
}

// Quote an assignment's value without turning its name into an executable.
func shellCommand(words []string) string {
	var prefix []string
	for len(words) > 0 && shellAssignment(words[0]) {
		key, value, _ := strings.Cut(words[0], "=")
		prefix = append(prefix, key+"="+shellquote.Join(value))
		words = words[1:]
	}
	if len(words) > 0 {
		prefix = append(prefix, shellquote.Join(words...))
	}
	return strings.Join(prefix, " ")
}

func applyCommandEnvironment(result *commandResolution, outer map[string]string) {

	for i := range result.Tests {
		result.Tests[i].Environment = mergeEnvironment(outer, result.Tests[i].Environment)
		for j := range result.Tests[i].Prerequisites {
			result.Tests[i].Prerequisites[j].Environment = mergeEnvironment(outer, result.Tests[i].Prerequisites[j].Environment)
		}
	}
	for i := range result.Builds {
		result.Builds[i].Environment = mergeEnvironment(outer, result.Builds[i].Environment)
	}
}

func repositoryPath(root, path string) string {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			root, path = canonical, target
		}
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(relative)
}

func repositoryError(root string, err error) string {
	text := strings.ReplaceAll(err.Error(), filepath.Clean(root)+string(filepath.Separator), "")
	if canonical, e := filepath.EvalSymlinks(root); e == nil {
		text = strings.ReplaceAll(text, canonical+string(filepath.Separator), "")
	}
	return text
}

func joinDiagnostic(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

// Tracer preloads are checked independently. Keep ordinary literal runtime
// options; unknown expansions or custom module hooks require review.
func ordinaryNodeOptions(value string) (string, error) {
	for _, reference := range []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT"} {
		pattern := regexp.MustCompile(`\$\{\{\s*env\.` + reference + `\s*\}\}`)
		module := "dd-trace/ci/init"
		if reference == "DD_TRACE_ESM_IMPORT" {
			module = "dd-trace/register.js"
		}
		value = pattern.ReplaceAllString(value, module)
	}
	if strings.ContainsAny(value, "$`") {
		return "", errors.New("dynamic NODE_OPTIONS requires review")
	}
	words, err := shellquote.Split(value)
	if err != nil {
		return "", err
	}
	var kept []string
	for i := 0; i < len(words); i++ {
		word := words[i]
		key, module, equals := strings.Cut(word, "=")
		if slices.Contains([]string{"-r", "--require", "--import", "--loader", "--experimental-loader"}, key) {
			if !equals {
				if i+1 >= len(words) {
					return "", errors.New("missing NODE_OPTIONS module")
				}
				i++
				module = words[i]
			}
			if (key == "-r" || key == "--require") && module == "dd-trace/ci/init" || key == "--import" && module == "dd-trace/register.js" {
				continue
			}
			return "", errors.New("custom NODE_OPTIONS module requires review")
		}
		if strings.HasPrefix(word, "-r") && len(word) > 2 {
			if word[2:] == "dd-trace/ci/init" {
				continue
			}
			return "", errors.New("custom NODE_OPTIONS preload requires review")
		}
		kept = append(kept, word)
	}
	return shellquote.Join(kept...), nil
}

func literalNodeJest(words []string) bool {
	if len(words) < 2 || words[0] != "node" {
		return false
	}
	for i := 1; i < len(words); i++ {
		word := words[i]
		if strings.HasPrefix(word, "-") {
			key, _, equal := strings.Cut(word, "=")
			if slices.Contains([]string{"--conditions", "--max-old-space-size"}, key) {
				if !equal {
					i++
					if i >= len(words) {
						return false
					}
				}
				continue
			}
			if !slices.Contains([]string{"--experimental-vm-modules", "--no-warnings", "--enable-source-maps"}, word) {
				return false
			}
			continue
		}
		path := filepath.ToSlash(filepath.Clean(word))
		return filepath.IsLocal(word) && (path == "node_modules/jest/bin/jest.js" || path == "node_modules/.bin/jest")
	}
	return false
}

// Resolve bounded workflow expressions as data, never shell code. This also
// handles conditional tracer-only settings without discarding ordinary flags.
func resolveNodeOptions(value string, row map[string]any, env map[string]string) (string, error) {
	if len(value) > 4096 {
		return "", errors.New("NODE_OPTIONS exceeds discovery limit")
	}
	known := maps.Clone(env)
	if known == nil {
		known = map[string]string{}
	}
	for key, module := range map[string]string{"DD_TRACE_PACKAGE": "dd-trace/ci/init", "DD_TRACE_ESM_IMPORT": "dd-trace/register.js"} {
		if _, exists := known[key]; !exists {
			known[key] = module
		}
	}
	var result strings.Builder
	for {
		literal, expression, found := strings.Cut(value, "${{")
		result.WriteString(literal)
		if !found {
			break
		}
		parser := conditionParser{rest: expression, row: row, env: known}
		resolved, err := parser.expression(0)
		text, ok := resolved.(string)
		if err != nil || !ok || !parser.take("}}") {
			return "", errors.New("unresolved NODE_OPTIONS expression")
		}
		result.WriteString(text)
		if result.Len() > 4096 {
			return "", errors.New("NODE_OPTIONS exceeds discovery limit")
		}
		value = parser.rest
	}
	return ordinaryNodeOptions(result.String())
}
