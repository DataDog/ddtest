package platform

import "strings"

type nodeOptionsToken struct {
	raw   string
	value string
}

// Node uses double quotes, rather than shell quoting, in NODE_OPTIONS. Keep
// each original token so removing a preload does not change other options or
// quoted project loader paths.
func splitnodeOptions(value string) []nodeOptionsToken {
	var tokens []nodeOptionsToken
	for index := 0; index < len(value); {
		for index < len(value) && isnodeOptionsSpace(value[index]) {
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
			if !quoted && isnodeOptionsSpace(current) {
				break
			}
			decoded.WriteByte(current)
			index++
		}
		tokens = append(tokens, nodeOptionsToken{raw: value[start:index], value: decoded.String()})
	}
	return tokens
}

func isnodeOptionsSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func nodeOptionsOptionValue(tokens []nodeOptionsToken, index int, option string) (string, int, bool) {
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

func nodeOptionsMatchesModule(value, module string) bool {
	if value == module {
		return true
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	return strings.HasSuffix(normalized, "/"+module) ||
		(module == "dd-trace/ci/init" && (value == module+".js" || strings.HasSuffix(normalized, "/"+module+".js")))
}

func findNodeOption(value, option, module string) string {
	tokens := splitnodeOptions(value)
	for index := 0; index < len(tokens); index++ {
		candidate, width, found := nodeOptionsOptionValue(tokens, index, option)
		if found && nodeOptionsMatchesModule(candidate, module) {
			return candidate
		}
		index += width - 1
	}
	return ""
}

func nodeOptionsHasRequire(value, module string) bool {
	return nodeOptionsRequire(value, module) != ""
}

func nodeOptionsRequire(value, module string) string {
	return findNodeOption(value, "--require", module)
}

func nodeOptionsHasImport(value, module string) bool {
	return findNodeOption(value, "--import", module) != ""
}

func withoutNodeOption(value, option, module string) string {
	tokens := splitnodeOptions(value)
	kept := make([]string, 0, len(tokens))
	for index := 0; index < len(tokens); {
		candidate, width, found := nodeOptionsOptionValue(tokens, index, option)
		if found && nodeOptionsMatchesModule(candidate, module) {
			index += width
			continue
		}
		kept = append(kept, tokens[index].raw)
		index++
	}
	return strings.Join(kept, " ")
}

func nodeOptionsWithoutRequire(value, module string) string {
	return withoutNodeOption(value, "--require", module)
}

func nodeOptionsWithoutImport(value, module string) string {
	return withoutNodeOption(value, "--import", module)
}
