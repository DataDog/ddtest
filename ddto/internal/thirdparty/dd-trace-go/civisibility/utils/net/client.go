// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

const (
	// DefaultMaxRetries is the default number of retries for a request.
	DefaultMaxRetries int = 3
	// DefaultBackoff is the default backoff time for a request.
	DefaultBackoff time.Duration = 100 * time.Millisecond
)

type (
	// Client is an interface for sending requests to the Datadog backend.
	Client interface {
		GetSettings() (*SettingsResponseData, error)
		GetKnownTests() (*KnownTestsResponseData, error)
		GetCommits(localCommits []string) ([]string, error)
		SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error)
		SendCoveragePayload(ciTestCovPayload io.Reader) error
		SendCoveragePayloadWithFormat(ciTestCovPayload io.Reader, format string) error
		SendCoverageReport(report io.Reader, format string) error
		GetSkippableTests() (*SkippableTestsResponse, error)
		GetTestManagementTests() (*TestManagementTestsResponseDataModules, error)
		SendLogs(logsPayload io.Reader) error
	}

	// coverageClient is an interface for sending coverage reports to the Datadog backend.
	coverageClient interface {
		Client
		SetCoverageFlags([]string)
	}

	// client is a client for sending requests to the Datadog backend.
	client struct {
		id                  string
		agentless           bool
		baseURL             string
		environment         string
		serviceName         string
		workingDirectory    string
		repositoryURL       string
		commitSha           string
		commitMessage       string
		headCommitSha       string
		headCommitMessage   string
		branchName          string
		testConfigurations  testConfigurations
		coverageReportFlags []string
		// readCacheScopeIdentity stores the short-lived read-cache scope derived from already-resolved CI tags.
		readCacheScopeIdentity readCacheScopeIdentity
		headers                map[string]string
		handler                *RequestHandler
	}

	// testConfigurations represents the test configurations.
	testConfigurations struct {
		OsPlatform          string            `json:"os.platform,omitempty"`
		OsVersion           string            `json:"os.version,omitempty"`
		OsArchitecture      string            `json:"os.architecture,omitempty"`
		RuntimeName         string            `json:"runtime.name,omitempty"`
		RuntimeArchitecture string            `json:"runtime.architecture,omitempty"`
		RuntimeVersion      string            `json:"runtime.version,omitempty"`
		Custom              map[string]string `json:"custom,omitempty"`
	}
)

var (
	_ Client         = &client{}
	_ coverageClient = &client{}

	// telemetryInit is used to initialize the telemetry client.
	telemetryInit sync.Once
)

// NewClientWithServiceNameAndSubdomain creates a new client with the given service name and subdomain.
func NewClientWithServiceNameAndSubdomain(serviceName, subdomain string) Client {
	client, startTelemetry := newClientWithServiceNameAndSubdomain(serviceName, subdomain)
	if startTelemetry != nil {
		startTelemetry(nil)
	}
	return client
}

// NewClientWithConcurrentTelemetry overlaps the initial telemetry flush with
// settings requests. The caller must defer the returned wait function before
// doing any work, including error paths, and finish waiting before running tests.
// Other constructors retain synchronous telemetry initialization.
func NewClientWithConcurrentTelemetry(serviceName string) (Client, func()) {
	client, startTelemetry := newClientWithServiceNameAndSubdomain(serviceName, "api")
	if startTelemetry == nil {
		return client, func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		startTelemetry(nil)
	}()
	return client, func() { <-done }
}

func newClientWithServiceNameAndSubdomain(serviceName, subdomain string) (Client, func(*cliTelemetryOwner)) {
	ciTags := utils.GetCITagsReadOnly()

	// get the environment
	environment := env.Get("DD_ENV")
	if environment == "" {
		environment = "none"
	}

	// get the service name
	if serviceName == "" {
		serviceName = env.Get("DD_SERVICE")
		if serviceName == "" {
			serviceName = utils.ServiceFromCodeOwners()
		}
		if serviceName == "" {
			serviceName = repositoryServiceName(ciTags[constants.GitRepositoryURL])
		}
	}

	// get all custom configuration (test.configuration.*)
	var customConfiguration map[string]string
	if v := env.Get("DD_TAGS"); v != "" {
		prefix := "test.configuration."
		for k, v := range internal.ParseTagString(v) {
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
	// socket is the Agent's Unix socket, which baseURL names only lossily.
	var socket string

	agentlessEnabled := internal.BoolEnv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, false)
	if agentlessEnabled {
		// Agentless mode is enabled.
		apiKeyValue = env.Get(constants.APIKeyEnvironmentVariable)
		if apiKeyValue == "" {
			log.Error("An API key is required for agentless mode. Use the DD_API_KEY env variable to set it")
			return nil, nil
		}

		defaultHeaders["dd-api-key"] = apiKeyValue

		// Check for a custom agentless URL.
		agentlessURL := env.Get(constants.CIVisibilityAgentlessURLEnvironmentVariable)

		if agentlessURL == "" {
			// Use the standard agentless URL format.
			site := "datadoghq.com"
			if v := env.Get("DD_SITE"); v != "" {
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

		agentURL = internal.AgentURLFromEnv()
		if agentURL.Scheme == "unix" {
			// If we're connecting over UDS we can just rely on the agent to provide the hostname
			log.Debug("connecting to agent over unix, do not set hostname on any traces")
			requestHandler = NewRequestHandlerWithClient(internal.UDSClient(agentURL.Path, 10*time.Second))
			socket = agentURL.Path
			agentURL = internal.UnixDataSocketURL(agentURL.Path)
		} else {
			requestHandler = NewRequestHandler()
		}

		baseURL = agentURL.String()
	}

	// create random id (the backend associate all transactions with the client request)
	id := strconv.FormatUint(rand.Uint64()&math.MaxInt64, 10)
	defaultHeaders["trace_id"] = id
	defaultHeaders["parent_id"] = id

	log.Debug("ciVisibilityHttpClient: new client created [id: %s, agentless: %t, url: %s, env: %s, serviceName: %s, subdomain: %s]",
		id, agentlessEnabled, baseURL, environment, serviceName, subdomain)

	var startTelemetry func(owner *cliTelemetryOwner)
	if !telemetry.Disabled() {
		startTelemetry = func(owner *cliTelemetryOwner) {
			telemetryInit.Do(func() {
				cfg := telemetry.ClientConfig{
					HTTPClient: requestHandler.Client,
					APIKey:     apiKeyValue,
				}
				if agentURL != nil {
					cfg.AgentURL = agentURL.String()
				}
				// A test process that ddto handed its state reports the
				// application of ddto's Git worker, which sends app-started,
				// with its product and configuration, and app-closing.
				handed := false
				if owner != nil {
					cfg.Shared, cfg.SharedOwner, cfg.SharedSeqID = &owner.app, true, owner.sequence.Next
				} else if cli, ok := utils.HandedCLITelemetry(); ok {
					cfg.Shared, cfg.SharedSeqID, cfg.SendFailed = &cli.App, cli.Sequence.Next, cli.Failed
					handed = true
				}
				if !handed {
					telemetry.ProductStarted(telemetry.NamespaceCIVisibility)
					telemetry.RegisterAppConfigs(
						telemetry.Configuration{Name: "service", Value: serviceName},
						telemetry.Configuration{Name: "env", Value: environment},
						telemetry.Configuration{Name: "agentless", Value: agentlessEnabled},
						telemetry.Configuration{Name: "test_session_name", Value: ciTags[constants.TestSessionName]},
					)
				}
				if telemetry.GlobalClient() != nil {
					return
				}
				client, err := telemetry.NewClient(serviceName, environment, env.Get("DD_VERSION"), cfg)
				if err != nil {
					log.Debug("civisibility: failed to create telemetry client: %s", err.Error())
					return
				}
				telemetry.StartApp(client)
			})
		}
	}

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

	return &client{
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
		testConfigurations: testConfigurations{
			OsPlatform:     ciTags[constants.OSPlatform],
			OsVersion:      ciTags[constants.OSVersion],
			OsArchitecture: ciTags[constants.OSArchitecture],
			RuntimeName:    ciTags[constants.RuntimeName],
			RuntimeVersion: ciTags[constants.RuntimeVersion],
			Custom:         customConfiguration,
		},
		readCacheScopeIdentity: cliReadCacheScopeIdentity(newReadCacheScopeIdentity(ciTags), cliStateDir(), apiKeyValue, socket),
		headers:                defaultHeaders,
		handler:                requestHandler,
	}, startTelemetry
}

// requestContext returns the context that stops c's requests.
func (c *client) requestContext() context.Context {
	if c.handler == nil {
		return context.Background()
	}
	return c.handler.context()
}

// repositoryServiceName returns the service named after the repository, the
// last resort for a test process's service.
func repositoryServiceName(repoURL string) string {
	// regex to sanitize the repository url to be used as a service name
	repoRegex := regexp.MustCompile(`(?m)/([a-zA-Z0-9\-_.]*)$`)
	matches := repoRegex.FindStringSubmatch(repoURL)
	if len(matches) > 1 {
		repoURL = strings.TrimSuffix(matches[1], ".git")
	}
	return repoURL
}

// CLITestProcessClients returns copies of c, the client of ddto's Git worker,
// that request as the test processes of the packages in dirs do: with their
// Go version and architecture, and one copy per service that they report.
// Their requests then share the read cache's entries with the test
// processes'. Without dirs, the one copy keeps c's service.
func CLITestProcessClients(c Client, goVersion, goarch string, dirs []string) []Client {
	cc, ok := c.(*client)
	if !ok {
		return nil
	}
	copyFor := func(service string) Client {
		copied := *cc
		if goVersion != "" {
			copied.testConfigurations.RuntimeVersion = goVersion
		}
		if goarch != "" {
			copied.testConfigurations.OsArchitecture = goarch
		}
		copied.serviceName = service
		return &copied
	}
	if len(dirs) == 0 {
		return []Client{copyFor(cc.serviceName)}
	}
	var clients []Client
	services := map[string]bool{}
	for _, dir := range dirs {
		service := testProcessServiceName(cc.repositoryURL, dir)
		if !services[service] {
			services[service] = true
			clients = append(clients, copyFor(service))
		}
	}
	return clients
}

// testProcessServiceName returns the service that a test process of the
// package in dir selects in newClientWithServiceNameAndSubdomain.
func testProcessServiceName(repoURL, dir string) string {
	if service := env.Get("DD_SERVICE"); service != "" {
		return service
	}
	if service := utils.ServiceFromCodeOwnersForDirectory(dir); service != "" {
		return service
	}
	return repositoryServiceName(repoURL)
}

// cliStateDir returns the directory of ddto's state when this process is the
// invocation's Git worker or a test process that uses its state.
func cliStateDir() string {
	dir, record := utils.HandedCLIGitState()
	if record == nil {
		return ""
	}
	return dir
}

// cliTelemetryOwner is the telemetry application that ddto's Git worker
// owns for an invocation.
type cliTelemetryOwner struct {
	app      telemetry.SharedApp
	sequence *utils.CLITelemetrySequence
}

// CLITelemetry starts and stops the telemetry application that ddto's Git
// worker owns for an invocation; test processes that use its state report
// under it.
type CLITelemetry struct {
	owner cliTelemetryOwner
	start func(*cliTelemetryOwner)
}

// NewCLIClient creates the client of ddto's Git worker, whose requests stop
// when ctx is done. Unless telemetry is disabled, it also records the
// invocation's telemetry application under runtimeID in the state directory
// dir, before any test process starts; telemetry requests do not stop with ctx.
func NewCLIClient(ctx context.Context, dir, runtimeID string) (Client, *CLITelemetry) {
	c, start := newClientWithServiceNameAndSubdomain("", "api")
	if c == nil {
		return nil, nil
	}
	cc := c.(*client)
	cc.handler = &RequestHandler{Client: cc.handler.Client, Context: ctx}
	if start == nil || runtimeID == "" {
		return c, nil
	}
	app := telemetry.NewSharedApp(runtimeID, cc.serviceName, cc.environment, env.Get("DD_VERSION"))
	sequence, err := utils.WriteCLITelemetry(dir, app)
	if err != nil {
		log.Debug("civisibility: CLI telemetry application not recorded: %s", err.Error())
		return c, nil
	}
	return c, &CLITelemetry{owner: cliTelemetryOwner{app: app, sequence: sequence}, start: start}
}

// Start sends app-started for the invocation, which reports languageVersion,
// the Go version of its test processes, if not empty.
func (t *CLITelemetry) Start(languageVersion string) {
	if t == nil {
		return
	}
	if languageVersion != "" {
		t.owner.app.Application.LanguageVersion = languageVersion
	}
	t.start(&t.owner)
}

// Stop sends app-closing for the invocation, with the data not yet sent. It
// takes the request's number first, so that a fallback's log leaves with it.
func (t *CLITelemetry) Stop() {
	if t == nil {
		return
	}
	t.owner.sequence.Reserve()
	telemetry.StopApp()
}

// NewClientWithServiceName creates a new client with the given service name.
func NewClientWithServiceName(serviceName string) Client {
	return NewClientWithServiceNameAndSubdomain(serviceName, "api")
}

// NewClient creates a new client with the default service name.
func NewClient() Client {
	return NewClientWithServiceName("")
}

// CloseIdleConnections closes idle HTTP connections owned by this CI
// Visibility client.
func (c *client) CloseIdleConnections() {
	if c == nil || c.handler == nil {
		return
	}
	c.handler.CloseIdleConnections()
}

// getURLPath returns the full URL path for the given URL path.
func (c *client) getURLPath(urlPath string) string {
	if c.agentless {
		return fmt.Sprintf("%s/%s", c.baseURL, urlPath)
	}

	return fmt.Sprintf("%s/%s/%s", c.baseURL, "evp_proxy/v2", urlPath)
}

// getPostRequestConfig	returns a new RequestConfig for a POST request.
func (c *client) getPostRequestConfig(url string, body any) *RequestConfig {
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

// SetCoverageFlags sets the coverage report flags to the client
func (c *client) SetCoverageFlags(flags []string) {
	c.coverageReportFlags = flags
}
