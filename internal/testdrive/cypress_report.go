package testdrive

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Redact only the published copy. Execution still receives the original command.
// Cypress recording keys and WebdriverIO grid keys share the --key/-k form.
func (result validationResult) MarshalJSON() ([]byte, error) {
	type plain validationResult
	data, err := json.Marshal(plain(result))
	if err != nil || (result.Framework != "cypress" && result.Framework != "webdriverio") {
		return data, err
	}
	var fields any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	var redact func(any, bool) any
	redact = func(value any, command bool) any {
		switch value := value.(type) {
		case string:
			if command {
				return onboard.RedactCypressCommand(value)
			}
			return onboard.RedactCypressText(value)
		case []any:
			for i := range value {
				if item, ok := value[i].(string); ok && command && (item == "--key" || item == "-k") && i+1 < len(value) {
					value[i+1] = "[REDACTED]"
					continue
				}
				if item, ok := value[i].(string); ok && command && strings.HasPrefix(item, "-k") && !strings.HasPrefix(item, "--") {
					value[i] = "-k[REDACTED]"
					continue
				}
				value[i] = redact(value[i], command)
			}
			return value
		case map[string]any:
			for key := range value {
				if _, ok := value[key].(string); ok && onboard.CypressSecretVariable(key) {
					value[key] = "[REDACTED]"
					continue
				}
				value[key] = redact(value[key], command || cypressCommandField(key))
			}
			return value
		default:
			return value
		}
	}
	return json.Marshal(redact(fields, false))
}

// Fields that hold commands, argv or CI locations quoting them. Elsewhere a
// short -k may be an ordinary word, so only unambiguous forms are redacted.
func cypressCommandField(key string) bool {
	switch key {
	case "command", "resolution", "cli", "start", "locations", "unresolved", "review", "unvalidated_setup", "unresolved_ci_nodes":
		return true
	}
	return strings.HasSuffix(key, "_command")
}
