package constants

const TestParallelizationEnabledEnvVar = "_DD_TEST_PARALLELIZATION_ENABLED"

// TestParallelizationPlanEnvVars maps planning-job CI tags to the private
// environment variables consumed by tracing libraries. Keep this allowlist free
// of secrets and machine-specific metadata such as workspace paths.
var TestParallelizationPlanEnvVars = map[string]string{
	CIProviderName:        "_DD_TEST_PARALLELIZATION_PLAN_PROVIDER_NAME",
	CIPipelineID:          "_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_ID",
	CIPipelineName:        "_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_NAME",
	CIPipelineDisplayName: "_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_DISPLAY_NAME",
	CIPipelineNumber:      "_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_NUMBER",
	CIPipelineURL:         "_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_URL",
	CIJobID:               "_DD_TEST_PARALLELIZATION_PLAN_JOB_ID",
	CIJobName:             "_DD_TEST_PARALLELIZATION_PLAN_JOB_NAME",
	CIJobURL:              "_DD_TEST_PARALLELIZATION_PLAN_JOB_URL",
	CIStageName:           "_DD_TEST_PARALLELIZATION_PLAN_STAGE_NAME",
}
