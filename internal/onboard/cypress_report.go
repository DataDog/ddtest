package onboard

import "regexp"

// Reports describe the original command without publishing Cypress Cloud keys.
var cypressKeyArgument = regexp.MustCompile(`(--key(?:=|\s+)|(?:^|\s)-k(?:=|\s*)?)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s;]+)`)

func RedactCypressCommand(command string) string {
	return cypressKeyArgument.ReplaceAllString(command, "${1}[REDACTED]")
}
