package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReleaseScopeDoesNotHideTestHooksOrShellCode(t *testing.T) {
	for _, tc := range []struct{ command, hook string }{
		{"pnpm version ${{ inputs.version }}", "preversion"},
		{"pnpm version patch", "postversion"},
		{"pnpm publish", "prepublishOnly"},
		{"pnpx pkg-pr-new publish --compact", "prepare"},
		{"pnpm version ${{ inputs.version }} && jest", ""},
		{"pnpm version $(node run-tests.js)", ""},
		{"git config core.hooksPath test-hooks", ""},
		{"pnpm publish && node unknown-tests.js", ""},
	} {
		t.Run(tc.command+tc.hook, func(t *testing.T) {
			root := t.TempDir()
			scripts := map[string]string{"publish": "clean-publish"}
			if tc.hook != "" {
				scripts[tc.hook] = "jest"
			}
			writeScripts(t, root, scripts)
			result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: tc.command}, "javascript", "jest")
			require.False(t, result.Review, result)
			require.NotEmpty(t, result.Reason, result)
		})
	}
	root := t.TempDir()
	writeScripts(t, root, map[string]string{})
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "git push origin main && jest"}, "javascript", "jest")
	require.True(t, result.Matched)
	require.False(t, result.Review)
}
