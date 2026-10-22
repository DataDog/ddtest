package testdrive

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Redact only the published copy. Execution still receives the original command.
func (result validationResult) MarshalJSON() ([]byte, error) {
	type plain validationResult
	data, err := json.Marshal(plain(result))
	if err != nil || result.Framework != "cypress" {
		return data, err
	}
	var fields any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	var redact func(any) any
	redact = func(value any) any {
		switch value := value.(type) {
		case string:
			return onboard.RedactCypressCommand(value)
		case []any:
			for i := range value {
				if item, ok := value[i].(string); ok && (item == "--key" || item == "-k") && i+1 < len(value) {
					value[i+1] = "[REDACTED]"
					continue
				}
				if item, ok := value[i].(string); ok && strings.HasPrefix(item, "-k") && !strings.HasPrefix(item, "--") {
					value[i] = "-k[REDACTED]"
					continue
				}
				value[i] = redact(value[i])
			}
			return value
		case map[string]any:
			for key := range value {
				value[key] = redact(value[key])
			}
			return value
		default:
			return value
		}
	}
	return json.Marshal(redact(fields))
}
