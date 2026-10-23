package onboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestCITracerSelectorsUseConfiguredRegistryAndAuthentication(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is required")
	}
	requests := 0
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer synthetic-ci-metadata-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"name":"dd-trace","dist-tags":{"latest":"6.16.0"},"versions":{"5.99.0":{"name":"dd-trace","version":"5.99.0","engines":{"node":">=18"}},"5.100.0":{"name":"dd-trace","version":"5.100.0","engines":{"node":">=18"}},"6.16.0":{"name":"dd-trace","version":"6.16.0","engines":{"node":">=22"}}}}`)
	}))
	defer registry.Close()
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"private-registry-fixture","private":true}`), 0600))
	configuration := "registry=" + registry.URL + "/\n//" + strings.TrimPrefix(registry.URL, "http://") + "/:_authToken=synthetic-ci-metadata-token\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, ".npmrc"), []byte(configuration), 0600))
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		t.Setenv(key, registry.URL+"/")
	}
	for _, tc := range []struct{ selector, version, node string }{{"5", "5.100.0", ">=18"}, {"^5", "5.100.0", ">=18"}, {"5.x", "5.100.0", ">=18"}, {"5.99.0", "5.99.0", ">=18"}, {"latest", "6.16.0", ">=22"}} {
		result, err := resolveRequirement(context.Background(), nil, githubAction+"@v3", tc.selector, platform.NewJavaScript().ResolveRegistryTracer)
		require.NoError(t, err)
		require.Equal(t, tracerRequirement{Version: tc.version, Node: tc.node, Requested: tc.selector}, result)
	}
	require.Positive(t, requests, "all CI metadata must be obtained through the configured registry")
}
