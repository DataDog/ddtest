package bazelupload

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Test      Kind = "test"
	Coverage  Kind = "coverage"
	Telemetry Kind = "telemetry"
)

// Plan contains the files a Bazel consumer selected for this invocation. The
// caller resolves BEP freshness and materializes remote artifacts first.
type Plan struct {
	Version           int               `json:"schema_version"`
	RulesVersion      string            `json:"rules_version"`
	RuntimeID         string            `json:"runtime_id"`
	CodeownersDialect string            `json:"codeowners_dialect,omitempty"`
	Codeowners        string            `json:"codeowners,omitempty"`
	Contexts          map[string]Object `json:"contexts,omitempty"`
	Sources           []Source          `json:"sources"`
}

type Object = map[string]any

type Source struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Kind     Kind   `json:"kind"`
	Context  string `json:"context,omitempty"`
	Metadata Object `json:"bazel_metadata,omitempty"`
	// The consumer decides which original telemetry envelope owns replay facts.
	Telemetry *TelemetryDirective `json:"telemetry,omitempty"`
}

type TelemetryDirective struct {
	Environment    string `json:"environment,omitempty"`
	AppendMessages []any  `json:"append_messages,omitempty"`
	ProviderSuffix string `json:"provider_suffix,omitempty"`
	Synthetic      Object `json:"synthetic,omitempty"`
}

func readPlan(reader io.Reader) (Plan, error) {
	var plan Plan
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("decode upload plan: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return Plan{}, fmt.Errorf("upload plan must contain a single JSON document")
	}
	if plan.Version != 1 {
		return Plan{}, fmt.Errorf("unsupported upload plan schema version %d", plan.Version)
	}
	if plan.RulesVersion == "" || plan.RuntimeID == "" {
		return Plan{}, fmt.Errorf("upload plan requires rules_version and runtime_id")
	}
	if plan.Sources == nil {
		return Plan{}, fmt.Errorf("upload plan requires a sources array")
	}
	if plan.Codeowners != "" {
		if !filepath.IsLocal(plan.Codeowners) {
			return Plan{}, fmt.Errorf("codeowners path must be relative to the upload root")
		}
		plan.Codeowners = filepath.Clean(plan.Codeowners)
	}
	switch plan.CodeownersDialect {
	case "", "github", "gitlab":
	default:
		return Plan{}, fmt.Errorf("codeowners_dialect must be github or gitlab")
	}
	if err := validateSources(&plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func validateSources(plan *Plan) error {
	paths, ids := make(map[string]bool), make(map[string]bool)
	for index := range plan.Sources {
		source := &plan.Sources[index]
		if !filepath.IsLocal(source.Path) {
			return fmt.Errorf("source %d requires a path relative to the upload root", index+1)
		}
		source.Path = filepath.Clean(source.Path)
		if source.Path == "." || paths[source.Path] || source.ID == "" || ids[source.ID] {
			return fmt.Errorf("source %d requires a unique path and ID", index+1)
		}
		paths[source.Path], ids[source.ID] = true, true
		if source.Context != "" && plan.Contexts[source.Context] == nil {
			return fmt.Errorf("source %d names an unknown context", index+1)
		}
		if (source.Kind != Test && source.Kind != Coverage && source.Kind != Telemetry) ||
			(source.Telemetry != nil && source.Kind != Telemetry) {
			return fmt.Errorf("source %d has invalid payload type or telemetry directives", index+1)
		}
	}
	return nil
}

func object(value any) Object {
	values, _ := value.(map[string]any)
	return values
}

func stringValue(values Object, key string) string {
	value, _ := values[key].(string)
	return value
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func checkSuffix(source Source, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.EqualFold(filepath.Ext(source.Path), suffix) {
			return true
		}
	}
	return false
}
