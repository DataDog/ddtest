package testoptimization

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DataDog/ddtest/civisibility"
	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/httptransport"
)

const (
	durationsRequestType string = "ci_app_ddtest_test_suite_durations_request"
	durationsURLPath     string = "api/v2/ci/ddtest/test_suite_durations"

	defaultDurationsPageSize int = 500
	maxDurationsRetries      int = 3
)

type (
	// request types

	durationsRequest struct {
		Data durationsRequestData `json:"data"`
	}

	durationsRequestData struct {
		Type       string                     `json:"type"`
		Attributes durationsRequestAttributes `json:"attributes"`
	}

	durationsRequestAttributes struct {
		RepositoryURL string                    `json:"repository_url"`
		Service       string                    `json:"service,omitempty"`
		PageInfo      *durationsRequestPageInfo `json:"page_info,omitempty"`
	}

	durationsRequestPageInfo struct {
		PageSize  int    `json:"page_size,omitempty"`
		PageState string `json:"page_state,omitempty"`
	}

	// response types

	durationsResponse struct {
		Data struct {
			ID         string                      `json:"id"`
			Type       string                      `json:"type"`
			Attributes durationsResponseAttributes `json:"attributes"`
		} `json:"data"`
	}

	durationsResponseAttributes struct {
		TestSuites map[string]map[string]TestSuiteDurationInfo `json:"test_suites"`
		PageInfo   *durationsResponsePageInfo                  `json:"page_info,omitempty"`
	}

	durationsResponsePageInfo struct {
		Cursor  string `json:"cursor,omitempty"`
		Size    int    `json:"size,omitempty"`
		HasNext bool   `json:"has_next"`
	}

	// public response types

	TestSuiteDurationInfo struct {
		SourceFile string              `json:"source_file"`
		Duration   DurationPercentiles `json:"duration"`
	}

	DurationPercentiles struct {
		P50 string `json:"p50"`
		P90 string `json:"p90"`
	}
)

// TestSuiteDurationsClient defines the interface for fetching test suite durations
type TestSuiteDurationsClient interface {
	GetTestSuiteDurations(repositoryURL, service string) (map[string]map[string]TestSuiteDurationInfo, error)
}

// DurationsAPI abstracts the HTTP endpoint for testability (equivalent of CIVisibilityIntegrations)
type DurationsAPI interface {
	FetchTestSuiteDurations(repositoryURL, service, cursor string, pageSize int) (*durationsResponseAttributes, error)
}

// DatadogDurationsClient implements TestSuiteDurationsClient (equivalent of DatadogClient)
type DatadogDurationsClient struct {
	api DurationsAPI
}

func NewDurationsClient() *DatadogDurationsClient {
	return &DatadogDurationsClient{
		api: NewDatadogDurationsAPI(),
	}
}

func NewDurationsClientWithDependencies(api DurationsAPI) *DatadogDurationsClient {
	return &DatadogDurationsClient{
		api: api,
	}
}

func (c *DatadogDurationsClient) GetTestSuiteDurations(repositoryURL, service string) (map[string]map[string]TestSuiteDurationInfo, error) {
	startTime := time.Now()
	allSuites := make(map[string]map[string]TestSuiteDurationInfo)

	slog.Debug("Fetching test suite durations...")

	cursor := ""
	for {
		data, err := c.api.FetchTestSuiteDurations(repositoryURL, service, cursor, defaultDurationsPageSize)
		if err != nil {
			return nil, fmt.Errorf("fetching test suite durations: %w", err)
		}

		for module, suites := range data.TestSuites {
			if _, ok := allSuites[module]; !ok {
				allSuites[module] = make(map[string]TestSuiteDurationInfo)
			}
			for suite, info := range suites {
				allSuites[module][suite] = info
			}
		}

		if data.PageInfo == nil || !data.PageInfo.HasNext {
			break
		}
		cursor = data.PageInfo.Cursor
	}

	duration := time.Since(startTime)
	totalSuites := 0
	for _, suites := range allSuites {
		totalSuites += len(suites)
	}
	slog.Debug("Finished fetching test suite durations", "modules", len(allSuites), "suites", totalSuites, "duration", duration)

	return allSuites, nil
}

// DatadogDurationsAPI implements DurationsAPI using real HTTP calls (equivalent of DatadogCIVisibilityIntegrations)
type DatadogDurationsAPI struct {
	baseURL    string
	headers    map[string]string
	httpClient *http.Client
	err        error
}

func NewDatadogDurationsAPI() *DatadogDurationsAPI {
	headers := map[string]string{}
	var baseURL string
	httpClient := &http.Client{
		Timeout: 45 * time.Second,
	}

	agentlessEnabled := civisibility.BoolEnv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, false)
	if agentlessEnabled {
		apiKey := os.Getenv(constants.APIKeyEnvironmentVariable)
		if apiKey == "" {
			slog.Error("An API key is required for agentless mode. Use the DD_API_KEY env variable to set it")
			return &DatadogDurationsAPI{
				headers:    headers,
				httpClient: httpClient,
				err:        fmt.Errorf("DD_API_KEY is required when DD_CIVISIBILITY_AGENTLESS_ENABLED is true"),
			}
		}
		headers["dd-api-key"] = apiKey

		agentlessURL := os.Getenv(constants.CIVisibilityAgentlessURLEnvironmentVariable)
		if agentlessURL == "" {
			site := "datadoghq.com"
			if v := os.Getenv("DD_SITE"); v != "" {
				site = v
			}
			baseURL = fmt.Sprintf("https://api.%s", site)
		} else {
			baseURL = agentlessURL
		}
	} else {
		headers["X-Datadog-EVP-Subdomain"] = "api"
		agentURL := civisibility.AgentURLFromEnv()
		if agentURL.Scheme == "unix" {
			httpClient = httptransport.UnixSocketClient(agentURL.Path, 45*time.Second)
			agentURL = httptransport.UnixSocketURL(agentURL.Path)
		}
		baseURL = agentURL.String()
	}

	id := fmt.Sprint(rand.Uint64() & math.MaxInt64)
	headers["trace_id"] = id
	headers["parent_id"] = id

	slog.Debug("DurationsAPI: client created",
		"agentless", agentlessEnabled, "url", baseURL)

	return &DatadogDurationsAPI{
		baseURL:    baseURL,
		headers:    headers,
		httpClient: httpClient,
	}
}

func (c *DatadogDurationsAPI) FetchTestSuiteDurations(repositoryURL, service, cursor string, pageSize int) (*durationsResponseAttributes, error) {
	if c.err != nil {
		return nil, c.err
	}
	if repositoryURL == "" {
		return nil, fmt.Errorf("repository URL is required")
	}

	var pageInfo *durationsRequestPageInfo
	if pageSize > 0 || cursor != "" {
		pageInfo = &durationsRequestPageInfo{
			PageSize:  pageSize,
			PageState: cursor,
		}
	}

	body := durationsRequest{
		Data: durationsRequestData{
			Type: durationsRequestType,
			Attributes: durationsRequestAttributes{
				RepositoryURL: repositoryURL,
				Service:       service,
				PageInfo:      pageInfo,
			},
		},
	}

	requestURL := c.getURLPath(durationsURLPath)

	var lastErr error
	for attempt := range maxDurationsRetries {
		result, err := c.doPost(requestURL, body)
		if err == nil {
			return result, nil
		}
		lastErr = err
		slog.Debug("DurationsAPI: request failed, retrying", "attempt", attempt+1, "error", err)
		time.Sleep(time.Duration(100*(1<<uint(attempt))) * time.Millisecond)
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (c *DatadogDurationsAPI) getURLPath(urlPath string) string {
	if _, ok := c.headers["dd-api-key"]; ok {
		return fmt.Sprintf("%s/%s", c.baseURL, urlPath)
	}
	return fmt.Sprintf("%s/%s/%s", c.baseURL, "evp_proxy/v2", urlPath)
}

func (c *DatadogDurationsAPI) doPost(requestURL string, body interface{}) (*durationsResponseAttributes, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshalling request body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, requestURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, truncateBody(respBody))
	}

	slog.Debug("test_suite_durations", "responseBody", string(respBody))

	var responseObject durationsResponse
	if err := json.Unmarshal(respBody, &responseObject); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	return &responseObject.Data.Attributes, nil
}

func truncateBody(body []byte) string {
	s := string(body)
	if len(s) > 256 {
		return s[:256] + "..."
	}
	return s
}
