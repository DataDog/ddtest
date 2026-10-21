package planner

import (
	"os"
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
	if got := string(data); got != `{"ciTags":{}}` {
		t.Fatalf("plan metadata = %s, want empty ciTags", got)
	}
}
