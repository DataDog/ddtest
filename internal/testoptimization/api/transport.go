// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/httptransport"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
)

const (
	ddTagsDelimiter = ":"
)

type (
	// Transport sends requests to the Datadog backend.
	Transport interface {
		GetSettings() (*SettingsResponseData, error)
		GetSettingsRawResponse() json.RawMessage
		GetKnownTests() (*KnownTestsResponseData, error)
		GetKnownTestsRawResponse() json.RawMessage
		GetTestSuiteDurations() *TestSuiteDurationsResponseData
		GetCommits(localCommits []string) ([]string, error)
		SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error)
		GetSkippableTests() (correlationID string, skippables Skippables, err error)
		GetSkippableTestsRawResponse() json.RawMessage
		GetTestManagementTests() (*TestManagementTestsResponseDataModules, error)
		GetTestManagementTestsRawResponse() json.RawMessage
		BackendRequestTimings() BackendRequestTimings
	}

	BackendRequestTimings struct {
		Settings            time.Duration
		KnownTests          time.Duration
		Skippables          time.Duration
		TestManagementTests time.Duration
		TestSuiteDurations  time.Duration
	}

	// transport sends requests to the Datadog backend.
	transport struct {
		id                 string
		agentless          bool
		baseURL            string
		environment        string
		serviceName        string
		workingDirectory   string
		repositoryURL      string
		commitSha          string
		commitMessage      string
		headCommitSha      string
		headCommitMessage  string
		branchName         string
		testConfigurations testConfigurations
		testSkippingLevel  settings.TestSkippingLevel
		headers            map[string]string
		handler            *RequestHandler
		telemetryClient    telemetry.Client

		settingsRawResponse            json.RawMessage
		knownTestsRawResponse          json.RawMessage
		skippableTestsRawResponse      json.RawMessage
		testManagementTestsRawResponse json.RawMessage
		backendRequestTimings          BackendRequestTimings
	}

	// testConfigurations represents the test configurations.
	testConfigurations struct {
		OsPlatform          string            `json:"os.platform,omitempty"`
		OsVersion           string            `json:"os.version,omitempty"`
		OsArchitecture      string            `json:"os.architecture,omitempty"`
		RuntimeName         string            `json:"runtime.name,omitempty"`
		RuntimeArchitecture string            `json:"runtime.architecture,omitempty"`
		RuntimeVersion      string            `json:"runtime.version,omitempty"`
		TestBundle          string            `json:"test.bundle,omitempty"`
		Custom              map[string]string `json:"custom,omitempty"`
	}
)

var (
	_ Transport = &transport{}
)

// NewTransportWithServiceNameAndSubdomain creates a new transport with the given service name and subdomain.
func NewTransportWithServiceNameAndSubdomain(serviceName, subdomain string) Transport {
	return newTransportWithServiceNameAndSubdomain(serviceName, subdomain, settings.TestSkippingLevelTest, telemetry.NoopClient())
}

func NewTransportWithServiceNameAndTestSkippingLevel(serviceName string, testSkippingLevel settings.TestSkippingLevel) Transport {
	return NewTransportWithTelemetry(serviceName, testSkippingLevel, telemetry.NoopClient())
}

// NewTransportWithTelemetry creates a transport that reports CI Visibility
// backend request metrics through telemetryClient.
func NewTransportWithTelemetry(serviceName string, testSkippingLevel settings.TestSkippingLevel, telemetryClient telemetry.Client) Transport {
	return newTransportWithServiceNameAndSubdomain(serviceName, "api", testSkippingLevel, telemetryClient)
}

func newTransportWithServiceNameAndSubdomain(serviceName, subdomain string, testSkippingLevel settings.TestSkippingLevel, telemetryClient telemetry.Client) Transport {
	ciTags := environment.GetCITags()

	// get the environment
	environment := os.Getenv("DD_ENV")
	if environment == "" {
		environment = "none"
	}

	// get the service name
	if serviceName == "" {
		serviceName = runmetadata.ResolveServiceName(ciTags[constants.GitRepositoryURL])
	}

	// get all custom configuration (test.configuration.*)
	var customConfiguration map[string]string
	if v := os.Getenv("DD_TAGS"); v != "" {
		prefix := "test.configuration."
		for k, v := range parseTagString(v) {
			if strings.HasPrefix(k, prefix) {
				if customConfiguration == nil {
					customConfiguration = map[string]string{}
				}

				customConfiguration[strings.TrimPrefix(k, prefix)] = v
			}
		}
	}

	// create default http headers and get base url
	defaultHeaders := map[string]string{}
	var baseURL string
	var requestHandler *RequestHandler
	var apiKeyValue string

	connection, err := httptransport.ResolveDatadogConnection()
	if err != nil {
		slog.Error("Failed to resolve Datadog connection", "error", err)
		return nil
	}
	agentlessEnabled := connection.Agentless
	if agentlessEnabled {
		// Agentless mode is enabled.
		apiKeyValue = connection.APIKey

		defaultHeaders["dd-api-key"] = apiKeyValue

		// Check for a custom agentless URL.
		agentlessURL := connection.AgentlessURL

		if agentlessURL == "" {
			// Use the standard agentless URL format.
			baseURL = fmt.Sprintf("https://%s.%s", subdomain, connection.Site)
		} else {
			// Use the custom agentless URL.
			baseURL = agentlessURL
		}

		requestHandler = NewRequestHandler()
	} else {
		// Use agent mode with the EVP proxy.
		defaultHeaders["X-Datadog-EVP-Subdomain"] = subdomain

		agentURL := connection.AgentURL
		if agentURL.Scheme == "unix" {
			// If we're connecting over UDS we can just rely on the agent to provide the hostname
			slog.Debug("connecting to agent over unix, do not set hostname on any traces")
			agentURL, client := httptransport.AgentHTTPTransport(agentURL, 10*time.Second)
			requestHandler = NewRequestHandlerWithClient(client)
			baseURL = agentURL.String()
		} else {
			requestHandler = NewRequestHandler()
			baseURL = agentURL.String()
		}
	}

	// create random id (the backend associate all transactions with the client request)
	id := fmt.Sprint(rand.Uint64() & math.MaxInt64)
	defaultHeaders["trace_id"] = id
	defaultHeaders["parent_id"] = id

	slog.Debug("ciVisibilityHttpClient: new client created [id: %s, agentless: %t, url: %s, env: %s, serviceName: %s, subdomain: %s]",
		id, agentlessEnabled, baseURL, environment, serviceName, subdomain)

	// we try to get the branch name
	bName := ciTags[constants.GitBranch]
	if bName == "" {
		// if not we try to use the tag (checkout over a tag)
		bName = ciTags[constants.GitTag]
	}
	if bName == "" {
		// if is still empty we assume the customer just used a detached HEAD
		bName = "auto:git-detached-head"
	}

	return &transport{
		id:                id,
		agentless:         agentlessEnabled,
		baseURL:           baseURL,
		environment:       environment,
		serviceName:       serviceName,
		workingDirectory:  ciTags[constants.CIWorkspacePath],
		repositoryURL:     ciTags[constants.GitRepositoryURL],
		commitSha:         ciTags[constants.GitCommitSHA],
		commitMessage:     ciTags[constants.GitCommitMessage],
		headCommitSha:     ciTags[constants.GitHeadCommit],
		headCommitMessage: ciTags[constants.GitHeadMessage],
		branchName:        bName,
		testSkippingLevel: testSkippingLevel,
		testConfigurations: testConfigurations{
			OsPlatform:     ciTags[constants.OSPlatform],
			OsVersion:      ciTags[constants.OSVersion],
			OsArchitecture: ciTags[constants.OSArchitecture],
			RuntimeName:    ciTags[constants.RuntimeName],
			RuntimeVersion: ciTags[constants.RuntimeVersion],
			Custom:         customConfiguration,
		},
		headers:         defaultHeaders,
		handler:         requestHandler,
		telemetryClient: telemetryClient,
	}
}

// NewTransportWithServiceName creates a new transport with the given service name.
func NewTransportWithServiceName(serviceName string) Transport {
	return NewTransportWithServiceNameAndSubdomain(serviceName, "api")
}

func parseTagString(str string) map[string]string {
	res := make(map[string]string)
	forEachStringTag(str, ddTagsDelimiter, func(key string, val string) {
		res[key] = val
	})
	return res
}

func forEachStringTag(str string, delimiter string, fn func(key string, val string)) {
	sep := " "
	if strings.Contains(str, ",") {
		sep = ","
	}
	for _, tag := range strings.Split(str, sep) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		kv := strings.SplitN(tag, delimiter, 2)
		key := strings.TrimSpace(kv[0])
		if key == "" {
			continue
		}
		var val string
		if len(kv) == 2 {
			val = strings.TrimSpace(kv[1])
		}
		fn(key, val)
	}
}

func cloneRawMessage(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func (c *transport) GetSettingsRawResponse() json.RawMessage {
	return cloneRawMessage(c.settingsRawResponse)
}

func (c *transport) GetKnownTestsRawResponse() json.RawMessage {
	return cloneRawMessage(c.knownTestsRawResponse)
}

func (c *transport) GetSkippableTestsRawResponse() json.RawMessage {
	return cloneRawMessage(c.skippableTestsRawResponse)
}

func (c *transport) getTestSkippingLevel() settings.TestSkippingLevel {
	if c.testSkippingLevel == "" {
		return settings.TestSkippingLevelTest
	}
	return c.testSkippingLevel
}

func (c *transport) GetTestManagementTestsRawResponse() json.RawMessage {
	return cloneRawMessage(c.testManagementTestsRawResponse)
}

func (c *transport) BackendRequestTimings() BackendRequestTimings {
	return c.backendRequestTimings
}

// getURLPath returns the full URL path for the given URL path.
func (c *transport) getURLPath(urlPath string) string {
	if c.agentless {
		return fmt.Sprintf("%s/%s", c.baseURL, urlPath)
	}

	return fmt.Sprintf("%s/%s/%s", c.baseURL, "evp_proxy/v2", urlPath)
}

// getPostRequestConfig	returns a new RequestConfig for a POST request.
func (c *transport) getPostRequestConfig(url string, body interface{}) *RequestConfig {
	return &RequestConfig{
		Method:     "POST",
		URL:        c.getURLPath(url),
		Headers:    c.headers,
		Body:       body,
		Format:     constants.FormatJSON,
		Compressed: false,
		Files:      nil,
		MaxRetries: constants.DefaultMaxRetries,
		Backoff:    constants.DefaultBackoff,
	}
}
