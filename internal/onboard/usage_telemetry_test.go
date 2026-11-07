package onboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/usagetelemetry"
	"github.com/stretchr/testify/require"
)

type humanUsageOutput struct {
	bytes.Buffer
	*usagetelemetry.Attempt
}

type humanUsageTransport func(*http.Request) (*http.Response, error)

func (f humanUsageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHumanOnboardingUsageReportsCIBlockers(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, phase, code, remedy string
		prepare                            func(*testing.T)
	}{
		{
			name: "unsupported provider", outcome: "unsupported", phase: "onboarding", code: "ci_provider_unsupported", remedy: "Test CI",
			prepare: func(t *testing.T) {
				registerTestCI(t)
				root := t.TempDir()
				writeFile(t, root, "package.json", `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.0.0"}}`)
				writeFile(t, root, testCIFile, jestWorkflow)
				t.Chdir(root)
			},
		},
		{
			name: "missing runtime", outcome: "blocked", phase: "discovery", code: "ci_runtime_required", remedy: "Rerun with --ci-runtime",
			prepare: func(t *testing.T) {
				buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - npm test\n")
			},
		},
		{
			name: "CI confirmation", outcome: "blocked", phase: "discovery", code: "ci_setup_confirmation_required", remedy: "Rerun with --ci-confirm",
			prepare: func(t *testing.T) {
				jenkinsRepository(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages {\n    stage('Test') { tools { nodejs '22' }\n      steps { sh 'npm test' } }\n  }\n}\n")
				useCIStatements(t, []string{"node=22"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.prepare(t)
			t.Setenv("DO_NOT_TRACK", "")
			t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
			type usageEvent struct {
				EventName string `json:"event_name"`
				Outcome   string `json:"outcome"`
				Phase     string `json:"phase"`
				Code      string `json:"code"`
				ExitCode  int    `json:"exit_code"`
			}
			events := make(chan usageEvent, 2)
			attempt := usagetelemetry.Start("onboard", "discovery", usagetelemetry.Config{
				Version: "test", ClientToken: "test-token", IntakeSite: "datadoghq.com",
				Transport: humanUsageTransport(func(r *http.Request) (*http.Response, error) {
					var event usageEvent
					if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
						return nil, err
					}
					events <- event
					return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
				}),
			})
			require.NotNil(t, attempt)
			output := humanUsageOutput{Attempt: attempt}
			err := StartWithOptions(&output, StartOptions{Human: true, Tracer: "5.133.0"})
			require.ErrorContains(t, err, tc.remedy)
			require.Empty(t, output.String(), "human error paths must not print a JSON result or alter terminal output")
			attempt.Finish(1, false)
			require.Len(t, events, 2)
			for range 2 {
				event := <-events
				if event.EventName == "ddtest.command.finished" {
					require.Equal(t, tc.outcome, event.Outcome)
					require.Equal(t, tc.phase, event.Phase)
					require.Equal(t, tc.code, event.Code)
					require.Equal(t, 1, event.ExitCode)
				}
			}
		})
	}
}
