package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

func setupPreload(framework string) string {
	if framework == "vitest" || framework == "cucumber" || framework == "webdriverio" {
		return vitestPreload
	}
	return "${{ env.NODE_OPTIONS }} -r ${{ env.DD_TRACE_PACKAGE }}"
}

func setupNodeEnd(node *yaml.Node) int {
	end := node.Line + strings.Count(strings.TrimSuffix(node.Value, "\n"), "\n")
	if node.Style == yaml.LiteralStyle || node.Style == yaml.FoldedStyle {
		end++
	}
	for _, child := range node.Content {
		end = max(end, setupNodeEnd(child))
	}
	return end
}

func indentSetup(text string, indent int) string {
	return strings.Repeat(" ", indent) + strings.ReplaceAll(strings.TrimSuffix(text, "\n"), "\n", "\n"+strings.Repeat(" ", indent)) + "\n"
}

func setupStepEnvironment(lines []string, step *yaml.Node, key, preload string, installed bool) (*lineInsertion, error) {
	env := setupMember(step, "env")
	if env == nil {
		line := lines[step.Line-1]
		indent := len(line) - len(strings.TrimLeft(line, " ")) + 2
		return &lineInsertion{line: setupMember(step, "run").Line, text: strings.Repeat(" ", indent) + "env:\n" + strings.Repeat(" ", indent+2) + key + ": " + preload + "\n"}, nil
	}
	if env.Kind != yaml.MappingNode || env.Style != 0 {
		return nil, fmt.Errorf("inline or non-mapping step environment requires explicit initialization at line %d", env.Line)
	}
	options := setupMember(env, key)
	if options != nil {
		if installed && (options.Value == preload || strings.HasSuffix(options.Value, " "+strings.TrimPrefix(preload, "${{ env.NODE_OPTIONS }} "))) {
			return nil, nil
		}
		if strings.Contains(options.Value, "${{") || strings.ContainsAny(options.Value, "$`\n") {
			return nil, fmt.Errorf("dynamic NODE_OPTIONS requires review at line %d", options.Line)
		}
		preload = options.Value + " " + strings.TrimPrefix(preload, "${{ env.NODE_OPTIONS }} ")
		options.Value, options.Style = preload, yaml.DoubleQuotedStyle
	} else {
		env.Content = append(env.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Value: preload})
	}
	text, err := yaml.Marshal(env)
	if err != nil {
		return nil, err
	}
	indent := len(lines[step.Line-1]) - len(strings.TrimLeft(lines[step.Line-1], " ")) + 2
	return &lineInsertion{line: env.Line - 1, remove: setupNodeEnd(env) - env.Line + 1, text: indentSetup(string(text), indent+2)}, nil
}

func setupInstrumentedStep(node *yaml.Node, preload, run, condition string) (string, error) {
	data, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	var copy yaml.Node
	if err := yaml.Unmarshal(data, &copy); err != nil {
		return "", err
	}
	step := copy.Content[0]
	setupMember(step, "run").Value = run
	step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "if"}, &yaml.Node{Kind: yaml.ScalarNode, Value: condition})
	if setupMember(step, "env") != nil {
		return "", fmt.Errorf("partitioned step has a custom environment; merge its hooks explicitly")
	}
	step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "env"}, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "NODE_OPTIONS"}, {Kind: yaml.ScalarNode, Value: preload}}})
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{step}}
	data, err = yaml.Marshal(sequence)
	return string(data), err
}

// Add NYC only to statically understood Mocha leaves. The original legacy
// command remains untouched when the runtime plan partitions instrumentation.
func setupMochaCoverage(root, framework, command string) (string, error) {
	if framework != "mocha" {
		return command, nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", err
	}
	var offsets []int
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		text := shellText(call)
		remaining := 256
		resolution := resolveJestSequence(root, root, text, nil, &remaining, "mocha")
		if len(resolution.Tests) == 1 && !resolution.Tests[0].usesNYC {
			offsets = append(offsets, int(call.Pos().Offset()))
		}
		return false
	})
	if len(offsets) > 0 {
		var pkg struct {
			Dependencies    map[string]string
			DevDependencies map[string]string
		}
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Dependencies["nyc"] == "" && pkg.DevDependencies["nyc"] == "" {
			return "", fmt.Errorf("cI source coverage needs an existing NYC dependency; add nyc with the repository package manager before resuming")
		}
	}
	slices.Sort(offsets)
	for _, offset := range slices.Backward(offsets) {
		command = command[:offset] + "npx --no-install nyc -- " + command[offset:]
	}
	return command, nil
}
