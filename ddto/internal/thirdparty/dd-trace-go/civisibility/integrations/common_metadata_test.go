package integrations

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

func TestCommonTagOptionsKeepUpdatesTruncationAndBazelFiltering(t *testing.T) {
	for _, files := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "bazel"}[files], func(t *testing.T) {
			t.Setenv(bazel.PayloadsInFilesEnv, map[bool]string{false: "false", true: "true"}[files])
			t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
			bazel.ResetForTesting()
			utils.ResetCITags()
			utils.ResetCIMetrics()
			t.Cleanup(bazel.ResetForTesting)
			t.Cleanup(utils.ResetCITags)
			t.Cleanup(utils.ResetCIMetrics)
			utils.AddCITagsMap(map[string]string{"ci.job.name": "one", "git.commit.sha": "original", "os.platform": "platform", "runtime.version": "runtime", "_dd.ci.env_vars": "env", "ci.long": strings.Repeat("ñ", 5001), "custom": "kept", "test_session.name": "envelope-only"})
			client, err := tracer.New(tracer.Config{Transport: citransport.Config{Endpoint: "http://diagnostic.invalid", HTTPClient: &http.Client{Transport: &commonMetadataTransport{}}}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := client.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			makeSpan := func() *tracer.Span {
				span, _ := client.StartSpan(context.Background(), "test", fillCommonTags([]tracer.StartSpanOption{tracer.SpanType("test")})...)
				return span
			}
			one := makeSpan()
			utils.AddCITags("ci.job.name", "two")
			two := makeSpan()
			utils.GetCITags()["ci.job.name"] = "direct"
			direct := makeSpan()
			for i, span := range []*tracer.Span{one, two, direct} {
				for _, key := range []string{"ci.job.name", "git.commit.sha", "os.platform", "runtime.version", "_dd.ci.env_vars", "ci.long"} {
					got, exists := span.Meta(key)
					if files {
						if exists {
							t.Fatalf("Bazel inherited %s", key)
						}
						continue
					}
					if !exists {
						t.Fatalf("missing %s", key)
					}
					if key == "ci.job.name" && got != []string{"one", "two", "direct"}[i] {
						t.Fatalf("stale snapshot: %q", got)
					}
					if key == "ci.long" && got != strings.Repeat("ñ", 5000) {
						t.Fatal("common value lost UTF-8 truncation")
					}
				}
				if got, _ := span.Meta("custom"); got != "kept" {
					t.Fatal("nonshared common tag lost")
				}
				if _, exists := span.Meta("test_session.name"); exists {
					t.Fatal("session name duplicated")
				}
			}
		})
	}
}
