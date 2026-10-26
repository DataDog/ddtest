// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/kballard/go-shellquote"
)

// NUL cannot occur in an environment value. Reject it in source expressions and
// matrix values so only a known action reference can produce these symbols.
const actionPreload = "\x00DD_ACTION_PRELOAD\x00"

func symbolicConditionValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.ContainsRune(text, 0)
}

func checkTestBootstrap(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, commands []TestCommand, frameworks ...string) string {
	runnerOnly := len(commands) > 0 && len(frameworks) > 0 && frameworks[0] == "vitest"
	for _, command := range commands {
		words, err := shellquote.Split(command.Command)
		runnerOnly = runnerOnly && err == nil && len(words) > 0 && IsDDTestCommand(words[0])
	}
	if runnerOnly {
		for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
			for _, key := range slices.Sorted(maps.Keys(env)) {
				if _, ok := VitestRunnerEnvironmentFlag(key); ok {
					continue
				}
				if strings.HasPrefix(key, "DD_TEST_OPTIMIZATION_RUNNER_") || key == "DD_TRACE_PACKAGE" || key == "DD_TRACE_ESM_IMPORT" || key == "DD_TRACE_ENABLED" || key == "DD_CIVISIBILITY_ENABLED" {
					return "Custom ddtest worker instrumentation requires review: " + key
				}
			}
		}
		options, err := effectiveBootstrapOptions(workflow, job, step, row, true)
		if err != nil {
			return "Cannot resolve ddtest worker NODE_OPTIONS: " + err.Error()
		}
		words, err := environment.SplitNodeOptions(options)
		if err != nil {
			return "Cannot parse ddtest worker NODE_OPTIONS: " + err.Error()
		}
		for i := 0; i < len(words); i++ {
			key, module, inline := strings.Cut(words[i], "=")
			preload := key == "-r" || key == "--require" || key == "--import" || key == "--loader" || key == "--experimental-loader"
			if strings.HasPrefix(key, "-r") && len(key) > 2 {
				key, module, inline = "-r", key[2:], true
				preload = true
			}
			if !preload {
				continue
			}
			if !inline {
				i++
				if i >= len(words) {
					return "Missing ddtest worker NODE_OPTIONS module"
				}
				module = words[i]
			}
			if (key == "-r" || key == "--require") && module == actionPreload || key == "--import" && module == "\x00DD_ACTION_IMPORT\x00" {
				continue
			}
			return "Custom ddtest worker preload requires review. Use the action's DD_TRACE_PACKAGE/DD_TRACE_ESM_IMPORT exports; local replacement does not prove this CI module resolves: " + module
		}
		return ""
	}
	esm := len(frameworks) > 0 && (frameworks[0] == "vitest" || frameworks[0] == "cucumber")
	if reason := checkJestBootstrap(workflow, job, step, row, esm); reason != "" {
		return reason
	}
	if len(frameworks) > 0 && frameworks[0] == "mocha" {
		for _, command := range commands {
			if command.mochaESM && checkJestBootstrap(workflow, job, step, row, true) != "" {
				return "Mocha ESM inputs require the action's --import ${{ env.DD_TRACE_ESM_IMPORT }} alongside its require preload"
			}
			if !command.usesNYC && !command.mochaIstanbul {
				reason := MochaCoverageGap + command.Command + ". Wrap the original command with npx --no-install nyc --, adding nyc as a dev dependency if the project does not declare it; real source coverage is checked separately."
				if words, err := shellquote.Split(command.Command); err == nil && len(words) > 0 && filepath.Base(words[0]) == "c8" {
					reason = "Mocha CI command uses c8: " + command.Command + ". c8's V8 coverage is not reported per test; wrap the Mocha command with npx --no-install nyc -- instead."
				}
				return reason
			}
		}
	}
	return ""
}

// MochaCoverageGap starts the finding for a Mocha command without NYC or
// recognized Istanbul instrumentation; automatic setup can add the NYC wrapper.
const MochaCoverageGap = "Cannot verify Istanbul/NYC coverage setup for Mocha CI command: "

// checkJestBootstrap verifies the action's require preload in the step's
// effective NODE_OPTIONS; requireImport also requires its ESM --import.
func checkJestBootstrap(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, requireImport bool) string {
	options, err := effectiveBootstrapOptions(workflow, job, step, row, true)
	if err == nil && !strings.ContainsAny(options, "$`'") {
		words, err := shellquote.Split(options)
		if err == nil {
			requireFound, importFound := false, false
			for i, word := range words {
				if word == "--import=\x00DD_ACTION_IMPORT\x00" || (word == "--import" && i+1 < len(words) && words[i+1] == "\x00DD_ACTION_IMPORT\x00") {
					importFound = true
				}
				if word == "--require="+actionPreload || ((word == "-r" || word == "--require") && i+1 < len(words) && words[i+1] == actionPreload) {
					requireFound = true
				}
			}
			if requireFound && (!requireImport || importFound) {
				return ""
			}
		}
	}
	return unverifiedPreloadReason
}

const unverifiedPreloadReason = "Cannot verify the action's JavaScript preload in this test step's effective NODE_OPTIONS. Preserve custom initialization and report it for review."

// stepSetsNoNodeOptions reports whether no workflow, job or step layer sets
// NODE_OPTIONS, so adding the action's preload overrides nothing custom.
func stepSetsNoNodeOptions(workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if _, ok := env["NODE_OPTIONS"]; ok {
			return false
		}
	}
	return true
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

// Resolve expressions with action outputs present only on instrumented entries.
func effectiveBootstrapOptions(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, actionRan bool) (string, error) {
	known := map[string]string{"NODE_OPTIONS": "", "DD_TRACE_PACKAGE": actionPreload, "DD_TRACE_ESM_IMPORT": "\x00DD_ACTION_IMPORT\x00"}
	if !actionRan {
		known["DD_TRACE_PACKAGE"], known["DD_TRACE_ESM_IMPORT"] = "", ""
	}
	var options string
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := env["NODE_OPTIONS"]; ok {
			options = value
		}
		for key, value := range env {
			if key == "NODE_OPTIONS" {
				if !strings.Contains(value, "${{") && !strings.ContainsRune(value, 0) {
					known[key] = value
				}
				continue
			}
			delete(known, key)
			if key != "DD_TRACE_PACKAGE" && key != "DD_TRACE_ESM_IMPORT" && !strings.Contains(value, "${{") && !strings.ContainsRune(value, 0) {
				known[key] = value
			}
		}
	}
	for key, value := range conditionEnvironment(workflow, job, step, row) {
		if key != "NODE_OPTIONS" && key != "DD_TRACE_PACKAGE" && key != "DD_TRACE_ESM_IMPORT" {
			known[key] = value
		}
	}
	return bootstrapOptions(options, row, known)
}

func checkExcludedJestBootstrap(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) string {
	options, err := effectiveBootstrapOptions(workflow, job, step, row, false)
	reason := "Cannot verify initialization is disabled when the Datadog action is skipped; guard installation and its NODE_OPTIONS preload together."
	if err != nil || strings.ContainsAny(options, "$`'") {
		return reason
	}
	words, err := shellquote.Split(options)
	if err != nil {
		return reason
	}
	for _, word := range words {
		key, _, _ := strings.Cut(word, "=")
		if key == "--require" || key == "--import" || key == "--loader" || key == "--experimental-loader" || strings.HasPrefix(word, "-r") {
			return reason
		}
	}
	return ""
}
