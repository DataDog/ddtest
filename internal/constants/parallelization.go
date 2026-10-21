package constants

const TestParallelizationEnabledEnvVar = "_DD_TEST_PARALLELIZATION_ENABLED"

// TestParallelizationPlanTags maps planning-job CI tags to the tracer tags written
// to plan-metadata.json. Keep this allowlist free of secrets and machine-specific
// metadata such as workspace paths.
var TestParallelizationPlanTags = map[string]string{
	CIProviderName:        "test.parallelization.plan.provider.name",
	CIPipelineID:          "test.parallelization.plan.pipeline.id",
	CIPipelineName:        "test.parallelization.plan.pipeline.name",
	CIPipelineDisplayName: "test.parallelization.plan.pipeline.display_name",
	CIPipelineNumber:      "test.parallelization.plan.pipeline.number",
	CIPipelineURL:         "test.parallelization.plan.pipeline.url",
	CIJobID:               "test.parallelization.plan.job.id",
	CIJobName:             "test.parallelization.plan.job.name",
	CIJobURL:              "test.parallelization.plan.job.url",
	CIStageName:           "test.parallelization.plan.stage.name",
}
