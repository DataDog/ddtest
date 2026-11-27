package constants

const TestParallelizationEnabledEnvVar = "_DD_TEST_PARALLELIZATION_ENABLED"
const TestParallelizationMetadataFileEnvVar = "_DD_TEST_PARALLELIZATION_METADATA_FILE"

// TestRunPartialEnvVar is "true" in a worker's environment when the worker runs
// part of the suite's files, as a CI node's or a parallel runner's share, so a
// framework can relax whole-suite checks that the other parts would satisfy.
const TestRunPartialEnvVar = "_DD_TEST_RUN_PARTIAL"

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
