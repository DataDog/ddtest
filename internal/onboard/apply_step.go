package onboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

func setupIndent(lines []string, node *yaml.Node) int {
	line := lines[node.Line-1]
	dash := len(line) - len(strings.TrimLeft(line, " "))
	return node.Content[0].Column - 1 - dash
}

// setupNestedIndent is the indentation of mappings nested in a step, such as
// env or with. Files can pair "- key:" items with deeper nested mappings, so
// it follows the step's own children, then the job's other steps and the
// workflow's job mappings; the dash width is only the last resort.
func setupNestedIndent(step, steps, jobs *yaml.Node, width int) int {
	for _, candidate := range append([]*yaml.Node{step}, steps.Content...) {
		if indent := nestedMappingIndent(candidate); indent > 0 {
			return indent
		}
	}
	if indent := nestedMappingIndent(jobs); indent > 0 {
		return indent
	}
	return width
}

func nestedMappingIndent(node *yaml.Node) int {
	if node == nil || node.Kind != yaml.MappingNode {
		return 0
	}
	for i := 1; i < len(node.Content); i += 2 {
		key, value := node.Content[i-1], node.Content[i]
		if value.Kind == yaml.MappingNode && value.Style == 0 && len(value.Content) > 0 && value.Content[0].Line > key.Line {
			return value.Content[0].Column - key.Column
		}
	}
	return 0
}

// setupComments counts the comment-only lines of the source range a step
// replaces. yaml.v3 can attach a comment just outside that range, such as a
// trailing foot comment, to the step; that line stays in place, so encoding
// keeps a node comment only while the range still holds its lines.
type setupComments map[string]int

func newSetupComments(lines []string) setupComments {
	comments := setupComments{}
	for _, line := range lines {
		if text := strings.TrimSpace(line); strings.HasPrefix(text, "#") {
			comments[text]++
		}
	}
	return comments
}

func (c setupComments) keep(comment string) bool {
	need := map[string]int{}
	for _, line := range strings.Split(comment, "\n") {
		if text := strings.TrimSpace(line); text != "" {
			need[text]++
		}
	}
	for text, count := range need {
		if c[text] < count {
			return false
		}
	}
	for text, count := range need {
		c[text] -= count
	}
	return true
}

func (c setupComments) prune(node *yaml.Node) {
	if !c.keep(node.HeadComment) {
		node.HeadComment = ""
	}
	for _, child := range node.Content {
		c.prune(child)
	}
	if !c.keep(node.FootComment) {
		node.FootComment = ""
	}
}

// Replace one complete sequence item so run scalars and env cannot overlap.
func setupStepText(node *yaml.Node, indent, nested int, comments setupComments, key, preload, command, condition string, installed bool) (string, error) {
	data, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	var document yaml.Node
	if err = yaml.Unmarshal(data, &document); err != nil {
		return "", err
	}
	step := document.Content[0]
	// The round trip quotes values the encoder escapes; keep the source styles
	// so restoreEscapedScalars can write those values back as they were.
	copyScalarStyles(step, node)
	setupMember(step, "run").Value = command
	if condition != "" {
		if setupMember(step, "env") != nil {
			return "", fmt.Errorf("partitioned step has a custom environment; merge its hooks explicitly")
		}
		step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "if"}, &yaml.Node{Kind: yaml.ScalarNode, Value: condition})
	}
	env := setupMember(step, "env")
	if env == nil {
		env = &yaml.Node{Kind: yaml.MappingNode}
		step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "env"}, env)
	}
	if env.Kind != yaml.MappingNode || env.Style != 0 {
		return "", fmt.Errorf("inline or non-mapping step environment requires explicit initialization")
	}
	options := setupMember(env, key)
	if options != nil {
		if installed && strings.HasPrefix(options.Value, "${{ env.DD_TRACE_PACKAGE && format(") {
			return encodeSetupStep(step, indent, nested, comments)
		}
		if installed && (options.Value == preload || strings.HasSuffix(options.Value, " "+preload)) {
			options.Value, options.Style = setupGuardedPreload(options.Value), yaml.DoubleQuotedStyle
			return encodeSetupStep(step, indent, nested, comments)
		}
		if strings.Contains(options.Value, "${{") || strings.ContainsAny(options.Value, "$`\n") {
			return "", fmt.Errorf("dynamic %s requires review", key)
		}
		preload = options.Value + " " + preload
		options.Value, options.Style = setupGuardedPreload(preload), yaml.DoubleQuotedStyle
	} else {
		env.Content = append(env.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Value: setupGuardedPreload(preload), Style: yaml.DoubleQuotedStyle})
	}
	return encodeSetupStep(step, indent, nested, comments)
}

// encodeSetupStep writes one sequence item: keys follow the dash at indent,
// and nested mappings use the file's own nested indentation.
func encodeSetupStep(step *yaml.Node, indent, nested int, comments setupComments) (string, error) {
	comments.prune(step)
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(nested)
	if err := encoder.Encode(step); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	encoded := restoreEscapedScalars(out.String(), step)
	var result strings.Builder
	first := true
	for _, line := range strings.SplitAfter(encoded, "\n") {
		if line == "" {
			continue
		}
		if strings.TrimSpace(line) == "" {
			// Never indent an empty line into a whitespace-only one.
			result.WriteString("\n")
			continue
		}
		if first && strings.HasPrefix(line, "#") {
			result.WriteString(line)
			continue
		}
		if first {
			result.WriteString("-" + strings.Repeat(" ", indent-1) + line)
			first = false
		} else {
			result.WriteString(strings.Repeat(" ", indent) + line)
		}
	}
	return result.String(), nil
}

func setupStepStart(node *yaml.Node, lines []string) int {
	start := node.Line - 1
	comment := node.HeadComment
	if comment == "" && len(node.Content) > 0 {
		comment = node.Content[0].HeadComment
	}
	if comment != "" {
		parts := strings.Split(comment, "\n")
		for i := len(parts) - 1; i >= 0; i-- {
			part := parts[i]
			if start > 0 && strings.TrimSpace(lines[start-1]) == strings.TrimSpace(part) {
				start--
			}
		}
	}
	return start
}

func setupJSON(value string) []byte {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// The action can fail when fork/Dependabot runs have no secret. Keep the
// customer's options and test command usable when the action exported no tracer.
func setupGuardedPreload(options string) string {
	const require = "-r ${{ env.DD_TRACE_PACKAGE }}"
	const imports = "--import ${{ env.DD_TRACE_ESM_IMPORT }}"
	fallback := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(options, require, ""), imports, ""))
	template := strings.ReplaceAll(strings.ReplaceAll(options, require, "-r \x00PACKAGE\x00"), imports, "--import \x00IMPORT\x00")
	template = strings.ReplaceAll(strings.ReplaceAll(template, "{", "{{"), "}", "}}")
	template = strings.ReplaceAll(strings.ReplaceAll(template, "\x00PACKAGE\x00", "{0}"), "\x00IMPORT\x00", "{1}")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	arguments := ", env.DD_TRACE_PACKAGE"
	if strings.Contains(options, imports) {
		arguments += ", env.DD_TRACE_ESM_IMPORT"
	}
	return "${{ env.DD_TRACE_PACKAGE && format(" + quote(template) + arguments + ") || " + quote(fallback) + " }}"
}

const setupActionV3 = "datadog/test-visibility-github-action@7ce545f228901e4ac4ef0b45d946120f1c4124e0" // v3.3.0

// pinSetupAction replaces the floating v3 reference in manual guidance with
// the verified commit setup writes, so both paths install the same action.
func pinSetupAction(text string) string {
	return strings.NewReplacer(
		"uses: datadog/test-visibility-github-action@v3\n", "uses: "+setupActionV3+" # v3.3.0\n",
		"`datadog/test-visibility-github-action@v3`", "`"+setupActionV3+"` (v3.3.0)",
	).Replace(text)
}

// simpleStepCondition reports whether a test step's if: reads only matrix and
// env values through comparisons and logical operators. Setup copies such a
// guard onto the action, so the tracer installs exactly where those tests run;
// job status functions and other contexts keep the step outside the applier.
func simpleStepCondition(condition string) bool {
	expression := strings.TrimSpace(condition)
	if strings.HasPrefix(expression, "${{") {
		if !strings.HasSuffix(expression, "}}") {
			return false
		}
		expression = strings.TrimSpace(expression[3 : len(expression)-2])
	}
	if expression == "" || strings.Contains(expression, "${{") {
		return false
	}
	reference := false
	for i := 0; i < len(expression); {
		c := expression[i]
		switch {
		case c == ' ' || c == '(' || c == ')':
			i++
		case strings.HasPrefix(expression[i:], "&&") || strings.HasPrefix(expression[i:], "||") || strings.HasPrefix(expression[i:], "==") ||
			strings.HasPrefix(expression[i:], "!=") || strings.HasPrefix(expression[i:], "<=") || strings.HasPrefix(expression[i:], ">="):
			i += 2
		case c == '!' || c == '<' || c == '>':
			i++
		case c == '\'':
			end := strings.IndexByte(expression[i+1:], '\'')
			if end < 0 {
				return false
			}
			i += end + 2
		default:
			j := i
			for j < len(expression) && (expression[j] == '.' || expression[j] == '_' || expression[j] == '-' ||
				'a' <= expression[j] && expression[j] <= 'z' || 'A' <= expression[j] && expression[j] <= 'Z' || '0' <= expression[j] && expression[j] <= '9') {
				j++
			}
			word := expression[i:j]
			if word == "" || j < len(expression) && expression[j] == '(' {
				return false // function calls such as success() or contains()
			}
			switch {
			case strings.HasPrefix(word, "matrix.") || strings.HasPrefix(word, "env."):
				reference = true
			case word == "true" || word == "false" || word == "null":
			default:
				if _, err := strconv.ParseFloat(word, 64); err != nil {
					return false
				}
			}
			i = j
		}
	}
	return reference
}

// CodeIncompleteCISetup marks an instrumented CI entry whose Datadog action is
// not a complete, supported setup for the selected site.
const CodeIncompleteCISetup = "incomplete_ci_setup"

const defaultDatadogSite = "datadoghq.com"

// These immutable releases were verified against the action's published v3 tags.
func setupActionSupported(action string) bool {
	switch strings.TrimPrefix(strings.ToLower(action), "datadog/test-visibility-github-action@") {
	case "v3", "v3.3.0", "v3.2.0", "v3.1.0", "v3.0.0",
		"7ce545f228901e4ac4ef0b45d946120f1c4124e0", "1abae93083b69778facd7ee11f89184306c999e6", "d7e3562370d3742851764531da3cb54a743ebd63", "d6d648d87563e713b3d396bb3bfebb5e3cc93707":
		return true
	}
	return false
}

// escapedScalar matches a double-quoted scalar the encoder wrote with \u or \U
// escapes, as it does for emoji and other characters outside its printable set.
var escapedScalar = regexp.MustCompile(`"(?:[^"\\]|\\.)*\\[uU][0-9A-Fa-f]{4,8}(?:[^"\\]|\\.)*"`)

// restoreEscapedScalars writes back, in their original style, the scalars the
// encoder escaped only because of their characters (a step named "🧪 Test"
// became "\U0001F9EA Test"). ddtest re-encodes a step to insert its own keys;
// the customer's other values must stay as they were written.
func restoreEscapedScalars(encoded string, step *yaml.Node) string {
	styles := map[string]yaml.Style{}
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.ScalarNode {
			if _, seen := styles[node.Value]; !seen {
				styles[node.Value] = node.Style
			}
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(step)
	return escapedScalar.ReplaceAllStringFunc(encoded, func(token string) string {
		value, err := strconv.Unquote(token)
		if err != nil {
			return token
		}
		style, ok := styles[value]
		if !ok {
			return token
		}
		switch style {
		case 0:
			return value
		case yaml.SingleQuotedStyle:
			return "'" + strings.ReplaceAll(value, "'", "''") + "'"
		}
		return token
	})
}

// copyScalarStyles copies scalar styles from src onto the same scalars of its
// re-parsed copy dst.
func copyScalarStyles(dst, src *yaml.Node) {
	if dst.Kind != src.Kind || len(dst.Content) != len(src.Content) {
		return
	}
	if dst.Kind == yaml.ScalarNode && dst.Value == src.Value {
		dst.Style = src.Style
	}
	for i := range dst.Content {
		copyScalarStyles(dst.Content[i], src.Content[i])
	}
}
