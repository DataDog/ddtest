// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package environment

import (
	"errors"
	"strings"
)

// SplitNodeOptions follows Node's ParseNodeOptionsEnvVar: only spaces separate
// arguments, double quotes group them, and backslashes escape only inside quotes.
// NODE_OPTIONS is not parsed by a POSIX shell, including on Windows.
func SplitNodeOptions(value string) ([]string, error) {
	var words []string
	var word strings.Builder
	quoted, started := false, false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '\\' && quoted:
			i++
			if i == len(value) {
				return nil, errors.New("invalid escape in NODE_OPTIONS")
			}
			c = value[i]
		case c == ' ' && !quoted:
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
			continue
		case c == '"':
			quoted = !quoted
			continue
		}
		word.WriteByte(c)
		started = true
	}
	if quoted {
		return nil, errors.New("unterminated double quote in NODE_OPTIONS")
	}
	if started {
		words = append(words, word.String())
	}
	return words, nil
}

// JoinNodeOptions uses only the escaping understood by Node's environment parser.
func JoinNodeOptions(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		if strings.ContainsAny(word, " \\\"") {
			word = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(word) + `"`
		}
		quoted[i] = word
	}
	return strings.Join(quoted, " ")
}
