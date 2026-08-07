// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package api

import (
	"fmt"
	"time"

	"github.com/DataDog/ddtest/internal/telemetry"
)

const (
	testManagementTestsRequestType string = "ci_app_libraries_tests_request"
	testManagementTestsURLPath     string = "api/v2/test/libraries/test-management/tests"
)

type (
	testManagementTestsRequest struct {
		Data testManagementTestsRequestHeader `json:"data"`
	}

	testManagementTestsRequestHeader struct {
		ID         string                         `json:"id"`
		Type       string                         `json:"type"`
		Attributes testManagementTestsRequestData `json:"attributes"`
	}

	testManagementTestsRequestData struct {
		RepositoryURL string `json:"repository_url"`
		CommitSha     string `json:"sha"`
		Module        string `json:"module,omitempty"`
		CommitMessage string `json:"commit_message"`
		Branch        string `json:"branch"`
	}

	testManagementTestsResponse struct {
		Data struct {
			ID         string                                 `json:"id"`
			Type       string                                 `json:"type"`
			Attributes TestManagementTestsResponseDataModules `json:"attributes"`
		} `json:"data"`
	}

	TestManagementTestsResponseDataModules struct {
		Modules map[string]TestManagementTestsResponseDataSuites `json:"modules"`
	}

	TestManagementTestsResponseDataSuites struct {
		Suites map[string]TestManagementTestsResponseDataTests `json:"suites"`
	}

	TestManagementTestsResponseDataTests struct {
		Tests map[string]TestManagementTestsResponseDataTestProperties `json:"tests"`
	}

	TestManagementTestsResponseDataTestProperties struct {
		Properties TestManagementTestsResponseDataTestPropertiesAttributes `json:"properties"`
	}

	TestManagementTestsResponseDataTestPropertiesAttributes struct {
		Quarantined  bool `json:"quarantined"`
		Disabled     bool `json:"disabled"`
		AttemptToFix bool `json:"attempt_to_fix"`
	}
)

func (c *transport) GetTestManagementTests() (*TestManagementTestsResponseDataModules, error) {
	startTime := time.Now()
	defer func() {
		c.backendRequestTimings.TestManagementTests = time.Since(startTime)
	}()

	if c.repositoryURL == "" {
		return nil, fmt.Errorf("testoptimization.GetTestManagementTests: repository URL is required")
	}
	c.testManagementTestsRawResponse = nil

	// we use the head commit SHA if it is set, otherwise we use the commit SHA
	commitSha := c.commitSha
	if c.headCommitSha != "" {
		commitSha = c.headCommitSha
	}

	// we use the head commit message if it is set, otherwise we use the commit message
	commitMessage := c.commitMessage
	if c.headCommitMessage != "" {
		commitMessage = c.headCommitMessage
	}

	body := testManagementTestsRequest{
		Data: testManagementTestsRequestHeader{
			ID:   c.id,
			Type: testManagementTestsRequestType,
			Attributes: testManagementTestsRequestData{
				RepositoryURL: c.repositoryURL,
				CommitSha:     commitSha,
				CommitMessage: commitMessage,
				Branch:        c.branchName,
			},
		},
	}

	request := c.getPostRequestConfig(testManagementTestsURLPath, body)
	telemetry.TestManagementTestsRequest(c.telemetryClient, request.Compressed)
	requestStartTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	telemetry.TestManagementTestsRequestMs(c.telemetryClient, time.Since(requestStartTime))

	if err != nil {
		telemetry.TestManagementTestsRequestErrors(c.telemetryClient, responseStatusCode(response))
		return nil, fmt.Errorf("sending test management tests request: %s", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.TestManagementTestsRequestErrors(c.telemetryClient, response.StatusCode)
	}
	telemetry.TestManagementTestsResponseBytes(c.telemetryClient, response.Compressed, response.BodySize)
	c.testManagementTestsRawResponse = cloneRawMessage(response.Body)

	var responseObject testManagementTestsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling test management tests response: %s", err)
	}
	responseData := &responseObject.Data.Attributes
	telemetry.TestManagementTestsResponseTests(c.telemetryClient, testManagementResponseTestCount(responseData))

	return responseData, nil
}

func testManagementResponseTestCount(response *TestManagementTestsResponseDataModules) int {
	if response == nil {
		return 0
	}
	testCount := 0
	for _, module := range response.Modules {
		for _, suite := range module.Suites {
			testCount += len(suite.Tests)
		}
	}
	return testCount
}
