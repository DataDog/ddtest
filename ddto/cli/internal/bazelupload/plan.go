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
	// ContextSelection chooses the context of each source that names its
	// output directory; see readSidecars.
	ContextSelection *ContextSelection `json:"context_selection,omitempty"`
	Sources          []Source          `json:"sources"`
}

// ContextSelection is the rules uploader's ContextPlan.select: a source gets
// the primary context, or the context of the repository that its sidecar
// names when the plan has several repositories or requires a match.
type ContextSelection struct {
	Primary          string        `json:"primary,omitempty"`
	ByRepo           []RepoContext `json:"by_repo,omitempty"`
	Override         bool          `json:"override,omitempty"`
	RequireRepoMatch bool          `json:"require_repo_match,omitempty"`
}

// RepoContext names the context of one repository.
type RepoContext struct {
	Repo    string `json:"repo"`
	Context string `json:"context"`
}

// Choose returns a context name for a sidecar's repository name, or a
// warning code, as ContextPlan.select does.
func (selection ContextSelection) Choose(repo string) (string, string) {
	if selection.Override || !selection.RequireRepoMatch && len(selection.ByRepo) <= 1 {
		return selection.Primary, ""
	}
	if repo == "" {
		return "", "context_repo_metadata_missing"
	}
	for _, entry := range selection.ByRepo {
		if entry.Repo == repo {
			return entry.Context, ""
		}
	}
	return "", "context_repo_not_found"
}

type Object = map[string]any

type Source struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Kind     Kind   `json:"kind"`
	Context  string `json:"context,omitempty"`
	Metadata Object `json:"bazel_metadata,omitempty"`
	// Output is a test source's test.outputs directory. ddtest reads its
	// bazel_target_metadata.json and selects the context instead of taking
	// bazel_metadata and context from the plan.
	Output string `json:"output,omitempty"`
	// The consumer decides which original telemetry envelope owns replay facts.
	Telemetry *TelemetryDirective `json:"telemetry,omitempty"`
}

type TelemetryDirective struct {
	Environment    string `json:"environment,omitempty"`
	AppendMessages []any  `json:"append_messages,omitempty"`
	ProviderSuffix string `json:"provider_suffix,omitempty"`
	Synthetic      Object `json:"synthetic,omitempty"`
	// SyntheticMessages asks for a message-batch request beside the source,
	// built as the rules uploader's _build_synthetic_telemetry does from the
	// source after its service and environment changes.
	SyntheticMessages  []any `json:"synthetic_messages,omitempty"`
	SyntheticSeqID     int64 `json:"synthetic_seq_id,omitempty"`
	SyntheticTimestamp int64 `json:"synthetic_timestamp,omitempty"`
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
	if err := checkPlan(&plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// checkPlan validates a plan and cleans its paths.
func checkPlan(plan *Plan) error {
	if plan.Version != 1 {
		return fmt.Errorf("unsupported upload plan schema version %d", plan.Version)
	}
	if plan.RulesVersion == "" || plan.RuntimeID == "" {
		return fmt.Errorf("upload plan requires rules_version and runtime_id")
	}
	if plan.Sources == nil {
		return fmt.Errorf("upload plan requires a sources array")
	}
	if plan.Codeowners != "" {
		if !filepath.IsLocal(plan.Codeowners) {
			return fmt.Errorf("codeowners path must be relative to the upload root")
		}
		plan.Codeowners = filepath.Clean(plan.Codeowners)
	}
	switch plan.CodeownersDialect {
	case "", "github", "gitlab":
	default:
		return fmt.Errorf("codeowners_dialect must be github or gitlab")
	}
	if err := validateSources(plan); err != nil {
		return err
	}
	if selection := plan.ContextSelection; selection != nil {
		for _, name := range append([]string{selection.Primary}, contextNames(selection.ByRepo)...) {
			if name != "" && plan.Contexts[name] == nil {
				return fmt.Errorf("context_selection names an unknown context")
			}
		}
	}
	return nil
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
		if source.Output != "" {
			if source.Kind != Test || source.Context != "" || source.Metadata != nil || plan.ContextSelection == nil ||
				!filepath.IsLocal(source.Output) {
				return fmt.Errorf("source %d with an output directory must be a test source without context or "+
					"bazel_metadata, in a plan with a context_selection, and the directory must be relative to the upload root", index+1)
			}
			source.Output = filepath.Clean(source.Output)
		}
		if (source.Kind != Test && source.Kind != Coverage && source.Kind != Telemetry) ||
			(source.Telemetry != nil && source.Kind != Telemetry) {
			return fmt.Errorf("source %d has invalid payload type or telemetry directives", index+1)
		}
	}
	return nil
}

func contextNames(entries []RepoContext) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Context
	}
	return names
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
