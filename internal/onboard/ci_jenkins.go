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
	type state struct {
		env                                     map[string]string
		directory, stage, node, source, blocked string
	}
	var walk func(int, int, state, int) state
	walk = func(start, end int, current state, depth int) state {
		if depth > 32 {
			job.Gaps = append(job.Gaps, "Jenkins nesting exceeds 32 levels")
			return current
		}
		for i := start; i < end; i++ {
			token := tokens[i]
			if token.quote != "" {
				continue
			}
			if token.value == "environment" && i+1 < end && tokens[i+1].value == "{" {
				stop := pairs[i+1]
				current.env = maps.Clone(current.env)
				for j := i + 2; j+2 < stop; j++ {
					if tokens[j+1].value == "=" {
						value := tokens[j+2]
						literal := value.quote != "" && (!strings.HasPrefix(value.quote, "\"") || !strings.Contains(value.value, "$"))
						if j+3 < stop {
							next := tokens[j+3]
							literal = literal && (next.value == ";" || strings.Contains(string(data[value.end:next.start]), "\n")) && !strings.ContainsAny(next.value, "+-*/%?.[<>=!&|^") && next.value != "as"
						}
						if literal {
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
				if numericNode.MatchString(version) {
					current.node = version
				}
				i++
				continue
			}
			if token.value == "image" && i+1 < end && tokens[i+1].quote != "" {
				current.node, current.source = ciImageNode(tokens[i+1].value, current.env), "Jenkins Docker image "+tokens[i+1].value
			}
			if token.value == "sh" {
				j := i + 1
				limit := end
				parenthesized := j < end && tokens[j].value == "("
				if parenthesized {
					limit = pairs[j]
					j++
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
					continue
				}
				text := tokens[literal]
				command := text.value
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
					for key, value := range current.env {
						command = strings.ReplaceAll(command, "${env."+key+"}", value)
					}
					if strings.Contains(command, "$") {
						blocked = "Groovy string interpolation requires explicit values"
					}
				}
				native := &nativeCommand{Path: path, Start: text.start, End: text.end, Quote: text.quote, Original: command, Blocked: blocked, NodeVersion: current.node, NodeSource: current.source}
				if setup, original, ok := unwrapNativeInstrumentation(command, "jenkins"); ok {
					native.Framework, native.Tracer, native.Site, native.ESM = setup.Framework, setup.Version, setup.Site, setup.ESM
					command = original
				}
				step := runtimeStep{Run: command, Env: maps.Clone(current.env), WorkingDirectory: current.directory, Source: fmt.Sprintf("%s / %s / byte %d", path, current.stage, text.start), Native: native}
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
				if i+1 < end && tokens[i+1].value == "(" && !slices.Contains([]string{"echo", "input", "checkout", "junit", "archiveArtifacts", "timestamps", "timeout"}, token.value) {
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
			case "stage":
				if i+2 < open && tokens[i+2].quote != "" {
					child.stage = tokens[i+2].value
				}
			case "dir":
				if i+2 < open && tokens[i+2].quote != "" {
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
				for j := i + 1; j < open; j++ {
					if tokens[j].quote != "" {
						key, value, ok := strings.Cut(tokens[j].value, "=")
						if ok {
							child.env[key] = value
						}
					}
				}
			case "pipeline", "stages", "steps", "node", "script", "agent", "docker", "tools":
			case "post", "success", "always", "failure", "cleanup":
				i = stop
				continue
			default:
				child.blocked = "Groovy control flow/function " + token.value + " requires a resolved execution path"
			}
			selected := walk(open+1, stop, child, depth+1)
			// Declarative agent/tools configure their containing scope. A
			// stage's selection must never leak to a subsequent sibling stage.
			if slices.Contains([]string{"agent", "docker", "tools"}, token.value) {
				current.node, current.source = selected.node, selected.source
			}
			i = stop
		}
		return current
	}
	walk(0, len(tokens), state{env: job.Env, directory: ".", stage: "pipeline"}, 0)
	job.Gaps = slices.Compact(job.Gaps)
	if len(job.Steps) == 0 {
		w.Unresolved = append(w.Unresolved, path+": no literal shell steps found; shared libraries or dynamically generated Pipeline need resolved commands")
	}
	w.Jobs["pipeline"] = job
	return w, nil
}
