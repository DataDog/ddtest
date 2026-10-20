package constants

const TestParallelizationEnabledEnvVar = "DD_TEST_PARALLELIZATION_ENABLED"

// TestParallelizationPlanEnvVars maps planning-job CI tags to the environment
// variables consumed by tracing libraries. Keep this allowlist free of secrets
// and machine-specific metadata such as workspace paths.
var TestParallelizationPlanEnvVars = map[string]string{
	CIProviderName:        "DD_TEST_PARALLELIZATION_PLAN_PROVIDER_NAME",
	CIPipelineID:          "DD_TEST_PARALLELIZATION_PLAN_PIPELINE_ID",
	CIPipelineName:        "DD_TEST_PARALLELIZATION_PLAN_PIPELINE_NAME",
	CIPipelineDisplayName: "DD_TEST_PARALLELIZATION_PLAN_PIPELINE_DISPLAY_NAME",
	CIPipelineNumber:      "DD_TEST_PARALLELIZATION_PLAN_PIPELINE_NUMBER",
	CIPipelineURL:         "DD_TEST_PARALLELIZATION_PLAN_PIPELINE_URL",
	CIJobID:               "DD_TEST_PARALLELIZATION_PLAN_JOB_ID",
	CIJobName:             "DD_TEST_PARALLELIZATION_PLAN_JOB_NAME",
	CIJobURL:              "DD_TEST_PARALLELIZATION_PLAN_JOB_URL",
	CIStageName:           "DD_TEST_PARALLELIZATION_PLAN_STAGE_NAME",
}
