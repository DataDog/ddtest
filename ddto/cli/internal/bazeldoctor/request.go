package bazeldoctor

import "encoding/json"

// Request is what the rules' Python doctor resolves before it hands the
// validation over: its parsed arguments, its configuration, and the paths
// that only it can resolve through Bazel's runfiles. The Python doctor still
// runs the first steps of _run_doctor, up to resolving ddtest, so that their
// failures come first; this package continues from the expected targets.
// Environment variables and the working directory it reads itself.
type Request struct {
	Version int  `json:"schema_version"`
	Args    Args `json:"args"`
	// ConfigPath is the resolved configuration path, and Config its
	// document as the Python doctor loaded it, in its key order.
	ConfigPath string          `json:"config_path"`
	Config     json.RawMessage `json:"config"`
	// BepFiles are _configured_bep_json_files(args), resolved.
	BepFiles []string `json:"bep_files"`
	// ExpectedTargetsFile is the configured expected-targets file, resolved
	// through runfiles, or empty when the configuration names none.
	ExpectedTargetsFile string `json:"expected_targets_file"`
	// ContextManifest is the configured context manifest, resolved through
	// runfiles, when the configuration requires Git metadata.
	ContextManifest *ContextManifest `json:"context_manifest,omitempty"`
	// ExecrootRoots and OutputBaseRoots are _execroot_roots() and
	// _output_base_roots(), in order.
	ExecrootRoots   []string `json:"execroot_roots"`
	OutputBaseRoots []string `json:"output_base_roots"`
	// SupportBundle is what the support bundle needs from the Python doctor,
	// when --support-bundle names one and the default collector writes it.
	SupportBundle *SupportBundle `json:"support_bundle,omitempty"`
}

// Args are the doctor's parsed command-line arguments, after _parse_args.
type Args struct {
	ExpectedTargets              []string `json:"expected_target"`
	ExpectedTargetsFile          string   `json:"expected_targets_file"`
	ContextEntries               []string `json:"context_entry"`
	BepJSON                      []string `json:"bep_json"`
	FreshnessSource              string   `json:"freshness_source"`
	FreshnessMode                string   `json:"freshness_mode"`
	ArtifactSource               string   `json:"artifact_source"`
	RemoteArtifacts              string   `json:"remote_artifacts"`
	ArtifactStagingDir           string   `json:"artifact_staging_dir"`
	BepArtifactDownloader        string   `json:"bep_artifact_downloader"`
	BepArtifactDownloaderTimeout float64  `json:"bep_artifact_downloader_timeout_sec"`
	ReportJSON                   string   `json:"report_json"`
	SupportBundle                string   `json:"support_bundle"`
	SupportBundleRenderer        string   `json:"support_bundle_renderer"`
}

// ContextManifest is a context manifest, read by the doctor, with the path
// that runfiles gave each of its lines' files. A line that does not resolve
// keeps the raw direct path, as _resolve_runfile_path returns it.
type ContextManifest struct {
	Path     string            `json:"path"`
	Resolved map[string]string `json:"resolved"`
}

// Result tells the Python doctor how the run ended.
type Result struct {
	// Status is "done", or "fallback" when the Python doctor must validate
	// the outputs itself; Reason then says why.
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Reason   string `json:"reason,omitempty"`
	// Stdout and Stderr are the messages that the doctor prints, when done.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}
