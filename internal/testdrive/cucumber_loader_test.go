package testdrive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCucumberProbeExplainsAMissingSourceLoader(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/config.ts"), []byte("export default {}"), 0600))
	output := "Error [ERR_MODULE_NOT_FOUND]: Cannot find module '" + filepath.Join(root, "src/config") + ".js' imported from " + filepath.Join(root, "features/support/world.ts")

	reason := missingSourceLoader(output, "-r dd-trace/ci/init")
	require.Contains(t, reason, "config.js is only available as config.ts")
	require.Contains(t, reason, "no source loader such as tsx was active")
	require.Empty(t, missingSourceLoader(output, "-r dd-trace/ci/init --import tsx"), "the loader was active; the cause is elsewhere")
	require.Empty(t, missingSourceLoader(strings.ReplaceAll(output, "config", "absent"), "-r dd-trace/ci/init"), "no TypeScript sibling")
	require.Equal(t, reason, probeControlDiagnostic(validationRun{missingSourceLoader: reason, SuiteErrors: []string{"ERR_MODULE_NOT_FOUND"}}))
}

// Probes run nyc and cucumber-js by their resolved scripts, bypassing pnpm's
// shims, which export NODE_PATH so plugins like babel-plugin-istanbul resolve.
func TestCucumberProbeKeepsPnpmShimNodePath(t *testing.T) {
	bin := t.TempDir()
	shim := func(name, path string) {
		body := "#!/bin/sh\nbasedir=$(dirname \"$(echo \"$0\" | sed -e 's,\\\\,/,g')\")\n\nif [ -z \"$NODE_PATH\" ]; then\n  export NODE_PATH=\"" + path + "\"\nelse\n  export NODE_PATH=\"" + path + ":$NODE_PATH\"\nfi\nexec node \"$basedir/../x.js\" \"$@\"\n"
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0700))
	}
	shim("nyc", "/repo/node_modules/.pnpm/nyc@18.0.0/node_modules:/repo/node_modules/.pnpm/node_modules")
	shim("cucumber-js", "$basedir/../.pnpm/@cucumber+cucumber@13.2.1/node_modules:/repo/node_modules/.pnpm/node_modules")

	names := shimNames("/usr/bin/node", []string{"/repo/node_modules/.pnpm/nyc@18.0.0/node_modules/nyc/bin/nyc.js", "--check-coverage=false", "/usr/bin/node", "/repo/node_modules/.pnpm/@cucumber+cucumber@13.2.1/node_modules/@cucumber/cucumber/bin/cucumber.js", "--config", "probe.cjs"})
	require.Equal(t, []string{"nyc", "cucumber-js"}, names)
	require.Equal(t, []string{
		"/repo/node_modules/.pnpm/nyc@18.0.0/node_modules",
		"/repo/node_modules/.pnpm/node_modules",
		bin + "/../.pnpm/@cucumber+cucumber@13.2.1/node_modules",
	}, shimNodePath(bin, names...))
	require.Empty(t, shimNodePath(t.TempDir(), names...), "npm shims are symlinks without NODE_PATH")
}
