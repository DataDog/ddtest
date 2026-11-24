package bazelupload

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type schemaNode struct {
	types       []string
	required    []string
	properties  map[string]*schemaNode
	patterns    []schemaPattern
	additional  bool
	items       *schemaNode
	all, any    []*schemaNode
	condition   *schemaNode
	then, other *schemaNode
	allowed     []any
	minimum     json.Number
	maximum     json.Number
}

type schemaPattern struct {
	expression *regexp.Regexp
	// everything marks a pattern that every key matches, such as the schema's
	// ".*" for meta and metrics keys: an unanchored search finds its empty
	// match in any string.
	everything bool
	node       *schemaNode
}

// compileSchema resolves the small schema subset used by CITESTCYCLE once per
// uploader invocation. Reusing ddtest as a compiler never initializes it.
func compileSchema() (*schemaNode, error) {
	spec, err := decodeObject(testSchemaJSON)
	if err != nil {
		return nil, err
	}
	definitions := object(spec["$defs"])
	compiled := make(map[string]*schemaNode, len(definitions))
	for name := range definitions {
		compiled[name] = new(schemaNode)
	}
	for name, definition := range definitions {
		node, err := compileNode(object(definition), compiled)
		if err != nil {
			return nil, fmt.Errorf("compile schema definition %q: %w", name, err)
		}
		if _, alias := object(definition)["$ref"]; alias {
			return nil, fmt.Errorf("unsupported reference alias in schema definition %q", name)
		}
		// Recursive definitions point into the same set of nodes; validation
		// follows each reference only when it descends into payload data.
		*compiled[name] = *node
	}
	return compileNode(spec, compiled)
}

func compileNode(spec Object, definitions map[string]*schemaNode) (*schemaNode, error) {
	if spec == nil {
		return nil, fmt.Errorf("expected a schema object")
	}
	if ref := stringValue(spec, "$ref"); ref != "" {
		name, ok := strings.CutPrefix(ref, "#/$defs/")
		if !ok || len(spec) != 1 || definitions[name] == nil {
			return nil, fmt.Errorf("unsupported schema reference %q", ref)
		}
		return definitions[name], nil
	}

	node := &schemaNode{additional: true}
	for keyword, value := range spec {
		var err error
		switch keyword {
		case "$schema", "$defs", "title", "description":
		case "type":
			if name, ok := value.(string); ok {
				node.types = []string{name}
			} else {
				node.types, err = schemaStrings(value)
			}
			for _, name := range node.types {
				if !slices.Contains([]string{"object", "array", "string", "number", "integer", "boolean", "null"}, name) {
					return nil, fmt.Errorf("unsupported schema type %q", name)
				}
			}
		case "properties":
			node.properties = make(map[string]*schemaNode)
			for key, property := range object(value) {
				node.properties[key], err = compileNode(object(property), definitions)
				if err != nil {
					break
				}
			}
		case "patternProperties":
			for key, property := range object(value) {
				expr, compileErr := regexp.Compile(key)
				if compileErr != nil {
					return nil, compileErr
				}
				var child *schemaNode
				child, err = compileNode(object(property), definitions)
				if err != nil {
					break
				}
				node.patterns = append(node.patterns, schemaPattern{expr, key == "" || key == ".*", child})
			}
		case "required":
			node.required, err = schemaStrings(value)
		case "additionalProperties":
			var ok bool
			node.additional, ok = value.(bool)
			if !ok {
				return nil, fmt.Errorf("expected boolean additionalProperties")
			}
		case "allOf", "anyOf":
			children, ok := value.([]any)
			if !ok || len(children) == 0 {
				return nil, fmt.Errorf("expected a nonempty %s array", keyword)
			}
			for _, candidate := range children {
				var child *schemaNode
				child, err = compileNode(object(candidate), definitions)
				if err != nil {
					break
				}
				if keyword == "allOf" {
					node.all = append(node.all, child)
				} else {
					node.any = append(node.any, child)
				}
			}
		case "if":
			node.condition, err = compileNode(object(value), definitions)
		case "then":
			node.then, err = compileNode(object(value), definitions)
		case "else":
			node.other, err = compileNode(object(value), definitions)
		case "items":
			node.items, err = compileNode(object(value), definitions)
		case "minimum", "maximum":
			number, ok := value.(json.Number)
			if !ok {
				return nil, fmt.Errorf("expected numeric %s", keyword)
			}
			if keyword == "minimum" {
				node.minimum = number
			} else {
				node.maximum = number
			}
		case "const", "enum":
			if keyword == "const" {
				node.allowed = []any{value}
			} else {
				node.allowed, _ = value.([]any)
			}
			if len(node.allowed) == 0 {
				return nil, fmt.Errorf("expected a nonempty enum array")
			}
			for _, candidate := range node.allowed {
				switch candidate.(type) {
				case string, bool, json.Number, nil:
				default:
					return nil, fmt.Errorf("unsupported %s value %T", keyword, candidate)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported CITESTCYCLE schema keyword %q", keyword)
		}
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

func schemaStrings(value any) ([]string, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a schema string array")
	}
	result := make([]string, len(list))
	for index, item := range list {
		result[index], ok = item.(string)
		if !ok {
			return nil, fmt.Errorf("expected a schema string at index %d", index)
		}
	}
	return result, nil
}

func (node *schemaNode) accepts(value any) bool {
	for _, child := range node.all {
		if !child.accepts(value) {
			return false
		}
	}
	if len(node.any) != 0 && !slices.ContainsFunc(node.any, func(child *schemaNode) bool { return child.accepts(value) }) {
		return false
	}
	if node.condition != nil {
		child := node.other
		if node.condition.accepts(value) {
			child = node.then
		}
		if child != nil && !child.accepts(value) {
			return false
		}
	}

	kind := "null"
	switch item := value.(type) {
	case Object:
		kind = "object"
	case []any:
		kind = "array"
	case string:
		kind = "string"
	case bool:
		kind = "boolean"
	case json.Number:
		kind = "number"
		if !strings.ContainsAny(item.String(), ".eE") && slices.Contains(node.types, "integer") {
			kind = "integer"
		}
		if node.minimum != "" && compareNumber(item, node.minimum) < 0 ||
			node.maximum != "" && compareNumber(item, node.maximum) > 0 {
			return false
		}
	}
	if len(node.types) != 0 && !slices.Contains(node.types, kind) {
		return false
	}
	if len(node.allowed) != 0 && !slices.ContainsFunc(node.allowed, func(candidate any) bool {
		if number, ok := candidate.(json.Number); ok {
			actual, numeric := value.(json.Number)
			return numeric && compareNumber(actual, number) == 0
		}
		return candidate == value
	}) {
		return false
	}
	switch item := value.(type) {
	case Object:
		for _, key := range node.required {
			if _, exists := item[key]; !exists {
				return false
			}
		}
		for key, property := range item {
			child, found := node.properties[key]
			if found && !child.accepts(property) {
				return false
			}
			for _, pattern := range node.patterns {
				if pattern.everything || pattern.expression.MatchString(key) {
					found = true
					if !pattern.node.accepts(property) {
						return false
					}
				}
			}
			if !found && !node.additional {
				return false
			}
		}
	case []any:
		if node.items != nil {
			for _, element := range item {
				if !node.items.accepts(element) {
					return false
				}
			}
		}
	}
	return true
}

func compareNumber(left, right json.Number) int {
	a, b := left.String(), right.String()
	if !strings.ContainsAny(a, ".eE") && !strings.ContainsAny(b, ".eE") {
		// Decimal comparison avoids rounding uint64 IDs through float64 and
		// never allocates a big integer on the validation hot path.
		a, negativeA := strings.CutPrefix(a, "-")
		b, negativeB := strings.CutPrefix(b, "-")
		negativeA = negativeA && a != "0"
		negativeB = negativeB && b != "0"
		if negativeA != negativeB {
			if negativeA {
				return -1
			}
			return 1
		}
		result := len(a) - len(b)
		if result == 0 {
			result = strings.Compare(a, b)
		}
		if negativeA {
			result = -result
		}
		return result
	}
	av, _ := left.Float64()
	bv, _ := right.Float64()
	if av < bv {
		return -1
	}
	if av > bv {
		return 1
	}
	return 0
}
