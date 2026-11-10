package testdrive

import (
	"bytes"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

type validationUsageOutput struct {
	bytes.Buffer
	status, phase, code string
}

func (o *validationUsageOutput) ObserveResult(status, phase, code string) {
	o.status, o.phase, o.code = status, phase, code
}

func TestValidationUsageReportsCurrentTypedVerdict(t *testing.T) {
	check := onboard.RuntimeCheck{Status: "compatible", Jobs: []onboard.RuntimeFinding{{Status: "compatible", Tracer: "dd-trace@5.128.0"}}}
	for _, tc := range []struct {
		name, status, phase, code string
		result                    validationResult
		wantError                 bool
	}{
		{
			name: "configuration passed", status: "passed", phase: "configuration_check",
			result: validationResult{CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, CIRuntime: &check, CISelection: compareCISelection(check, "5.128.0")},
		},
		{
			name: "configuration blocked", status: "needs_input", phase: "configuration_check", code: "configuration_check_failed", wantError: true,
			result: validationResult{CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "incompatible"}}},
		},
		{
			name: "validation failed", status: "validation_failed", phase: "validation", code: "validation_incomplete", wantError: true,
			result: validationResult{Compatibility: verdict{Status: "suspected regression"}},
		},
		{
			name: "reporting only", status: "unsupported", phase: "validation", code: "framework_unsupported",
			result: validationResult{Framework: "unsupported-framework"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output validationUsageOutput
			err := finishValidation(&output, t.TempDir(), tc.result)
			require.Equal(t, tc.wantError, err != nil)
			require.Equal(t, tc.status, output.status)
			require.Equal(t, tc.phase, output.phase)
			require.Equal(t, tc.code, output.code)
			require.NotEmpty(t, output.String(), "terminal output is still rendered")
		})
	}
}
