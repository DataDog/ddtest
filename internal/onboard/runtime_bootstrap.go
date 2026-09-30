// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

// NUL cannot occur in an environment value. Reject it in source expressions and
// matrix values so only a known action reference can produce these symbols.
const actionPreload = "\x00DD_ACTION_PRELOAD\x00"

func symbolicConditionValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.ContainsRune(text, 0)
}

// Called only after an active JavaScript action. Resolve its known references,
// never arbitrary environment values or commands. Scope overrides need review.
func checkJestBootstrap(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, frameworks ...string) string {
	known := map[string]string{"DD_TRACE_PACKAGE": actionPreload, "DD_TRACE_ESM_IMPORT": "\x00DD_ACTION_IMPORT\x00"}
	var options string
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := env["NODE_OPTIONS"]; ok {
			options = value
		}
		for key := range env {
			delete(known, key)
		}
	}
	options, err := bootstrapOptions(options, row, known)
	if err == nil && !strings.ContainsAny(options, "$`") {
		words, err := shellquote.Split(options)
		if err == nil {
			requireFound, importFound := false, false
			for i, word := range words {
				if word == "--require="+actionPreload || ((word == "-r" || word == "--require") && i+1 < len(words) && words[i+1] == actionPreload) {
					requireFound = true
				}
				if word == "--import=\x00DD_ACTION_IMPORT\x00" || (word == "--import" && i+1 < len(words) && words[i+1] == "\x00DD_ACTION_IMPORT\x00") {
					importFound = true
				}
			}
			if requireFound && (len(frameworks) == 0 || frameworks[0] != "vitest" || importFound) {
				return ""
			}
		}
	}
	return "Cannot verify the action's JavaScript preload in this test step's effective NODE_OPTIONS. Preserve custom initialization and report it for review."
}

func bootstrapOptions(value string, row map[string]any, env map[string]string) (string, error) {
	if len(value) > 4096 || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("unsupported NODE_OPTIONS length or NUL byte")
	}
	var result strings.Builder
	for {
		literal, expression, found := strings.Cut(value, "${{")
		result.WriteString(literal)
		if !found {
			break
		}
		p := conditionParser{rest: expression, row: row, env: env}
		resolved, err := p.expression(0)
		if err != nil || !p.take("}}") {
			return "", fmt.Errorf("unsupported NODE_OPTIONS expression")
		}
		text, ok := resolved.(string)
		if !ok {
			return "", fmt.Errorf("NODE_OPTIONS expression must resolve to a string")
		}
		result.WriteString(text)
		if result.Len() > 4096 {
			return "", fmt.Errorf("resolved NODE_OPTIONS exceeds 4096 characters")
		}
		value = p.rest
	}
	if result.Len() > 4096 {
		return "", fmt.Errorf("resolved NODE_OPTIONS exceeds 4096 characters")
	}
	return result.String(), nil
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
