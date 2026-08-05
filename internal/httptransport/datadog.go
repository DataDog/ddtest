package httptransport

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	defaultAgentHost = "localhost"
	defaultAgentPort = "8126"
	defaultSite      = "datadoghq.com"
)

var defaultAgentSocketPath = "/var/run/datadog/apm.socket"

// DatadogConnection contains the shared Agent/agentless configuration used by
// Datadog HTTP clients. Products remain responsible for constructing their
// product-specific endpoint paths and headers.
type DatadogConnection struct {
	Agentless    bool
	APIKey       string
	Site         string
	AgentlessURL string
	AgentURL     *url.URL
}

// ResolveDatadogConnection resolves the standard ddtest Agent/agentless
// environment configuration.
func ResolveDatadogConnection() (DatadogConnection, error) {
	if utils.BoolEnv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, false) {
		apiKey := os.Getenv(constants.APIKeyEnvironmentVariable)
		if apiKey == "" {
			return DatadogConnection{}, errors.New("datadog API key must not be empty in agentless mode")
		}
		site := os.Getenv("DD_SITE")
		if site == "" {
			site = defaultSite
		}
		return DatadogConnection{
			Agentless:    true,
			APIKey:       apiKey,
			Site:         site,
			AgentlessURL: os.Getenv(constants.TestOptimizationAgentlessURLEnvironmentVariable),
		}, nil
	}

	return DatadogConnection{AgentURL: AgentURLFromEnv()}, nil
}

// AgentURLFromEnv resolves the trace Agent URL using DD_TRACE_AGENT_URL,
// DD_AGENT_HOST, DD_TRACE_AGENT_PORT, the default Unix socket, then
// localhost:8126, in that order. Invalid explicit URLs are ignored.
func AgentURLFromEnv() *url.URL {
	if configured := os.Getenv("DD_TRACE_AGENT_URL"); configured != "" {
		agentURL, err := url.Parse(configured)
		if err != nil {
			slog.Warn("Failed to parse DD_TRACE_AGENT_URL", "error", err.Error())
		} else if validAgentURL(agentURL) {
			return agentURL
		} else {
			slog.Warn("Unsupported or incomplete Agent URL; using default Agent configuration", "url", configured)
		}
	}

	host, hostConfigured := os.LookupEnv("DD_AGENT_HOST")
	port, portConfigured := os.LookupEnv("DD_TRACE_AGENT_PORT")
	if host == "" {
		host = defaultAgentHost
		hostConfigured = false
	}
	if port == "" {
		port = defaultAgentPort
		portConfigured = false
	}
	httpURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}
	if hostConfigured || portConfigured {
		return httpURL
	}
	if _, err := os.Stat(defaultAgentSocketPath); err == nil {
		return &url.URL{Scheme: "unix", Path: defaultAgentSocketPath}
	}
	return httpURL
}

func validAgentURL(agentURL *url.URL) bool {
	switch agentURL.Scheme {
	case "http", "https":
		return agentURL.Host != ""
	case "unix":
		return agentURL.Path != ""
	default:
		return false
	}
}

// NewHTTPClient returns an uninstrumented HTTP client suitable for Datadog
// intake requests.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// AgentHTTPTransport returns an HTTP-compatible Agent base URL and matching
// client, including Unix-socket adaptation when required. The input URL is not
// mutated.
func AgentHTTPTransport(agentURL *url.URL, timeout time.Duration) (*url.URL, *http.Client) {
	resolvedURL := *agentURL
	if resolvedURL.Scheme == "unix" {
		return UnixSocketURL(resolvedURL.Path), UnixSocketClient(resolvedURL.Path, timeout)
	}
	return &resolvedURL, NewHTTPClient(timeout)
}
