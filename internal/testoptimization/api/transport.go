// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/DataDog/ddtest/civisibility"
	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/runmetadata"
)

const (
	// DefaultMaxRetries is the default number of retries for a request.
	DefaultMaxRetries int = 3
	// DefaultBackoff is the default backoff time for a request.
	DefaultBackoff time.Duration = 100 * time.Millisecond
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
		GetSkippableTests() (correlationID string, skippables SkippableTests, err error)
		GetSkippableTestsRawResponse() json.RawMessage
		GetTestManagementTests() (*TestManagementTestsResponseDataModules, error)
		GetTestManagementTestsRawResponse() json.RawMessage
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
		headers            map[string]string
		handler            *RequestHandler

		settingsRawResponse            json.RawMessage
		knownTestsRawResponse          json.RawMessage
		skippableTestsRawResponse      json.RawMessage
		testManagementTestsRawResponse json.RawMessage
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
	ciTags := environment.GetCITags()

	// get the environment
	environment := os.Getenv("DD_ENV")
	if environment == "" {
		environment = "none"
	}

	// get the service name
	if serviceName == "" {
		serviceName = runmetadata.ResolveServiceName(ciTags[git.GitRepositoryURL])
	}

	// get all custom configuration (test.configuration.*)
	var customConfiguration map[string]string
	if v := os.Getenv("DD_TAGS"); v != "" {
		prefix := "test.configuration."
		for k, v := range civisibility.ParseTagString(v) {
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
	var agentURL *url.URL
	var apiKeyValue string

	agentlessEnabled := civisibility.BoolEnv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, false)
	if agentlessEnabled {
		// Agentless mode is enabled.
		apiKeyValue = os.Getenv(constants.APIKeyEnvironmentVariable)
		if apiKeyValue == "" {
			slog.Error("An API key is required for agentless mode. Use the DD_API_KEY env variable to set it")
			return nil
		}

		defaultHeaders["dd-api-key"] = apiKeyValue

		// Check for a custom agentless URL.
		agentlessURL := os.Getenv(constants.TestOptimizationAgentlessURLEnvironmentVariable)

		if agentlessURL == "" {
			// Use the standard agentless URL format.
			site := "datadoghq.com"
			if v := os.Getenv("DD_SITE"); v != "" {
				site = v
			}

			baseURL = fmt.Sprintf("https://%s.%s", subdomain, site)
		} else {
			// Use the custom agentless URL.
			baseURL = agentlessURL
		}

		requestHandler = NewRequestHandler()
	} else {
		// Use agent mode with the EVP proxy.
		defaultHeaders["X-Datadog-EVP-Subdomain"] = subdomain

		agentURL = civisibility.AgentURLFromEnv()
		if agentURL.Scheme == "unix" {
			// If we're connecting over UDS we can just rely on the agent to provide the hostname
			slog.Debug("connecting to agent over unix, do not set hostname on any traces")
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}
			requestHandler = NewRequestHandlerWithClient(&http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyFromEnvironment,
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						return dialer.DialContext(ctx, "unix", (&net.UnixAddr{
							Name: agentURL.Path,
							Net:  "unix",
						}).String())
					},
					MaxIdleConns:          100,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
				},
				Timeout: 10 * time.Second,
			})
			// TODO(darccio): use internal.UnixDataSocketURL instead
			agentURL = &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("UDS_%s", strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(agentURL.Path)),
			}
		} else {
			requestHandler = NewRequestHandler()
		}

		baseURL = agentURL.String()
	}

	// create random id (the backend associate all transactions with the client request)
	id := fmt.Sprint(rand.Uint64() & math.MaxInt64)
	defaultHeaders["trace_id"] = id
	defaultHeaders["parent_id"] = id

	slog.Debug("ciVisibilityHttpClient: new client created [id: %s, agentless: %t, url: %s, env: %s, serviceName: %s, subdomain: %s]",
		id, agentlessEnabled, baseURL, environment, serviceName, subdomain)

	// we try to get the branch name
	bName := ciTags[git.GitBranch]
	if bName == "" {
		// if not we try to use the tag (checkout over a tag)
		bName = ciTags[git.GitTag]
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
		repositoryURL:     ciTags[git.GitRepositoryURL],
		commitSha:         ciTags[git.GitCommitSHA],
		commitMessage:     ciTags[git.GitCommitMessage],
		headCommitSha:     ciTags[git.GitHeadCommit],
		headCommitMessage: ciTags[git.GitHeadMessage],
		branchName:        bName,
		testConfigurations: testConfigurations{
			OsPlatform:     ciTags[constants.OSPlatform],
			OsVersion:      ciTags[constants.OSVersion],
			OsArchitecture: ciTags[constants.OSArchitecture],
			RuntimeName:    ciTags[constants.RuntimeName],
			RuntimeVersion: ciTags[constants.RuntimeVersion],
			Custom:         customConfiguration,
		},
		headers: defaultHeaders,
		handler: requestHandler,
	}
}

// NewTransportWithServiceName creates a new transport with the given service name.
func NewTransportWithServiceName(serviceName string) Transport {
	return NewTransportWithServiceNameAndSubdomain(serviceName, "api")
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

func (c *transport) GetTestManagementTestsRawResponse() json.RawMessage {
	return cloneRawMessage(c.testManagementTestsRawResponse)
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
		Format:     FormatJSON,
		Compressed: false,
		Files:      nil,
		MaxRetries: DefaultMaxRetries,
		Backoff:    DefaultBackoff,
	}
}
