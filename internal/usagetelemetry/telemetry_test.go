package usagetelemetry

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func enabledEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
}

func localConfig(t *testing.T, handler http.Handler) Config {
	t.Helper()
	enabledEnvironment(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	return Config{
		Version: "1.2.3", ClientToken: "test-token", IntakeSite: "datadoghq.eu",
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// Keep the real intake path and headers, but send only to the local server.
			copy := r.Clone(r.Context())
			copy.URL.Scheme, copy.URL.Host = endpoint.Scheme, endpoint.Host
			copy.Host = endpoint.Host
			return server.Client().Transport.RoundTrip(copy)
		}),
	}
}

func TestUsageUsesClientTokenIntake(t *testing.T) {
	enabledEnvironment(t)
	for _, site := range []string{"datadoghq.com", "us3.datadoghq.com", "us5.datadoghq.com", "datadoghq.eu", "ap1.datadoghq.com", "ap2.datadoghq.com", "uk1.datadoghq.com"} {
		t.Run(site, func(t *testing.T) {
			const token = "test token/+?&"
			requests := make(chan string, 2)
			a := Start("onboard", "discovery", Config{
				Version: "test", ClientToken: token, IntakeSite: site,
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					// Inspect the production URL without rewriting it to a local server.
					requests <- r.URL.String()
					return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
				}),
			})
			require.NotNil(t, a)
			a.Finish(0, false)
			require.Len(t, requests, 2)
			for range 2 {
				endpoint, err := url.Parse(<-requests)
				require.NoError(t, err)
				require.Equal(t, "https", endpoint.Scheme)
				require.Equal(t, "browser-http-intake.logs."+site, endpoint.Host)
				require.Equal(t, "/api/v2/logs", endpoint.Path)
				require.Equal(t, url.Values{"dd-api-key": {token}}, endpoint.Query())
			}
		})
	}
}

func TestAttemptReportsLifecycleWithoutCustomerData(t *testing.T) {
	var mu sync.Mutex
	var events []event
	config := localConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/logs" || r.URL.Query().Get("dd-api-key") != "test-token" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected usage request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("DD-API-KEY") != "" || r.Header.Get("Authorization") != "" {
			t.Error("customer authentication must not be sent")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(string(body), "customer-secret") || strings.Contains(string(body), "/customer/repository") {
			t.Error("customer data in usage payload")
		}
		var e event
		if err := json.Unmarshal(body, &e); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Setenv("DD_API_KEY", "customer-secret")
	t.Setenv("DD_APP_KEY", "customer-secret")
	t.Setenv("DD_SITE", "customer-secret")
	t.Setenv("DD_TRACE_AGENT_URL", "http://customer-secret")
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", "http://customer-secret")
	a := Start("onboard", "apply", config)
	require.NotNil(t, a)
	a.ObserveResult("complete_locally", "complete", "")
	a.Finish(0, false)
	a.Finish(0, false) // Finishing twice must not double-count.
	require.Len(t, events, 2)
	byName := make(map[string]event)
	for _, e := range events {
		byName[e.EventName] = e
		require.Equal(t, "ddtest-cli", e.Service)
		require.Equal(t, "ddtest", e.Source)
		require.Equal(t, "onboard", e.Command)
		require.Equal(t, "apply", e.Mode)
		require.Equal(t, "1.2.3", e.Version)
		require.NotEmpty(t, e.OS)
		require.NotEmpty(t, e.Arch)
		require.NotEmpty(t, e.AttemptID)
		require.Equal(t, e.EventName, e.Message)
	}
	started, finished := byName["ddtest.command.started"], byName["ddtest.command.finished"]
	require.Equal(t, started.AttemptID, finished.AttemptID)
	require.False(t, finished.Date.Before(started.Date))
	require.Nil(t, started.ExitCode)
	require.Empty(t, started.Outcome)
	require.Equal(t, "succeeded", finished.Outcome)
	require.Equal(t, 0, *finished.ExitCode)
	require.NotNil(t, finished.DurationMS)
}

func TestDisabledUsageMakesNoRequests(t *testing.T) {
	for _, tc := range []struct{ name, variable, value string }{
		{"missing token", "", ""},
		{"do not track", "DO_NOT_TRACK", "1"},
		{"ddtest opt out", "DDTEST_TELEMETRY_DISABLED", "true"},
		{"instrumentation opt out", "DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false"},
		{"numeric instrumentation opt out", "DD_INSTRUMENTATION_TELEMETRY_ENABLED", "0"},
		{"unknown site", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabledEnvironment(t)
			config := Config{ClientToken: "test-token", IntakeSite: "datadoghq.com", Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("disabled telemetry attempted delivery")
				return nil, errors.New("unexpected request")
			})}
			if tc.variable != "" {
				t.Setenv(tc.variable, tc.value)
			}
			if tc.name == "missing token" {
				config.ClientToken = ""
			}
			if tc.name == "unknown site" {
				config.IntakeSite = "custom.example"
			}
			a := Start("testdrive", "validation", config)
			require.Nil(t, a)
			a.ObserveResult("failed", "validation", "validation_incomplete")
			a.Finish(1, false)
		})
	}
}

func TestUsageOutcomesAndAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name, mode, status, phase, code, outcome string
		exit                                     int
		cancelled                                bool
	}{
		{"handoff", "discovery", "ready", "onboarding", "", "handoff", 0, false},
		{"selected validation", "validation", "selected_passed", "complete", "", "succeeded", 0, false},
		{"configuration check", "configuration_check", "ready", "configuration_check", "", "succeeded", 0, false},
		{"blocked", "apply", "needs_input", "consent", "consent_required", "blocked", 1, false},
		{"metadata network", "validation", "blocked_environment", "preflight", "metadata_network", "blocked", 1, false},
		{"metadata errno", "validation", "blocked_environment", "preflight", "ENOTFOUND", "blocked", 1, false},
		{"preflight blocker", "validation", "needs_input", "preflight", "preflight_unresolved", "blocked", 1, false},
		{"open file exhaustion", "validation", "blocked_environment", "validation", "file_descriptors_exhausted", "blocked", 1, false},
		{"plain saved report", "report", "validation_failed", "report", "plain_report", "failed", 1, false},
		{"setup editing blocker", "apply", "needs_input", "apply", "dirty_workflow", "blocked", 1, false},
		{"invalid options", "discovery", "needs_input", "options", "invalid_options", "failed", 1, false},
		{"unsupported", "validation", "unsupported", "discovery", "framework_unsupported", "unsupported", 1, false},
		{"plain unsupported", "validation", "needs_input", "preparation", "framework_unsupported", "unsupported", 1, false},
		{"unsupported CI setup", "discovery", "unsupported", "onboarding", "ci_setup_unsupported", "unsupported", 1, false},
		{"validation failure", "validation", "validation_failed", "validation", "validation_incomplete", "failed", 1, false},
		{"declined", "validation", "cancelled", "consent", "", "cancelled", 0, false},
		{"interrupted", "validation", "needs_input", "validation", "", "cancelled", 130, true},
		{"write failed after success", "apply", "complete_locally", "complete", "", "failed", 1, false},
		{"unclassified error", "validation", "", "/customer/repository", "customer-secret", "failed", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var finished event
			config := localConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var e event
				if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
					t.Error(err)
				}
				if e.EventName == "ddtest.command.finished" {
					finished = e
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			a := Start("testdrive", tc.mode, config)
			a.ObserveResult(tc.status, tc.phase, tc.code)
			a.Finish(tc.exit, tc.cancelled)
			require.Equal(t, tc.outcome, finished.Outcome)
			require.Equal(t, tc.exit, *finished.ExitCode)
			if tc.name == "unclassified error" {
				require.Equal(t, "unknown", finished.Phase)
				require.Equal(t, "unknown", finished.Code)
			} else {
				require.Equal(t, tc.phase, finished.Phase)
				require.Equal(t, tc.code, finished.Code)
			}
		})
	}
}

func TestUsageDeliveryFailuresAreBoundedAndNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			config := localConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()
				w.Header().Set("Location", "/redirect-must-not-be-followed")
				w.WriteHeader(status)
			}))
			a := Start("testdrive", "validation", config)
			a.Finish(0, false)
			require.Equal(t, 2, requests)
		})
	}
	t.Run("network error", func(t *testing.T) {
		enabledEnvironment(t)
		a := Start("testdrive", "validation", Config{ClientToken: "test-token", IntakeSite: "datadoghq.com", Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})})
		a.Finish(0, false)
	})
	t.Run("timeout", func(t *testing.T) {
		config := localConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}))
		started := time.Now()
		a := Start("testdrive", "validation", config)
		a.Finish(0, false)
		require.Less(t, time.Since(started), flushTimeout+time.Second)
	})
}
