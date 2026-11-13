// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A command-line option whose value only CI knows and selects no test is
// left out of the command validation runs, and named for the user. That is
// one rule, whichever way CI writes the value: a CI expression, as
// ${{ secrets.GPG_PASSPHRASE }} (see withoutCIOnlyOptions), or a variable
// validation cannot read, as a GitLab file variable. It covers two kinds:
//
//   - Maven's settings files (javaSettingsOptions), which hold repositories,
//     mirrors and credentials: the option is removed with its value, and
//     reported as omitted (see javaOmittedOptionsNote);
//   - the JVM options of plugins that run no tests, GPG signing, Gradle's
//     signing plugin and the Sonar scanner (see ciOnlyPlugins), as
//     -Dgpg.passphrase=$GPG_PASSPHRASE: the value is emptied, and the option
//     is among the values validation runs without (runtimeStep.WithoutCIValues).
//
// Any other option, a test's credential as -Ddb.password=… included, still
// requires review.

// javaSettingsOptions select Maven's user and global settings files. A
// settings file holds repositories, mirrors and credentials; it selects no
// test, so a path only CI provides, as a GitLab file variable, is left out.
var javaSettingsOptions = []string{"-s", "--settings", "-gs", "--global-settings"}

// omitCIOnlyOptions leaves the CI-only options out of run (see above), where
// unknown reports a variable whose value validation cannot read. It returns
// run without them, the Maven settings options it removed as written, and
// the JVM options whose values it emptied, as -Dgpg.passphrase. A value that
// runs a command or transforms the variable stays, and leaves the step
// unresolved.
func omitCIOnlyOptions(run string, unknown func(string) bool) (text string, omitted, without []string) {
	if !strings.Contains(run, "$") {
		return run, nil, nil
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(run), "")
	if err != nil {
		return run, nil, nil
	}
	type span struct {
		start, end uint
		// option is the emptied JVM option; empty for a removed setting.
		option string
	}
	var spans []span
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		maven := mavenCommand([]string{call.Args[0].Lit()})
		for i := 1; i < len(call.Args); i++ {
			option := call.Args[i].Lit()
			if maven && slices.Contains(javaSettingsOptions, option) && i+1 < len(call.Args) {
				if javaCIOnlyValue(call.Args[i+1].Parts, unknown) {
					spans = append(spans, span{call.Args[i].Pos().Offset(), call.Args[i+1].End().Offset(), ""})
				}
				i++
				continue
			}
			// --settings=$MAVEN_SETTINGS, or -Dgpg.passphrase=$GPG_PASSPHRASE,
			// also within double quotes.
			parts := call.Args[i].Parts
			if len(parts) == 1 {
				if quoted, ok := parts[0].(*syntax.DblQuoted); ok {
					parts = quoted.Parts
				}
			}
			literal, ok := firstLiteral(parts)
			if !ok {
				continue
			}
			name, value, assigned := strings.Cut(literal, "=")
			switch {
			case !assigned:
			case maven && strings.HasPrefix(name, "--") && slices.Contains(javaSettingsOptions, name) && value == "" && javaCIOnlyValue(parts[1:], unknown):
				spans = append(spans, span{call.Args[i].Pos().Offset(), call.Args[i].End().Offset(), ""})
			case ciOnlyProperty(name) && len(parts) > 1 && javaCIOnlyValue(parts[1:], unknown):
				// The value starts after the =, in the literal.
				spans = append(spans, span{parts[0].Pos().Offset() + uint(len(name)) + 1, parts[len(parts)-1].End().Offset(), name})
			}
		}
		return true
	})
	if len(spans) == 0 {
		return run, nil, nil
	}
	text = run
	for i := len(spans) - 1; i >= 0; i-- {
		start, end := int(spans[i].start), int(spans[i].end)
		if spans[i].option != "" {
			text = text[:start] + text[end:]
			if !slices.Contains(without, spans[i].option) {
				without = append(without, spans[i].option)
			}
			continue
		}
		omitted = append(omitted, run[start:end])
		// The separating blank before the option goes with it.
		for start > 0 && (text[start-1] == ' ' || text[start-1] == '\t') {
			start--
		}
		text = text[:start] + text[end:]
	}
	slices.Reverse(omitted)
	slices.Reverse(without)
	return text, omitted, without
}

// firstLiteral is a word's leading literal part.
func firstLiteral(parts []syntax.WordPart) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	literal, ok := parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}
	return literal.Value, true
}

// javaCIOnlyValue reports a value made of literal text and plain reads of
// variables, as $MAVEN_SETTINGS or "${CI_SETTINGS}", at least one of which
// unknown reports; a command, arithmetic or a transformed read is not one.
func javaCIOnlyValue(parts []syntax.WordPart, unknown func(string) bool) bool {
	reads := false
	var plain func(parts []syntax.WordPart, quoted bool) bool
	plain = func(parts []syntax.WordPart, quoted bool) bool {
		for _, part := range parts {
			switch part := part.(type) {
			case *syntax.Lit:
			case *syntax.SglQuoted:
			case *syntax.DblQuoted:
				if quoted || !plain(part.Parts, true) {
					return false
				}
			case *syntax.ParamExp:
				if part.Param == nil || part.Index != nil || part.Slice != nil || part.Repl != nil || part.Exp != nil || part.Excl || part.Length || part.Width {
					return false
				}
				reads = reads || unknown(part.Param.Value)
			default:
				return false
			}
		}
		return true
	}
	return plain(parts, false) && reads
}

// javaOmittedOptionsNote tells the user which options a validated step's CI
// command passes that local validation runs without.
func javaOmittedOptionsNote(omitted []string, selected string) string {
	if len(omitted) == 0 {
		return ""
	}
	return "This CI step passes Maven " + strings.Join(omitted, " ") + ", a settings file only CI provides, for repositories, mirrors and credentials rather than tests. Local validation runs " + selected + " without it, with this machine's Maven settings, so any profile or property that file activates is not validated. Report this to the user."
}
