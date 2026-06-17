package api

import (
	"fmt"
	"log/slog"
	"time"
)

const (
	durationsRequestType string = "ci_app_ddtest_test_suite_durations_request"
	durationsURLPath     string = "api/v2/ci/ddtest/test_suite_durations"

	defaultDurationsPageSize int = 500
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

	TestSuiteDurationsResponseData struct {
		TestSuites map[string]map[string]TestSuiteDurationInfo `json:"test_suites"`
	}

	TestSuiteDurationInfo struct {
		SourceFile string              `json:"source_file"`
		Duration   DurationPercentiles `json:"duration"`
	}

	DurationPercentiles struct {
		P50 string `json:"p50"`
		P90 string `json:"p90"`
	}
)

func (c *transport) GetTestSuiteDurations() *TestSuiteDurationsResponseData {
	startTime := time.Now()
	if c.repositoryURL == "" {
		slog.Error("Test durations API errored", "duration", time.Since(startTime), "error", "repository URL is required")
		return emptyTestSuiteDurationsResponseData()
	}

	durations, err := c.fetchTestSuiteDurations(c.repositoryURL, c.serviceName)
	if err != nil {
		slog.Error("Test durations API errored",
			"service", c.serviceName,
			"repositoryURL", c.repositoryURL,
			"duration", time.Since(startTime),
			"error", err)
		return emptyTestSuiteDurationsResponseData()
	}

	totalSuites := countTestSuiteDurations(durations)
	if totalSuites == 0 {
		slog.Warn("Test durations API returned no test suites",
			"service", c.serviceName,
			"repositoryURL", c.repositoryURL,
			"modulesCount", len(durations),
			"testSuitesCount", totalSuites,
			"duration", time.Since(startTime))
		return emptyTestSuiteDurationsResponseData()
	}

	slog.Info("Fetched test suite durations",
		"service", c.serviceName,
		"repositoryURL", c.repositoryURL,
		"modulesCount", len(durations),
		"testSuitesCount", totalSuites,
		"duration", time.Since(startTime))
	return &TestSuiteDurationsResponseData{TestSuites: durations}
}

func emptyTestSuiteDurationsResponseData() *TestSuiteDurationsResponseData {
	return &TestSuiteDurationsResponseData{
		TestSuites: map[string]map[string]TestSuiteDurationInfo{},
	}
}

func (c *transport) fetchTestSuiteDurations(repositoryURL, service string) (map[string]map[string]TestSuiteDurationInfo, error) {
	startTime := time.Now()
	allSuites := make(map[string]map[string]TestSuiteDurationInfo)

	slog.Debug("Fetching test suite durations...")

	cursor := ""
	for {
		data, err := c.fetchTestSuiteDurationsPage(repositoryURL, service, cursor, defaultDurationsPageSize)
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

func (c *transport) fetchTestSuiteDurationsPage(repositoryURL, service, cursor string, pageSize int) (*durationsResponseAttributes, error) {
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

	request := c.getPostRequestConfig(durationsURLPath, body)
	response, err := c.handler.SendRequest(*request)
	if err != nil {
		return nil, fmt.Errorf("sending test suite durations request: %s", err)
	}

	slog.Debug("test_suite_durations", "responseBody", string(response.Body))

	var responseObject durationsResponse
	if err := response.Unmarshal(&responseObject); err != nil {
		return nil, fmt.Errorf("unmarshalling test suite durations response: %s", err)
	}

	return &responseObject.Data.Attributes, nil
}

func countTestSuiteDurations(testSuiteDurations map[string]map[string]TestSuiteDurationInfo) int {
	totalSuites := 0
	for _, suites := range testSuiteDurations {
		totalSuites += len(suites)
	}
	return totalSuites
}
