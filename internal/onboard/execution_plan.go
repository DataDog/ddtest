package onboard

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
)

// ExecutionPlan is the input to preparation, execution and reporting. An
// unresolved plan is useful evidence, never permission to omit a configuration.
type ExecutionPlan struct {
	UnsupportedFeatures map[string]string `json:"unsupported_features,omitempty"`
	SchemaVersion       int               `json:"schema_version"`
	Fingerprint         string            `json:"input_fingerprint"`
	Framework           string            `json:"framework"`
	Runtime             string            `json:"local_runtime"`
	PackageManager      string            `json:"package_manager,omitempty"`
	Tracer              string            `json:"tracer,omitempty"`
	Site                string            `json:"site"`
	TracerNode          string            `json:"tracer_node,omitempty"`
	Initialization      string            `json:"initialization"`
	Features            []string          `json:"feature_scenarios"`
	DeferredFeatures    map[string]string `json:"deferred_features,omitempty"`
	CIRuntime           *RuntimeCheck     `json:"ci_runtime,omitempty"`
	Scope               ValidationScope   `json:"scope"`
}

// AssignConfigurationIDs excludes line locations and agent-supplied setup:
// adding a hook or prerequisite must not change the configuration it targets.
func AssignConfigurationIDs(scope *ValidationScope) {
	for i := range scope.Commands {
		entry := &scope.Commands[i]
		var prerequisites []BuildCommand
		for _, build := range entry.Prerequisites {
			build.Environment = configurationIdentityEnvironment(build.Environment)
			if build.Kind == PreparationNeedsFacts {
				build.ExpectedFiles = nil
				build.FactSource = ""
				prerequisites = append(prerequisites, build)
			} else if build.FactSource == "" {
				prerequisites = append(prerequisites, build)
			}
		}
		environment := configurationIdentityEnvironment(entry.Environment)
		data, _ := json.Marshal(struct {
			Command, Directory string
			// Empty for runner-independent commands, keeping their IDs stable.
			Platform      string `json:",omitempty"`
			Environment   map[string]string
			Cypress       *CypressCommand
			Prerequisites []BuildCommand
			ExitOnFailure bool
		}{entry.Command, entry.Directory, entry.Platform, environment, entry.Cypress, prerequisites, entry.ExitOnFailure})
		entry.ID = fmt.Sprintf("configuration-%x", sha256.Sum256(data))[:30]
	}
}

// ServiceCommand describes a repository-owned application, not the test runner.
// PortEnvironment is an existing shared app/test override. A free port is chosen
// only when that explicit override is supplied; arbitrary config rewriting is not.
type ServiceCommand struct {
	Argv            []string `json:"argv"`
	Directory       string   `json:"directory"`
	ReadinessURL    string   `json:"readiness_url"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	PortEnvironment string   `json:"port_environment,omitempty"`
}

type ConfigurationFact struct {
	PortOverride    *PortOverrideFact  `json:"port_override,omitempty"`
	ConfigurationID string             `json:"configuration_id"`
	Directory       string             `json:"directory,omitempty"`
	Prerequisites   []PrerequisiteFact `json:"prerequisites,omitempty"`
	Services        []ServiceCommand   `json:"services,omitempty"`
}

type PortOverrideFact struct {
	Environment string `json:"environment"`
	SourceFile  string `json:"source_file"`
}

func configurationIdentityEnvironment(original map[string]string) map[string]string {
	environment := maps.Clone(original)
	if environment["NODE_OPTIONS"] == "" {
		delete(environment, "NODE_OPTIONS")
	}
	if len(environment) == 0 {
		return nil
	}
	return environment
}
