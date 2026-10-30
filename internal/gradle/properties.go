// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"os"
	"strconv"
	"strings"
)

// propertySpace is the whitespace a properties file ignores around keys and
// values.
const propertySpace = " \t\f"

// ReadProperties reads a properties file as Java, and so Gradle, reads
// gradle.properties: comments are skipped, a line ending in an unescaped
// backslash continues on the next, a key ends at the first unescaped '=', ':'
// or whitespace, and escapes such as \t or A are resolved.
func ReadProperties(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(data)), "\n")
	properties := map[string]string{}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimLeft(lines[i], propertySpace)
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		for continued(line) {
			line = line[:len(line)-1]
			if i+1 == len(lines) {
				break
			}
			i++
			line += strings.TrimLeft(lines[i], propertySpace)
		}
		key, value := splitProperty(line)
		properties[unescapeProperty(key)] = unescapeProperty(value)
	}
	return properties, nil
}

// continued reports whether a line ends in an odd number of backslashes, the
// last of which joins the next line.
func continued(line string) bool {
	return (len(line)-len(strings.TrimRight(line, `\`)))%2 == 1
}

// splitProperty splits a logical line at its key's end: the first unescaped
// separator, '=', ':' or whitespace, and the whitespace around it.
func splitProperty(line string) (key, value string) {
	end := len(line)
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			continue
		}
		if strings.IndexByte("=:"+propertySpace, line[i]) >= 0 {
			end = i
			break
		}
	}
	value = strings.TrimLeft(line[end:], propertySpace)
	if value != "" && (value[0] == '=' || value[0] == ':') {
		value = strings.TrimLeft(value[1:], propertySpace)
	}
	return line[:end], value
}

// unescapeProperty resolves a key's or value's escapes; a backslash before any
// other character stands for that character.
func unescapeProperty(text string) string {
	var unescaped strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' || i+1 == len(text) {
			unescaped.WriteByte(text[i])
			continue
		}
		i++
		switch text[i] {
		case 't':
			unescaped.WriteByte('\t')
		case 'n':
			unescaped.WriteByte('\n')
		case 'r':
			unescaped.WriteByte('\r')
		case 'f':
			unescaped.WriteByte('\f')
		case 'u':
			if code, err := strconv.ParseUint(text[i+1:min(i+5, len(text))], 16, 16); err == nil && i+5 <= len(text) {
				unescaped.WriteRune(rune(code))
				i += 4
			} else {
				unescaped.WriteByte('u')
			}
		default:
			unescaped.WriteByte(text[i])
		}
	}
	return unescaped.String()
}
