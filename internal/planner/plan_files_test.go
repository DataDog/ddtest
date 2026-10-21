package planner

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestWritePlanMetadata_EmptyCIReplacesPreviousIdentity(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := writePlanMetadata(map[string]string{constants.CIJobID: "previous-plan"}); err != nil {
		t.Fatal(err)
	}
	if err := writePlanMetadata(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(constants.PlanMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"tags":{}}` {
		t.Fatalf("plan metadata = %s, want empty tags", got)
	}
}

func TestWritePlanMetadata_WritesFinalTracerTags(t *testing.T) {
	t.Chdir(t.TempDir())
	ciTags := map[string]string{
		constants.CIProviderName:        "gitlab",
		constants.CIPipelineID:          "pipeline-456",
		constants.CIPipelineName:        "pipeline-name",
		constants.CIPipelineDisplayName: "pipeline-display-name",
		constants.CIPipelineNumber:      "456",
		constants.CIPipelineURL:         "https://gitlab.example.com/pipelines/456",
		constants.CIJobID:               "plan-123",
		constants.CIJobName:             "plan",
		constants.CIJobURL:              "https://gitlab.example.com/jobs/123",
		constants.CIStageName:           "planning",
		"DD_API_KEY":                    "must-not-be-stored",
		"ci.workspace_path":             "/private/workspace",
	}
	if err := writePlanMetadata(ciTags); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(constants.PlanMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{"tags": {
		"test.parallelization.plan.provider.name":         "gitlab",
		"test.parallelization.plan.pipeline.id":           "pipeline-456",
		"test.parallelization.plan.pipeline.name":         "pipeline-name",
		"test.parallelization.plan.pipeline.display_name": "pipeline-display-name",
		"test.parallelization.plan.pipeline.number":       "456",
		"test.parallelization.plan.pipeline.url":          "https://gitlab.example.com/pipelines/456",
		"test.parallelization.plan.job.id":                "plan-123",
		"test.parallelization.plan.job.name":              "plan",
		"test.parallelization.plan.job.url":               "https://gitlab.example.com/jobs/123",
		"test.parallelization.plan.stage.name":            "planning",
	}}
	if !reflect.DeepEqual(metadata, want) {
		t.Fatalf("plan metadata = %v, want %v", metadata, want)
	}

	ciTags[constants.CIJobID] = ""
	if err := writePlanMetadata(ciTags); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(constants.PlanMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata = nil
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	delete(want["tags"], "test.parallelization.plan.job.id")
	if !reflect.DeepEqual(metadata, want) {
		t.Fatalf("plan metadata with empty job ID = %v, want %v", metadata, want)
	}
}
