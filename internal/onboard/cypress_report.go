package onboard

import (
	"maps"
	"regexp"
	"slices"
)

// Reports describe the original command without publishing Cypress Cloud keys.
var cypressKeyArgument = regexp.MustCompile(`(--key(?:=|\s+)|(?:^|\s)-k(?:=|\s*)?)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s;]+)`)

// A long --key option is unambiguous, even in prose. A short -k is redacted
// only where a command is expected, so words such as "-kitten" survive.
var cypressLongKeyArgument = regexp.MustCompile(`(--key(?:=|\s+))(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s;]+)`)

// Cypress Cloud also reads its credentials from these variables.
var cypressSecretVariables = []string{"CYPRESS_RECORD_KEY", "CYPRESS_CLOUD_TOKEN"}

var cypressSecretAssignment = regexp.MustCompile(`\b((?:CYPRESS_RECORD_KEY|CYPRESS_CLOUD_TOKEN)=)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s;]+)`)

// Text that quotes a Cypress invocation is redacted as a command.
var cypressInvocation = regexp.MustCompile(`\bcypress\s+run\b`)

func RedactCypressCommand(command string) string {
	return cypressKeyArgument.ReplaceAllString(cypressSecretAssignment.ReplaceAllString(command, "${1}[REDACTED]"), "${1}[REDACTED]")
}

// RedactCypressText redacts recording keys in free text such as errors and
// reasons, where a short -k may be an ordinary word.
func RedactCypressText(text string) string {
	if cypressInvocation.MatchString(text) {
		return RedactCypressCommand(text)
	}
	return cypressLongKeyArgument.ReplaceAllString(cypressSecretAssignment.ReplaceAllString(text, "${1}[REDACTED]"), "${1}[REDACTED]")
}

// CypressSecretVariable reports whether a variable holds a Cypress Cloud credential.
func CypressSecretVariable(name string) bool {
	return slices.Contains(cypressSecretVariables, name)
}

// RedactCypressEnvironment masks Cypress Cloud credentials by name.
func RedactCypressEnvironment(environment map[string]string) map[string]string {
	if !slices.ContainsFunc(slices.Collect(maps.Keys(environment)), CypressSecretVariable) {
		return environment
	}
	redacted := maps.Clone(environment)
	for name := range redacted {
		if CypressSecretVariable(name) {
			redacted[name] = "[REDACTED]"
		}
	}
	return redacted
}

// RedactCypressScope returns a copy of scope that is safe to print. Every
// scope string quotes CI commands, so each is redacted as a command.
func RedactCypressScope(scope ValidationScope) ValidationScope {
	commands := make([]TestCommand, len(scope.Commands))
	for i, entry := range scope.Commands {
		entry.Command = RedactCypressCommand(entry.Command)
		entry.Resolution = RedactCypressCommand(entry.Resolution)
		entry.Environment = RedactCypressEnvironment(entry.Environment)
		entry.Locations = redactCypressList(entry.Locations, RedactCypressCommand)
		entry.UnvalidatedSetup = redactCypressList(entry.UnvalidatedSetup, RedactCypressCommand)
		entry.Prerequisites = slices.Clone(entry.Prerequisites)
		for j := range entry.Prerequisites {
			entry.Prerequisites[j].Command = RedactCypressCommand(entry.Prerequisites[j].Command)
			entry.Prerequisites[j].Environment = RedactCypressEnvironment(entry.Prerequisites[j].Environment)
		}
		commands[i] = entry
	}
	scope.Commands = commands
	scope.Unresolved = redactCypressList(scope.Unresolved, RedactCypressCommand)
	scope.Review = redactCypressList(scope.Review, RedactCypressCommand)
	return scope
}

func redactCypressList(values []string, redact func(string) string) []string {
	if values == nil {
		return nil
	}
	redacted := make([]string, len(values))
	for i, value := range values {
		redacted[i] = redact(value)
	}
	return redacted
}
