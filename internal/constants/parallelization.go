package constants

const TestParallelizationEnabledEnvVar = "_DD_TEST_PARALLELIZATION_ENABLED"

// TestParallelizationPlanTags lists planning-job CI tags shared with tracing
// libraries. Keep this allowlist free of secrets and machine-specific metadata
// such as workspace paths.
var TestParallelizationPlanTags = []string{
	CIProviderName,
	CIPipelineID,
	CIPipelineName,
	CIPipelineDisplayName,
	CIPipelineNumber,
	CIPipelineURL,
	CIJobID,
	CIJobName,
	CIJobURL,
	CIStageName,
}
