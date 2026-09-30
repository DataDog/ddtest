package nodeoptions

import (
	"path/filepath"
	"strings"
)

type token struct {
	raw   string
	value string
}

// Node uses double quotes, rather than shell quoting, in NODE_OPTIONS. Keep
// each original token so removing a preload does not change other options or
// quoted project loader paths.
func split(value string) []token {
	var tokens []token
	for index := 0; index < len(value); {
		for index < len(value) && isSpace(value[index]) {
			index++
		}
		if index == len(value) {
			break
		}
		start := index
		var decoded strings.Builder
		quoted := false
		for index < len(value) {
			current := value[index]
			if current == '"' {
				quoted = !quoted
				index++
				continue
			}
			if current == '\\' && index+1 < len(value) && value[index+1] == '"' {
				decoded.WriteByte('"')
				index += 2
				continue
			}
			if !quoted && isSpace(current) {
				break
			}
			decoded.WriteByte(current)
			index++
		}
		tokens = append(tokens, token{raw: value[start:index], value: decoded.String()})
	}
	return tokens
}

func isSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func optionValue(tokens []token, index int, option string) (string, int, bool) {
	value := tokens[index].value
	if value == option || (option == "--require" && value == "-r") {
		if index+1 < len(tokens) {
			return tokens[index+1].value, 2, true
		}
		return "", 1, false
	}
	if strings.HasPrefix(value, option+"=") {
		return strings.TrimPrefix(value, option+"="), 1, true
	}
	if option == "--require" && strings.HasPrefix(value, "-r") && len(value) > 2 {
		return strings.TrimPrefix(value, "-r"), 1, true
	}
	return "", 1, false
}

func matchesModule(value, module string) bool {
	if value == module {
		return true
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	return strings.HasSuffix(normalized, "/"+module) ||
		(module == "dd-trace/ci/init" && (value == module+".js" || strings.HasSuffix(normalized, "/"+module+".js")))
}

func find(value, option, module string, absoluteOnly bool) string {
	tokens := split(value)
	for index := 0; index < len(tokens); index++ {
		candidate, width, found := optionValue(tokens, index, option)
		if found && matchesModule(candidate, module) && (!absoluteOnly || filepath.IsAbs(candidate)) {
			return candidate
		}
		index += width - 1
	}
	return ""
}

func HasRequire(value, module string) bool {
	return find(value, "--require", module, false) != ""
}

func AbsoluteRequire(value, module string) string {
	return find(value, "--require", module, true)
}

func HasImport(value, module string) bool {
	return find(value, "--import", module, false) != ""
}

func without(value, option, module string) string {
	tokens := split(value)
	kept := make([]string, 0, len(tokens))
	for index := 0; index < len(tokens); {
		candidate, width, found := optionValue(tokens, index, option)
		if found && matchesModule(candidate, module) {
			index += width
			continue
		}
		kept = append(kept, tokens[index].raw)
		index++
	}
	return strings.Join(kept, " ")
}

func WithoutRequire(value, module string) string {
	return without(value, "--require", module)
}

func WithoutImport(value, module string) string {
	return without(value, "--import", module)
}
