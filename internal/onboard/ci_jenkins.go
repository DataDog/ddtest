// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Groovy is executable code. This lexer reads literal Pipeline steps without
// evaluating it; computed calls, control flow and libraries stay explicit gaps.
type groovyToken struct {
	value, quote string
	start, end   int
}

func groovyTokens(source string) ([]groovyToken, error) {
	var tokens []groovyToken
	for i := 0; i < len(source); {
		if unicode.IsSpace(rune(source[i])) {
			i++
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			if end := strings.IndexByte(source[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				break
			}
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated Groovy comment")
			}
			i += end + 4
			continue
		}
		start := i
		if source[i] == '\'' || source[i] == '"' {
			quote := source[i : i+1]
			if strings.HasPrefix(source[i:], strings.Repeat(quote, 3)) {
				quote = strings.Repeat(quote, 3)
			}
			i += len(quote)
			var value strings.Builder
			closed := false
			for i < len(source) {
				if strings.HasPrefix(source[i:], quote) {
					i += len(quote)
					closed = true
					break
				}
				if source[i] == '\\' && i+1 < len(source) {
					switch source[i+1] {
					case 'n':
						value.WriteByte('\n')
					case 'r':
						value.WriteByte('\r')
					case 't':
						value.WriteByte('\t')
					default:
						value.WriteByte(source[i+1])
					}
					i += 2
				} else {
					value.WriteByte(source[i])
					i++
				}
			}
			if !closed {
				return nil, fmt.Errorf("unterminated Groovy string")
			}
			tokens = append(tokens, groovyToken{value.String(), quote, start, i})
			continue
		}
		if unicode.IsLetter(rune(source[i])) || source[i] == '_' {
			i++
			for i < len(source) && (unicode.IsLetter(rune(source[i])) || unicode.IsDigit(rune(source[i])) || source[i] == '_' || source[i] == '.') {
				i++
			}
		} else {
			i++
		}
		tokens = append(tokens, groovyToken{value: source[start:i], start: start, end: i})
		if len(tokens) > 100000 {
			return nil, fmt.Errorf("pipeline file exceeds 100000 tokens")
		}
	}
	return tokens, nil
}

func readJenkins(root, path string) (ciWorkflow, error) {
	w := ciWorkflow{Path: path, Provider: "jenkins", Jobs: map[string]runtimeJob{}}
	info, ok := repositoryFile(root, root, path)
	if !ok || info.Size() > 2<<20 {
		return w, fmt.Errorf("pipeline file must be inside the repository and below 2 MiB")
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return w, err
	}
	tokens, err := groovyTokens(string(data))
	if err != nil {
		return w, fmt.Errorf("parse %s: %w", path, err)
	}
	pairs := map[int]int{}
	stack := []int{}
	for i, t := range tokens {
		if t.quote != "" {
			continue
		}
		switch t.value {
		case "{", "(", "[":
			stack = append(stack, i)
		case "}", ")", "]":
			if len(stack) == 0 {
				return w, fmt.Errorf("unbalanced Groovy delimiters")
			}
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if map[string]string{"{": "}", "(": ")", "[": "]"}[tokens[start].value] != t.value {
				return w, fmt.Errorf("unbalanced Groovy delimiters")
			}
			pairs[start] = i
		}
	}
	if len(stack) > 0 {
		return w, fmt.Errorf("unbalanced Groovy delimiters")
	}
	job := runtimeJob{Env: providerEnvironment("jenkins", nil)}
	assignments := map[string]string{}
	type state struct {
		env                                     map[string]string
		directory, stage, node, source, blocked string
		workspace                               string
		docker                                  bool
	}
	var walk func(int, int, state, int, bool) state
	walk = func(start, end int, current state, depth int, declarative bool) state {
		if depth > 32 {
			job.Gaps = append(job.Gaps, "Jenkins nesting exceeds 32 levels")
			return current
		}
		// Declarative directives apply to their whole pipeline/stage regardless
		// of source order. Resolve them before steps, without visiting sibling
		// stages or treating imperative wrappers as declarative configuration.
		directives := map[int]int{}
		if declarative {
			for _, kind := range []string{"environment", "agent", "tools"} {
				for i := start; i < end; i++ {
					if tokens[i].quote == "" && tokens[i].value == kind && i+1 < end {
						stop := i
						if tokens[i+1].value == "{" {
							stop = pairs[i+1] + 1
						} else if kind == "agent" && slices.Contains([]string{"any", "none"}, tokens[i+1].value) {
							stop = i + 2
						}
						if stop > i {
							current = walk(i, stop, current, depth+1, false)
							directives[i] = stop
						}
					}
					if stop, ok := pairs[i]; ok {
						i = stop
					}
				}
			}
		}
		for i := start; i < end; i++ {
			if stop, done := directives[i]; done {
				i = stop - 1
				continue
			}
			token := tokens[i]
			if token.quote != "" {
				continue
			}
			if strings.HasPrefix(token.value, "env.") && i+2 < end && tokens[i+1].value == "=" {
				key := strings.TrimPrefix(token.value, "env.")
				value := tokens[i+2].value
				_, scoped := current.env[key]
				if current.blocked != "" || strings.Contains(key, ".") || !literalJenkinsAssignment(tokens, data, i+2, end) || (scoped && current.env[key] != value) {
					delete(assignments, key)
					job.Gaps = append(job.Gaps, "Jenkins imperative environment "+key+" requires a resolved value/path or conflicts with a scoped environment binding")
				} else {
					assignments[key] = value
				}
				i += 2
				continue
			}
			if token.value == "environment" && i+1 < end && tokens[i+1].value == "{" {
				stop := pairs[i+1]
				current.env = maps.Clone(current.env)
				for j := i + 2; j+2 < stop; j++ {
					if tokens[j+1].value == "=" {
						if literalJenkinsAssignment(tokens, data, j+2, stop) {
							current.env[tokens[j].value] = tokens[j+2].value
						} else {
							delete(current.env, tokens[j].value)
							job.Gaps = append(job.Gaps, "Jenkins environment "+tokens[j].value+" is computed or credential-backed")
						}
						j += 2
					}
				}
				i = stop
				continue
			}
			if token.value == "nodejs" && i+1 < end && tokens[i+1].quote != "" {
				version := tokens[i+1].value
				if current.blocked != "" {
					job.Gaps = append(job.Gaps, "Node tools are selected inside unresolved Groovy control flow")
					current.node, current.source = "", "unresolved Jenkins tools"
					i++
					continue
				}
				current.node, current.source = "", "Jenkins tools.nodejs "+version
				current.docker = false
				if numericNode.MatchString(version) {
					current.node = version
				}
				i++
				continue
			}
			if token.value == "image" && i+1 < end && tokens[i+1].quote != "" {
				current.node, current.source = ciImageNode(tokens[i+1].value, current.env), "Jenkins Docker image "+tokens[i+1].value
				current.docker = true
			}
			if token.value == "agent" && i+1 < end && slices.Contains([]string{"any", "none"}, tokens[i+1].value) {
				if current.docker {
					current.node, current.source, current.docker = "", "Jenkins agent override", false
				}
				current.workspace = ""
				if tokens[i+1].value == "any" {
					current.workspace = fmt.Sprintf("%s:agent:%d", path, token.start)
				}
			}
			if token.value == "sh" {
				j := i + 1
				var limit int
				parenthesized := j < end && tokens[j].value == "("
				if parenthesized {
					limit = pairs[j]
					j++
				} else {
					limit = j
					for limit < end {
						if tokens[limit].quote == "" && slices.Contains([]string{";", "{", "}"}, tokens[limit].value) {
							break
						}
						if limit > j && strings.Contains(string(data[tokens[limit-1].end:tokens[limit].start]), "\n") && !slices.Contains([]string{",", ":"}, tokens[limit-1].value) && !strings.ContainsAny(tokens[limit].value, "+-*/%?.[<>=!&|^") && tokens[limit].value != "as" {
							break
						}
						limit++
					}
				}
				literal := -1
				if j < limit && tokens[j].quote != "" {
					literal = j
				} else {
					for k := j; k+2 < limit && k < j+20; k++ {
						if tokens[k].value == "script" && tokens[k+1].value == ":" && tokens[k+2].quote != "" {
							literal = k + 2
							break
						}
					}
				}
				if literal < 0 {
					job.Gaps = append(job.Gaps, "Jenkins sh command is computed; supply its literal command and environment")
					w.Unresolved = append(w.Unresolved, path+": Jenkins sh command is computed; supply its literal command and environment")
					continue
				}
				text := tokens[literal]
				command := text.value
				env := mergeEnvironment(assignments, current.env)
				blocked := current.blocked
				// The complete argument must be literal: replacing just the first
				// string of a concatenation changes the executed shell program.
				if literal+1 < limit {
					next := tokens[literal+1]
					boundary := next.value == "," || next.value == ";" || next.value == "}"
					computed := !boundary && !strings.Contains(string(data[text.end:next.start]), "\n") || strings.ContainsAny(next.value, "+-*/%?.[<>=!&|^") || next.value == "as"
					if parenthesized {
						computed = literal == j || next.value != ","
					}
					if computed {
						blocked = "Jenkins sh script argument is computed; supply its complete literal command"
					}
				}
				if strings.HasPrefix(text.quote, "\"") {
					for key, value := range env {
						command = strings.ReplaceAll(command, "${env."+key+"}", value)
					}
					if strings.Contains(command, "$") {
						blocked = "Groovy string interpolation requires explicit values"
					}
				}
				native := &nativeCommand{Path: path, Start: text.start, End: text.end, Quote: text.quote, Original: command, Blocked: blocked, NodeVersion: current.node, NodeSource: current.source, Workspace: current.workspace}
				if setup, original, ok := unwrapNativeInstrumentation(command, "jenkins"); ok {
					native.Framework, native.Tracer, native.Site, native.ESM = setup.Framework, setup.Version, setup.Site, setup.ESM
					command = original
				}
				step := runtimeStep{Run: command, Env: env, WorkingDirectory: current.directory, Source: fmt.Sprintf("%s / %s / byte %d", path, current.stage, text.start), Native: native}
				if blocked != "" {
					job.Gaps = append(job.Gaps, step.Source+": "+blocked)
				}
				job.Steps = append(job.Steps, step)
				i = literal
				continue
			}
			open := i + 1
			if open < end && tokens[open].value == "(" {
				open = pairs[open] + 1
			}
			if open >= end || tokens[open].value != "{" {
				call := i+1 < end && (tokens[i+1].value == "(" || tokens[i+1].quote != "" || i+2 < end && tokens[i+2].value == ":")
				if call && !slices.Contains([]string{"echo", "input", "checkout", "junit", "archiveArtifacts", "timestamps", "timeout", "image", "label", "reuseNode", "customWorkspace", "registryUrl", "registryCredentialsId"}, token.value) {
					job.Gaps = append(job.Gaps, "Jenkins call "+token.value+" needs its resolved setup; no Groovy or library code was executed")
				}
				if token.value == "load" || token.value == "library" {
					job.Gaps = append(job.Gaps, "Jenkins shared library/load requires its resolved commands")
				}
				continue
			}
			stop := pairs[open]
			child := current
			child.env = maps.Clone(current.env)
			switch token.value {
			case "agent":
				child.workspace = fmt.Sprintf("%s:agent:%d", path, token.start)
				if child.docker {
					child.node, child.source, child.docker = "", "Jenkins agent override", false
				}
			case "stage":
				if i+2 < open && tokens[i+2].quote != "" {
					child.stage = tokens[i+2].value
				}
			case "dir":
				if i+4 == open && tokens[i+1].value == "(" && tokens[i+2].quote != "" {
					directory, e := literalCIPath(tokens[i+2].value)
					if e != nil {
						child.blocked = e.Error()
					} else {
						child.directory = filepath.ToSlash(filepath.Join(current.directory, directory))
					}
				} else {
					child.blocked = "Jenkins dir is computed"
				}
			case "withEnv":
				bindings := map[string]string{}
				literal := i+3 < open && tokens[i+1].value == "(" && tokens[i+2].value == "[" && pairs[i+2] == open-2
				if literal {
					for j := i + 3; j < open-2; j++ {
						text := tokens[j]
						key, value, ok := strings.Cut(text.value, "=")
						if text.quote == "" || strings.HasPrefix(text.quote, "\"") && strings.Contains(text.value, "$") || !ok || key == "" || strings.Contains(key, "+") {
							literal = false
							break
						}
						bindings[key] = value
						if j+1 < open-2 {
							j++
							if tokens[j].value != "," {
								literal = false
								break
							}
						}
					}
				}
				if !literal {
					child.blocked = "Jenkins withEnv bindings are computed or use unresolved interpolation/expansion"
				} else {
					maps.Copy(child.env, bindings)
				}
			case "docker":
				for j := open + 1; j < stop; j++ {
					if tokens[j].quote == "" && tokens[j].value == "args" {
						child.blocked = "Jenkins Docker arguments require resolved runtime and mount configuration"
						job.Gaps = append(job.Gaps, child.blocked)
						break
					}
				}
			case "node":
				child.workspace = fmt.Sprintf("%s:node:%d", path, token.start)
			case "pipeline", "stages", "steps", "script", "tools":
			case "post", "success", "always", "failure", "cleanup":
				child.blocked = "Jenkins post conditions require a resolved execution path"
			default:
				child.blocked = "Groovy control flow/function " + token.value + " requires a resolved execution path"
			}
			selected := walk(open+1, stop, child, depth+1, token.value == "pipeline" || token.value == "stage")
			// Declarative agent/tools configure their containing scope. A
			// stage's selection must never leak to a subsequent sibling stage.
			if slices.Contains([]string{"agent", "docker", "tools"}, token.value) {
				current.node, current.source, current.docker = selected.node, selected.source, selected.docker
				current.workspace = selected.workspace
			}
			i = stop
		}
		return current
	}
	walk(0, len(tokens), state{env: job.Env, directory: ".", stage: "pipeline", workspace: path + ":implicit"}, 0, false)
	job.Gaps = slices.Compact(job.Gaps)
	if len(job.Steps) == 0 {
		w.Unresolved = append(w.Unresolved, path+": no literal shell steps found; shared libraries or dynamically generated Pipeline need resolved commands")
	}
	w.Jobs["pipeline"] = job
	return w, nil
}

func literalJenkinsAssignment(tokens []groovyToken, source []byte, valueIndex, end int) bool {
	value := tokens[valueIndex]
	if value.quote == "" || strings.HasPrefix(value.quote, "\"") && strings.Contains(value.value, "$") {
		return false
	}
	if valueIndex+1 == end {
		return true
	}
	next := tokens[valueIndex+1]
	return (next.value == ";" || strings.Contains(string(source[value.end:next.start]), "\n")) && !strings.ContainsAny(next.value, "+-*/%?.[<>=!&|^") && next.value != "as"
}
