package platform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryMetadataFailureDoesNotGuessVersionAndCleansCache(t *testing.T) {
	for _, tc := range []struct{ code, reason string }{
		{"ENOTFOUND", "registry network failure"},
		{"ECONNREFUSED", "registry network failure"},
		{"EPERM", "filesystem failure"},
		{"ENOSPC", "filesystem failure"},
		{"E401", "npm authentication"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cause := errors.New("npm exit 1")
			executor := &fakeCommandExecutor{responses: []commandResponse{{stderr: []byte("npm error code " + tc.code), err: cause}}}
			installer := &JavaScript{executor: executor}
			selection, err := installer.ResolveRegistryTracer(t.Context(), "latest")
			require.ErrorIs(t, err, cause)
			var metadata *MetadataError
			require.ErrorAs(t, err, &metadata)
			want := tc.code
			if tc.code == "E401" {
				want = "registry_metadata_failed"
			}
			require.Equal(t, want, metadata.Code)
			require.Empty(t, selection.Version)
			require.Len(t, executor.commands, 1, "no guessed fallback request")
			cache, ok := strings.CutPrefix(executor.commands[0].args[7], "--cache=")
			require.True(t, ok)
			require.NoDirExists(t, cache)
		})
	}
}

func TestRegistryMetadataPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("process terminated")}}}
	installer := &JavaScript{executor: executor}
	_, err := installer.ResolveRegistryTracer(ctx, "latest")
	require.ErrorIs(t, err, context.Canceled)
}

func TestRegistryMetadataUsesPrivateCacheAndConfiguredRegistry(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is required for the local-registry integration check")
	}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dd-trace" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"name":"dd-trace","dist-tags":{"latest":"6.17.0"},"versions":{"6.17.0":{"name":"dd-trace","version":"6.17.0","engines":{"node":">=22"}}}}`)
	}))
	defer registry.Close()
	// A file cannot be used as npm's cache directory. The lookup must not use it.
	blocked := filepath.Join(t.TempDir(), "customer-cache")
	require.NoError(t, os.WriteFile(blocked, []byte("customer data"), 0400))
	for _, key := range []string{"npm_config_cache", "NPM_CONFIG_CACHE"} {
		t.Setenv(key, blocked)
	}
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		t.Setenv(key, registry.URL)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	installer := NewJavaScript()
	// A closed local registry must fail without selecting a version. Retrying
	// against the restored registry must work with the same blocked parent cache.
	unavailable := httptest.NewServer(http.NotFoundHandler())
	unavailable.Close()
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		t.Setenv(key, unavailable.URL)
	}
	failed, err := installer.ResolveRegistryTracer(t.Context(), "latest")
	var metadata *MetadataError
	require.ErrorAs(t, err, &metadata)
	require.Equal(t, "ECONNREFUSED", metadata.Code)
	require.Empty(t, failed.Version)
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		t.Setenv(key, registry.URL)
	}
	selected, err := installer.ResolveRegistryTracer(t.Context(), "latest")
	require.NoError(t, err)
	require.Equal(t, "6.17.0", selected.Version)
	data, err := os.ReadFile(blocked)
	require.NoError(t, err)
	require.Equal(t, "customer data", string(data))
	files, err := filepath.Glob(filepath.Join(tmp, "ddtest-npm-metadata-*"))
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestPnPTracerMetadataUsesProjectLoaderWithoutInitializingTracer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TMPDIR", t.TempDir())
	tracer := filepath.Join(root, "cache", "dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(tracer, "package.json"), []byte(`{"version":"5.132.0","engines":{"node":">=18"}}`), 0600))
	init := filepath.Join(tracer, "ci", "init.js")
	require.NoError(t, os.WriteFile(init, []byte(`require('fs').writeFileSync('TRACER_WAS_INITIALIZED','bad')`), 0600))
	loader := fmt.Sprintf(`const m=require('module');const original=m._resolveFilename;m._resolveFilename=function(request,...args){if(request.startsWith('node:'))throw new Error('Classic PnP does not recognize node: builtins');if(request==='dd-trace/ci/init')return %q;return original.call(this,request,...args)}`, init)
	require.NoError(t, os.WriteFile(".pnp.cjs", []byte(loader), 0600))
	packageRoot := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(packageRoot, 0700))
	t.Chdir(packageRoot)
	selected, err := NewJavaScript().ResolveTestdriveTracer(t.Context(), TracerOptions{Version: "latest"})
	require.NoError(t, err)
	require.Equal(t, "project", selected.Source)
	require.Equal(t, "5.132.0", selected.Version)
	require.NoFileExists(t, "TRACER_WAS_INITIALIZED")
	require.NoDirExists(t, "node_modules")
	external := filepath.Join(t.TempDir(), "external tracer", "dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(external, "ci"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(external, "package.json"), []byte(`{"version":"6.17.0","engines":{"node":">=22"}}`), 0600))
	externalInit := filepath.Join(external, "ci", "init.js")
	require.NoError(t, os.WriteFile(externalInit, []byte(`throw new Error('metadata must not initialize tracer')`), 0600))
	for _, tc := range []struct{ name, nodeOptions, tracePackage string }{
		{"package preload", "", externalInit},
		{"Node preload", "--require " + fmt.Sprintf("%q", externalInit), ""},
		{"Node preload takes precedence", "--require " + fmt.Sprintf("%q", externalInit), init},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NODE_OPTIONS", tc.nodeOptions)
			t.Setenv("DD_TRACE_PACKAGE", tc.tracePackage)
			selected, err := NewJavaScript().ResolveTestdriveTracer(t.Context(), TracerOptions{Version: "latest"})
			require.NoError(t, err)
			require.Equal(t, "project", selected.Source)
			require.Equal(t, "6.17.0", selected.Version)
			require.Equal(t, ">=22", selected.Node)
			require.NoFileExists(t, "TRACER_WAS_INITIALIZED")
		})
	}
	t.Setenv("NODE_OPTIONS", "--require "+fmt.Sprintf("%q", externalInit))
	options := TracerOptions{Version: "latest", Environment: map[string]string{"NODE_OPTIONS": "--require " + fmt.Sprintf("%q", filepath.Join(root, ".pnp.cjs")), "DD_TRACE_PACKAGE": ""}}
	detected, err := NewJavaScript().DetectTracer(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, init, detected)
	selected, err = NewJavaScript().ResolveTestdriveTracer(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "5.132.0", selected.Version)
	options.Directory = t.TempDir()
	installation, err := NewJavaScript().InstallTestdriveTracer(t.Context(), options)
	require.NoError(t, err)
	require.True(t, installation.Project)
	require.Equal(t, init, installation.Path)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".pnp.cjs"), []byte(`throw new Error('broken project loader')`), 0600))
	_, err = NewJavaScript().ResolveTestdriveTracer(t.Context(), TracerOptions{Version: "latest"})
	require.Error(t, err)
}

func TestConfigurationTracerEnvironmentOverridesCallerForEveryStage(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "-r /caller/dd-trace/ci/init.js")
	t.Setenv("DD_TRACE_PACKAGE", "/caller/dd-trace/ci/init.js")
	root := t.TempDir()
	t.Chdir(root)
	tracer := filepath.Join(root, "node_modules/dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(tracer, "package.json"), []byte(`{"version":"5.132.0","engines":{"node":">=18"}}`), 0600))
	path := filepath.Join(tracer, "ci/init.js")
	executor := &fakeCommandExecutor{responses: []commandResponse{{output: []byte(path)}, {output: []byte(path)}, {output: []byte(path)}}}
	installer := &JavaScript{executor: executor}
	options := TracerOptions{Directory: t.TempDir(), Command: filepath.Join(root, "explicit", "node"), Environment: map[string]string{"NODE_OPTIONS": "", "DD_TRACE_PACKAGE": ""}}
	preload, err := installer.DetectTracer(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, path, preload)
	selection, err := installer.ResolveTestdriveTracer(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "5.132.0", selection.Version)
	installation, err := installer.InstallTestdriveTracer(t.Context(), options)
	require.NoError(t, err)
	require.True(t, installation.Project)
	require.Len(t, executor.commands, 3, "none of the stages installs a fallback")
	for i, c := range executor.commands {
		require.Equal(t, options.Command, c.name)
		require.Contains(t, c.args, "dd-trace/ci/init")
		require.Empty(t, executor.envs[i]["NODE_OPTIONS"])
		require.Equal(t, "false", executor.envs[i]["DD_CIVISIBILITY_ENABLED"])
	}
}
