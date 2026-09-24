// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// A bounded subset of GitHub expressions, not a workflow interpreter. Unknown
// contexts/functions stay inconclusive, even inside short-circuited expressions.
// Scalar types matter: the string "false" is truthy; boolean false is not.
func runtimeCondition(value string, row map[string]any) (bool, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${{") && strings.HasSuffix(value, "}}") {
		value = strings.TrimSpace(value[3 : len(value)-2])
	}
	if value == "" {
		return true, nil
	}
	if len(value) > 4096 {
		return false, fmt.Errorf("CI condition exceeds 4096 characters")
	}
	p := conditionParser{rest: value, row: row}
	result, err := p.expression(0)
	if err != nil || strings.TrimSpace(p.rest) != "" {
		return false, fmt.Errorf("cannot statically resolve CI condition %q; unsupported syntax requires review, not a workflow rewrite", value)
	}
	return conditionTruthy(result), nil
}

type conditionParser struct {
	rest  string
	row   map[string]any
	depth int
}

func (p *conditionParser) take(token string) bool {
	p.rest = strings.TrimSpace(p.rest)
	if !strings.HasPrefix(p.rest, token) {
		return false
	}
	p.rest = p.rest[len(token):]
	return true
}

func (p *conditionParser) expression(level int) (any, error) {
	if level == 3 {
		return p.atom()
	}
	left, err := p.expression(level + 1)
	if err != nil {
		return nil, err
	}
	operators := [][]string{{"||"}, {"&&"}, {"==", "!="}}[level]
	for {
		op := ""
		for _, candidate := range operators {
			if p.take(candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			return left, nil
		}
		right, err := p.expression(level + 1)
		if err != nil {
			return nil, err
		}
		switch op {
		case "||":
			if !conditionTruthy(left) {
				left = right
			}
		case "&&":
			if conditionTruthy(left) {
				left = right
			}
		case "==", "!=":
			equal, err := conditionEqual(left, right)
			if err != nil {
				return nil, err
			}
			left = equal == (op == "==")
		}
	}
}

func (p *conditionParser) atom() (any, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 32 {
		return nil, fmt.Errorf("condition nesting exceeds 32")
	}
	if p.take("!") {
		value, err := p.atom()
		return !conditionTruthy(value), err
	}
	if p.take("(") {
		value, err := p.expression(0)
		if err != nil || !p.take(")") {
			return nil, fmt.Errorf("unclosed group")
		}
		return value, nil
	}
	if p.take("'") {
		var value strings.Builder
		for len(p.rest) > 0 {
			i := strings.IndexByte(p.rest, '\'')
			if i < 0 {
				break
			}
			value.WriteString(p.rest[:i])
			p.rest = p.rest[i+1:]
			if !strings.HasPrefix(p.rest, "'") {
				return value.String(), nil
			}
			value.WriteByte('\'')
			p.rest = p.rest[1:]
		}
		return nil, fmt.Errorf("unclosed string")
	}
	p.rest = strings.TrimSpace(p.rest)
	i := 0
	for i < len(p.rest) && (unicode.IsLetter(rune(p.rest[i])) || unicode.IsDigit(rune(p.rest[i])) || strings.ContainsRune("._-", rune(p.rest[i]))) {
		i++
	}
	token := p.rest[:i]
	p.rest = p.rest[i:]
	switch token {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "startsWith":
		if !p.take("(") {
			return nil, fmt.Errorf("missing argument list")
		}
		left, err := p.expression(0)
		if err != nil || !p.take(",") {
			return nil, fmt.Errorf("missing prefix argument")
		}
		right, err := p.expression(0)
		if err != nil || !p.take(")") {
			return nil, fmt.Errorf("unclosed function")
		}
		return strings.HasPrefix(strings.ToLower(fmt.Sprint(left)), strings.ToLower(fmt.Sprint(right))), nil
	}
	if key, ok := strings.CutPrefix(token, "matrix."); ok {
		value, exists := p.row[key]
		if exists {
			return value, nil
		}
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return number, nil
	}
	return nil, fmt.Errorf("unsupported operand %q", token)
}

func conditionTruthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case float64:
		return v != 0
	default:
		return false
	}
}

func conditionEqual(left, right any) (bool, error) {
	if a, ok := left.(string); ok {
		if b, ok := right.(string); ok {
			return strings.EqualFold(a, b), nil
		}
	}
	if a, ok := left.(bool); ok {
		if b, ok := right.(bool); ok {
			return a == b, nil
		}
	}
	// Avoid approximating GitHub's loose cross-type coercions. A mixed comparison
	// remains unverified rather than selecting the wrong runtime silently.
	number := func(v any) (float64, bool) {
		switch n := v.(type) {
		case int:
			return float64(n), true
		case float64:
			return n, true
		}
		return 0, false
	}
	a, aok := number(left)
	b, bok := number(right)
	if aok && bok {
		return a == b, nil
	}
	return false, fmt.Errorf("mixed-type comparison requires review")
}
