package bazeldoctor

import (
	"encoding/json"
	"strings"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelupload"
)

// PostRequest is what the rules uploader resolves before ddtest runs the
// rest of it: its configuration, the resources that it loads through Bazel's
// runfiles while it holds the workspace lock, and optionally the doctor's
// configuration, whose output checks the run then applies. Credentials come
// from the environment, as for ddtest instrument bazel upload.
type PostRequest struct {
	Version   int           `json:"schema_version"`
	Config    PostConfig    `json:"config"`
	Resources PostResources `json:"resources"`
	// PreflightError is the uploader's failure before ddtest, such as an
	// invalid expected target or resource; the run then only reports it.
	PreflightError string `json:"preflight_error,omitempty"`
	// Validation applies the doctor's output checks; nil skips them.
	Validation *PostValidation `json:"validation,omitempty"`
}

// PostConfig is the rules uploader's UploaderConfig, with its paths resolved.
type PostConfig struct {
	Workspace string `json:"workspace"`
	DryRun    bool   `json:"dry_run"`
	// Agentless is the endpoint mode, for the credential check.
	Agentless                    bool     `json:"agentless"`
	ValidateEnrichment           bool     `json:"validate_enrichment"`
	LogLevel                     string   `json:"log_level"`
	QuiescentSec                 int      `json:"quiescent_sec"`
	MaxWaitSec                   int      `json:"max_wait_sec"`
	MaxDepth                     int      `json:"max_depth"`
	FailOnError                  bool     `json:"fail_on_error"`
	KeepPayloads                 bool     `json:"keep_payloads"`
	FilterPrefix                 bool     `json:"filter_prefix"`
	GzipPayloads                 bool     `json:"gzip_payloads"`
	Workers                      int      `json:"workers"`
	WorkersExplicit              bool     `json:"workers_explicit"`
	ExpectedEnrichedTags         []string `json:"expected_enriched_tags"`
	ExpectedTargets              []string `json:"expected_targets"`
	BepJSONFiles                 []string `json:"bep_json_files"`
	FreshnessSource              string   `json:"freshness_source"`
	FreshnessMode                string   `json:"freshness_mode"`
	FreshnessDisabledExplicitly  bool     `json:"freshness_disabled_explicitly"`
	ExecutionLogJSON             string   `json:"execution_log_json"`
	ArtifactSource               string   `json:"artifact_source"`
	RemoteArtifacts              string   `json:"remote_artifacts"`
	ArtifactStagingDir           string   `json:"artifact_staging_dir"`
	BepArtifactDownloader        string   `json:"bep_artifact_downloader"`
	BepArtifactDownloaderTimeout float64  `json:"bep_artifact_downloader_timeout_sec"`
	ReportJSON                   string   `json:"report_json"`
	TestlogsDir                  string   `json:"testlogs_dir"`
	CI                           bool     `json:"ci"`
	RetainedOutputsDir           string   `json:"retained_outputs_dir"`
	WrapperTempRoot              string   `json:"wrapper_temp_root"`
	RetentionStatusFile          string   `json:"retention_status_file"`
	RulesVersion                 string   `json:"rules_version"`
	UploaderBackend              string   `json:"uploader_backend"`
	BatchTestPayloads            bool     `json:"batch_test_payloads"`
	// ExecrootRoots and OutputBaseRoots resolve BEP artifact references, as
	// the doctor's _execroot_roots() and _output_base_roots() list them.
	ExecrootRoots   []string `json:"execroot_roots"`
	OutputBaseRoots []string `json:"output_base_roots"`
}

// PostResources is the uploader's LoadedResources and its pre-worker
// inputs.
type PostResources struct {
	// Contexts and ContextSelection are the context plan, as ddtest_backend
	// writes them into an upload plan; PrimaryContext names the primary one.
	Contexts         map[string]bazelupload.Object `json:"contexts"`
	ContextSelection *bazelupload.ContextSelection `json:"context_selection"`
	PrimaryContext   string                        `json:"primary_context"`
	// ContextRecords lists each context with its repository key, for the
	// retention catalog.
	ContextRecords      []ContextRecord `json:"context_records"`
	TelemetryFactsPaths []string        `json:"telemetry_facts_paths"`
	CodeownersFile      string          `json:"codeowners_file"`
	// WarningCodes are the resource, codeowners and API key fingerprint
	// warnings, and Messages what the uploader logs about them.
	WarningCodes []string     `json:"warning_codes"`
	Messages     []LogMessage `json:"messages"`
}

// ContextRecord is a context of the context plan.
type ContextRecord struct {
	RepoKey string `json:"repo_key"`
	Context string `json:"context"`
}

// LogMessage is a message that the uploader logs at a level.
type LogMessage struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// PostValidation is the doctor's configuration, for its output checks.
type PostValidation struct {
	Config json.RawMessage `json:"config"`
	// ConfigPath is the configuration's file, which the report records.
	ConfigPath string `json:"config_path,omitempty"`
	// ReportJSON receives the doctor's report of the checks.
	ReportJSON string `json:"report_json"`
}

// unknownRequestField reports the field that encoding/json's
// DisallowUnknownFields rejected, when err is that rejection.
func unknownRequestField(err error) (string, bool) {
	field, found := strings.CutPrefix(err.Error(), "json: unknown field ")
	return strings.Trim(field, `"`), found
}
