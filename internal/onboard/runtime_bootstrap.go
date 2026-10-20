package onboard

import (
	"fmt"
	"strconv"
	"strings"
)

func symbolicConditionValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.ContainsRune(text, 0)
}

// GitHub format() replaces numbered placeholders and escapes doubled braces.
// Only string arguments are needed for bootstrap paths; other types and format
// specifiers stay inconclusive. Replacement values are never parsed again.
func (p *conditionParser) format() (any, error) {
	if !p.take("(") {
		return nil, fmt.Errorf("missing format arguments")
	}
	var args []string
	for {
		value, err := p.expression(0)
		text, ok := value.(string)
		if err != nil || !ok {
			return nil, fmt.Errorf("unsupported format argument")
		}
		args = append(args, text)
		if !p.take(",") {
			break
		}
	}
	if !p.take(")") {
		return nil, fmt.Errorf("unclosed format function")
	}
	var result strings.Builder
	for template := args[0]; template != ""; {
		switch template[0] {
		case '{', '}':
			if len(template) > 1 && template[0] == template[1] {
				result.WriteByte(template[0])
				template = template[2:]
				break
			}
			i := 1
			for i < len(template) && template[i] >= '0' && template[i] <= '9' {
				i++
			}
			if template[0] != '{' || i == 1 || i >= len(template) || template[i] != '}' {
				return nil, fmt.Errorf("unsupported format placeholder")
			}
			index, err := strconv.ParseUint(template[1:i], 10, 8)
			if err != nil || int(index)+1 >= len(args) {
				return nil, fmt.Errorf("format argument index out of range")
			}
			result.WriteString(args[index+1])
			template = template[i+1:]
		default:
			result.WriteByte(template[0])
			template = template[1:]
		}
		if result.Len() > 4096 {
			return nil, fmt.Errorf("format result exceeds 4096 characters")
		}
	}
	return result.String(), nil
}
