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
	loader := fmt.Sprintf(`const m=require('module');const original=m._resolveFilename;m._resolveFilename=function(request,...args){if(request==='dd-trace/ci/init')return %q;return original.call(this,request,...args)}`, init)
	require.NoError(t, os.WriteFile(".pnp.cjs", []byte(loader), 0600))
	selected, err := NewJavaScript().ResolveTestdriveTracer(t.Context(), TracerOptions{Version: "latest"})
	require.NoError(t, err)
	require.Equal(t, "project", selected.Source)
	require.Equal(t, "5.132.0", selected.Version)
	require.NoFileExists(t, "TRACER_WAS_INITIALIZED")
	require.NoDirExists(t, "node_modules")
	require.NoError(t, os.WriteFile(".pnp.cjs", []byte(`throw new Error('broken project loader')`), 0600))
	_, err = NewJavaScript().ResolveTestdriveTracer(t.Context(), TracerOptions{Version: "latest"})
	require.Error(t, err)
}
