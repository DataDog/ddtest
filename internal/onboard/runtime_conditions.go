// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// A bounded subset of GitHub expressions, not a workflow interpreter. Unknown
// contexts/functions stay inconclusive, even inside short-circuited expressions.
// Scalar types matter: the string "false" is truthy; boolean false is not.
func runtimeCondition(value string, row map[string]any, environments ...map[string]string) (bool, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${{") && strings.HasSuffix(value, "}}") {
		value = strings.TrimSpace(value[3 : len(value)-2])
	}
	if value == "" {
		return true, nil
	}
	if len(value) > 4096 || strings.ContainsRune(value, 0) {
		return false, fmt.Errorf("CI condition exceeds 4096 characters or contains a NUL byte")
	}
	p := conditionParser{rest: value, row: row, ifCondition: true}
	if len(environments) > 0 {
		p.env = environments[0]
	}
	result, err := p.expression(0)
	if err != nil || strings.TrimSpace(p.rest) != "" {
		reason := "unsupported syntax requires review, not a workflow rewrite"
		if err != nil {
			reason += ": " + err.Error()
		}
		return false, fmt.Errorf("cannot statically resolve CI condition %q; %s", value, reason)
	}
	return conditionTruthy(result), nil
}

type conditionParser struct {
	rest  string
	row   map[string]any
	env   map[string]string
	depth int
	// Secrets are allowed in environment expressions, but never in GitHub if.
	ifCondition bool
}

// Skip only leading whitespace: the remainder may be the rest of a shell
// command whose trailing whitespace is significant.
func (p *conditionParser) take(token string) bool {
	p.rest = strings.TrimLeftFunc(p.rest, unicode.IsSpace)
	if !strings.HasPrefix(p.rest, token) {
		return false
	}
	p.rest = p.rest[len(token):]
	return true
}

func (p *conditionParser) expression(level int) (any, error) {
	if level == 4 {
		return p.atom()
	}
	left, err := p.expression(level + 1)
	if err != nil {
		return nil, err
	}
	operators := [][]string{{"||"}, {"&&"}, {"==", "!="}, {"<=", ">=", "<", ">"}}[level]
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
		default:
			left, err = conditionOrder(left, right, op)
			if err != nil {
				return nil, err
			}
		}
	}
}

// Support static numbers and strings using GitHub's ordering rules. Two
// strings compare case-insensitively; mixed string/number operands coerce to
// numbers. Invalid numeric strings become NaN, whose comparisons are false.
// Only unlike types coerce, so two booleans or two nulls never order.
func conditionOrder(left, right any, op string) (bool, error) {
	if symbolicConditionValue(left) || symbolicConditionValue(right) {
		return false, fmt.Errorf("action path contents require runtime resolution")
	}
	switch left.(type) {
	case bool:
		if _, ok := right.(bool); ok {
			return false, nil
		}
	case nil:
		if right == nil {
			return false, nil
		}
	}
	var a, b float64
	if x, ok := left.(string); ok {
		if y, ok := right.(string); ok {
			a = float64(strings.Compare(strings.ToLower(x), strings.ToLower(y)))
			return compareConditionNumbers(a, 0, op), nil
		}
	}
	a, err := conditionNumber(left)
	if err != nil {
		return false, err
	}
	b, err = conditionNumber(right)
	if err != nil {
		return false, err
	}
	return compareConditionNumbers(a, b, op), nil
}

func compareConditionNumbers(a, b float64, op string) bool {
	switch op {
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	default:
		return a >= b
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
	p.rest = strings.TrimLeftFunc(p.rest, unicode.IsSpace)
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
	case "null":
		return nil, nil
	case "format":
		return p.format()
	case "fromJSON":
		if !p.take("(") {
			return nil, fmt.Errorf("missing JSON argument")
		}
		value, err := p.expression(0)
		if err != nil || !p.take(")") {
			return nil, fmt.Errorf("unclosed JSON argument")
		}
		text, ok := value.(string)
		if !ok || symbolicConditionValue(text) {
			return nil, fmt.Errorf("JSON argument requires a known string")
		}
		var values []any
		if json.Unmarshal([]byte(text), &values) != nil || values == nil || len(values) > 128 {
			return nil, fmt.Errorf("only JSON arrays of at most 128 scalars are supported")
		}
		strs := make([]string, 0, len(values))
		for _, value := range values {
			switch value := value.(type) {
			case string:
				if strings.ContainsRune(value, 0) {
					return nil, fmt.Errorf("JSON array entries must not contain NUL bytes")
				}
				strs = append(strs, value)
			case float64, bool, nil:
			default:
				return nil, fmt.Errorf("only JSON scalar array entries are supported")
			}
		}
		if len(strs) == len(values) {
			return strs, nil
		}
		// Mixed scalars keep their types for contains()'s loose equality.
		return values, nil
	case "contains":
		if !p.take("(") {
			return nil, fmt.Errorf("missing contains arguments")
		}
		search, err := p.expression(0)
		if err != nil || !p.take(",") {
			return nil, fmt.Errorf("missing contains item")
		}
		value, err := p.expression(0)
		if err != nil || !p.take(")") {
			return nil, fmt.Errorf("unclosed contains arguments")
		}
		if symbolicConditionValue(value) || symbolicConditionValue(search) {
			return nil, fmt.Errorf("contains requires known operands")
		}
		// GitHub searches an array with loose equality against the original
		// item, and a scalar as a case-insensitive substring of strings.
		var elements []any
		switch values := search.(type) {
		case []string:
			for _, element := range values {
				elements = append(elements, element)
			}
		case []any:
			elements = values
		default:
			text, textOK := conditionString(search)
			item, itemOK := conditionString(value)
			if !textOK || !itemOK {
				return nil, fmt.Errorf("contains requires a string or array")
			}
			return strings.Contains(strings.ToLower(text), strings.ToLower(item)), nil
		}
		for _, element := range elements {
			equal, err := conditionEqual(value, element)
			if err != nil {
				return nil, err
			}
			if equal {
				return true, nil
			}
		}
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
		if symbolicConditionValue(left) || symbolicConditionValue(right) {
			return nil, fmt.Errorf("action path contents require runtime resolution")
		}
		a, aok := conditionString(left)
		b, bok := conditionString(right)
		if !aok || !bok {
			return nil, fmt.Errorf("prefix comparison requires scalar operands")
		}
		return strings.HasPrefix(strings.ToLower(a), strings.ToLower(b)), nil
	}
	if key, ok := strings.CutPrefix(token, "matrix."); ok {
		value, exists, err := matrixProperty(p.row, key)
		if err != nil {
			return nil, err
		}
		if exists {
			if text, ok := value.(string); ok && strings.ContainsRune(text, 0) {
				return nil, fmt.Errorf("matrix value contains a NUL byte")
			}
			return value, nil
		}
		if p.row != nil {
			return "", nil
		} // GitHub returns an empty string for a missing property in a known context.
	}
	if key, ok := strings.CutPrefix(token, "env."); ok {
		if value, exists := p.env[key]; exists {
			return value, nil
		}
	}
	if p.ifCondition && strings.HasPrefix(token, "secrets.") {
		return nil, fmt.Errorf("GitHub does not allow the secrets context in if; set a job-level environment variable from the secret and test env.<name> instead")
	}
	if strings.HasPrefix(token, "inputs.") || strings.HasPrefix(token, "github.") || strings.HasPrefix(token, "steps.") || strings.HasPrefix(token, "secrets.") || strings.HasPrefix(token, "needs.") || strings.HasPrefix(token, "vars.") {
		if value, exists := p.env["\x00"+token]; exists {
			return value, nil
		}
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return number, nil
	}
	return nil, fmt.Errorf("unsupported operand %q", token)
}

// conditionTruthy follows the runner's EvaluationResult.IsFalsy: null, false,
// 0, NaN and the empty string are falsy; arrays and objects are truthy.
func conditionTruthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case float64:
		return v != 0 && !math.IsNaN(v)
	case []string, []any, map[string]any:
		return true
	default:
		return false
	}
}

func conditionEqual(left, right any) (bool, error) {
	// An active action exports nonempty paths; their contents remain unknown.
	if (symbolicConditionValue(left) && right == "") || (symbolicConditionValue(right) && left == "") {
		return false, nil
	}
	if symbolicConditionValue(left) || symbolicConditionValue(right) {
		return false, fmt.Errorf("action path contents require runtime resolution")
	}
	if a, ok := left.(string); ok {
		if b, ok := right.(string); ok {
			return strings.EqualFold(a, b), nil
		}
	}
	switch left.(type) {
	case []string, []any, map[string]any:
		switch right.(type) {
		case []string, []any, map[string]any:
			return false, fmt.Errorf("composite equality requires runtime identity resolution")
		}
	}
	a, err := conditionNumber(left)
	if err != nil {
		return false, err
	}
	b, err := conditionNumber(right)
	return a == b, err
}

// GitHub coerces unlike scalar types to numbers for equality and ordering;
// strings follow the runner's ParseNumber and arrays compare as NaN.
func conditionNumber(value any) (float64, error) {
	switch v := value.(type) {
	case nil:
		return 0, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case int:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		return conditionParseNumber(v), nil
	case []string, []any, map[string]any:
		return math.NaN(), nil
	default:
		return 0, fmt.Errorf("unsupported comparison operand requires review")
	}
}

var (
	conditionDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	conditionHex     = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	conditionOctal   = regexp.MustCompile(`^0o[0-7]+$`)
)

// conditionParseNumber follows the runner's ExpressionUtility.ParseNumber:
// trimmed, empty is zero, decimals allow a sign, point and exponent (so
// "022" is 22), 0x and 0o prefixes are signed 32-bit patterns, then Infinity.
func conditionParseNumber(text string) float64 {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return 0
	case conditionDecimal.MatchString(text):
		value, _ := strconv.ParseFloat(text, 64) // out of range parses as ±Inf, as in .NET
		return value
	case conditionHex.MatchString(text):
		// Int32.TryParse with AllowHexSpecifier reads a 32-bit pattern, so
		// 0xffffffff is -1.
		if value, err := strconv.ParseUint(text[2:], 16, 32); err == nil {
			return float64(int32(uint32(value)))
		}
	case conditionOctal.MatchString(text):
		// Convert.ToInt32(text, 8) also reads a 32-bit pattern.
		if value, err := strconv.ParseUint(text[2:], 8, 32); err == nil {
			return float64(int32(uint32(value)))
		}
	case text == "Infinity":
		return math.Inf(1)
	case text == "-Infinity":
		return math.Inf(-1)
	}
	return math.NaN()
}

// A job-level branch guard applies equally to installation and all consumers.
// Enumerate that job as a possible CI path; do not assume the current branch.
// Step-level guards still require full evaluation so a conditional install
// cannot incorrectly certify an unconditionally executed test.
var branchJobGuard = regexp.MustCompile(`^github\.ref\s*(?:==|!=)\s*'refs/(?:heads|tags)/[^'\s]+'$`)

var forkJobOwner = regexp.MustCompile("^[A-Za-z0-9-]+$")

func possibleJobCondition(value string, row map[string]any) (bool, error) {
	active, _, err := possibleJobConditionPath(value, row)
	return active, err
}

// Event, job-output, input and repository-variable values are unknown statically.
var runtimeContextOperand = regexp.MustCompile(`\b(?:github|needs|inputs|vars)\.[A-Za-z0-9_.-]+`)

var conditionLiteral = regexp.MustCompile(`'((?:[^']|'')*)'`)

// possibleJobConditionPath reports whether some CI path can run the job. A job
// guard that reads only runtime context (actor, event, other jobs' outputs) gates
// the whole job, never which command runs or how it is instrumented. It is a
// possible path when some value of that context passes; assumed reports that the
// guard itself remains unverified.
func possibleJobConditionPath(value string, row map[string]any) (active, assumed bool, err error) {
	active, err = knownJobCondition(value, row)
	if err == nil {
		return active, false, nil
	}
	operands := runtimeContextOperand.FindAllString(value, -1)
	slices.Sort(operands)
	operands = slices.Compact(operands)
	candidates := []string{"", "true", "false"}
	for _, match := range conditionLiteral.FindAllStringSubmatch(value, -1) {
		literal := strings.ReplaceAll(match[1], "''", "'")
		candidates = append(candidates, literal)
		// contains(fromJSON('["push", …]'), github.event_name) passes for an
		// element of the array, never for the JSON text itself. Numbers are
		// sampled as their text, which loose equality matches against them.
		var elements []any
		if json.Unmarshal([]byte(literal), &elements) == nil {
			for _, element := range elements {
				switch element.(type) {
				case string, float64:
					text, _ := conditionString(element)
					candidates = append(candidates, text)
				}
			}
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	// Bound the combinations tried, not the candidates, so one operand can
	// sample a long membership array (8 candidates for each of 3 operands).
	combinations := 1
	for range operands {
		combinations *= len(candidates)
	}
	if len(operands) == 0 || len(operands) > 3 || combinations > 512 {
		return false, false, err
	}
	env := map[string]string{}
	var try func(int) (bool, error)
	try = func(i int) (bool, error) {
		if i == len(operands) {
			return runtimeCondition(value, row, env)
		}
		for _, candidate := range candidates {
			env["\x00"+operands[i]] = candidate
			passed, err := try(i + 1)
			if err != nil || passed {
				return passed, err
			}
		}
		return false, nil
	}
	if passed, tryErr := try(0); tryErr == nil && passed {
		return true, true, nil
	}
	// The sampled values are not exhaustive (a run number can exceed any
	// literal), so a failed search only excludes the job when the guard is false
	// for every outcome of its runtime-context comparisons.
	possible, abstractErr := possibleRuntimeOutcome(value, row)
	if abstractErr != nil {
		return false, false, err
	}
	return possible, possible, nil
}

var runtimeComparison = regexp.MustCompile(`(?:\b(?:github|needs|inputs|vars)\.[A-Za-z0-9_.-]+\s*(?:==|!=|<=|>=|<|>)\s*(?:'(?:[^']|'')*'|-?[0-9]+(?:\.[0-9]+)?|true|false|null|\b(?:github|needs|inputs|vars)\.[A-Za-z0-9_.-]+)|(?:'(?:[^']|'')*'|-?[0-9]+(?:\.[0-9]+)?|true|false|null)\s*(?:==|!=|<=|>=|<|>)\s*\b(?:github|needs|inputs|vars)\.[A-Za-z0-9_.-]+)`)

// possibleRuntimeOutcome treats each runtime-context comparison (and each bare
// runtime operand) as independently true or false. Correlated comparisons can
// only make fewer outcomes reachable, so a guard false for every assignment is
// impossible; any passing assignment leaves the job a possible CI path.
func possibleRuntimeOutcome(value string, row map[string]any) (bool, error) {
	atoms := 0
	abstract := runtimeComparison.ReplaceAllStringFunc(value, func(string) string {
		atoms++
		return fmt.Sprintf("\x00%d\x00", atoms-1)
	})
	// A bare operand can only stand for a true/false outcome where it is used as
	// a condition on its own. As a function argument or comparison operand it is
	// an unknown value, and substituting a boolean would misjudge the guard.
	var misplaced bool
	operands := runtimeContextOperand.FindAllStringIndex(abstract, -1)
	for i := len(operands) - 1; i >= 0; i-- {
		start, end := operands[i][0], operands[i][1]
		if !conditionOperandPosition(abstract, start, end) {
			misplaced = true
			break
		}
		atoms++
		abstract = abstract[:start] + fmt.Sprintf("\x00%d\x00", atoms-1) + abstract[end:]
	}
	if misplaced {
		return false, fmt.Errorf("runtime context value is used as an operand, not a condition")
	}
	if atoms == 0 || atoms > 6 {
		return false, fmt.Errorf("runtime condition has %d context comparisons", atoms)
	}
	for assignment := 0; assignment < 1<<atoms; assignment++ {
		condition := abstract
		for atom := range atoms {
			outcome := "false"
			if assignment&(1<<atom) != 0 {
				outcome = "true"
			}
			condition = strings.ReplaceAll(condition, fmt.Sprintf("\x00%d\x00", atom), outcome)
		}
		passed, err := runtimeCondition(condition, row)
		if err != nil {
			return false, err
		}
		if passed {
			return true, nil
		}
	}
	return false, nil
}

// conditionOperandPosition reports whether text[start:end] is used as a whole
// condition: bounded by the expression edges, !, &&, || or grouping
// parentheses, and every enclosing group is itself used that way. A function
// call, comma or comparison anywhere around it makes it a value instead.
func conditionOperandPosition(text string, start, end int) bool {
	open, close := conditionEnclosingGroup(text, start)
	before := strings.TrimRightFunc(text[:start], unicode.IsSpace)
	after := strings.TrimLeftFunc(text[end:], unicode.IsSpace)
	before = strings.TrimRightFunc(strings.TrimSuffix(before, "${{"), unicode.IsSpace)
	switch {
	case before == "", strings.HasSuffix(before, "&&"), strings.HasSuffix(before, "||"),
		strings.HasSuffix(before, "!") && !strings.HasSuffix(before, "!="),
		strings.HasSuffix(before, "(") && open == len(before)-1:
	default:
		return false
	}
	switch {
	case after == "", after == "}}", strings.HasPrefix(after, "&&"), strings.HasPrefix(after, "||"),
		strings.HasPrefix(after, ")") && close == len(text)-len(after):
	default:
		return false
	}
	if open < 0 {
		return true
	}
	// A parenthesis after an identifier opens a function call.
	head := strings.TrimRightFunc(text[:open], unicode.IsSpace)
	if head != "" {
		if last := head[len(head)-1]; last == '_' || last == '.' || last >= '0' && last <= '9' || last >= 'a' && last <= 'z' || last >= 'A' && last <= 'Z' {
			return false
		}
	}
	if close < 0 {
		return false
	}
	return conditionOperandPosition(text, open, close+1)
}

// conditionEnclosingGroup returns the parentheses that most closely enclose
// position, skipping quoted literals, or -1 when there are none.
func conditionEnclosingGroup(text string, position int) (open, close int) {
	var stack []int
	open, close = -1, -1
	quoted := false
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\'':
			quoted = !quoted
		case quoted:
		case text[i] == '(':
			stack = append(stack, i)
		case text[i] == ')' && len(stack) > 0:
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if at < position && i >= position && at > open {
				open, close = at, i
			}
		}
	}
	return open, close
}

func knownJobCondition(value string, row map[string]any) (bool, error) {
	condition := strings.TrimSpace(value)
	if strings.HasPrefix(condition, "${{") && strings.HasSuffix(condition, "}}") {
		condition = strings.TrimSpace(condition[3 : len(condition)-2])
	}
	// These job-wide status guards admit an ordinary successful CI path.
	// Step guards remain strict so conditional setup cannot certify another step.
	if condition == "!cancelled()" || condition == "success()" || condition == "always()" {
		return true, nil
	}
	if branchJobGuard.MatchString(condition) {
		return true, nil
	}
	// This common fork guard admits an upstream push. It does not exclude any
	// matrix row; step conditions and instrumentation checks remain strict.
	if rest, ok := strings.CutPrefix(condition, "github.repository_owner == '"); ok {
		owner, tail, found := strings.Cut(rest, "'")
		if found && forkJobOwner.MatchString(owner) &&
			tail == " && (github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository)" {
			return true, nil
		}
	}
	return runtimeCondition(value, row)
}

func conditionString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case nil:
		return "", true
	case bool:
		return strconv.FormatBool(v), true
	case int:
		return conditionString(float64(v))
	case float64:
		// The runner formats numbers with .NET's "G15".
		switch {
		case math.IsNaN(v):
			return "NaN", true
		case math.IsInf(v, 1):
			return "Infinity", true
		case math.IsInf(v, -1):
			return "-Infinity", true
		}
		return strconv.FormatFloat(v, 'G', 15, 64), true
	default:
		return "", false
	}
}
