// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package api

import (
	"encoding/json"
	"fmt"
)

const (
	knownTestsRequestType string = "ci_app_libraries_tests_request"
	knownTestsURLPath     string = "api/v2/ci/libraries/tests"
)

type (
	knownTestsRequest struct {
		Data knownTestsRequestHeader `json:"data"`
	}

	knownTestsRequestHeader struct {
		ID         string                `json:"id"`
		Type       string                `json:"type"`
		Attributes KnownTestsRequestData `json:"attributes"`
	}

	KnownTestsRequestData struct {
		Service        string                     `json:"service"`
		Env            string                     `json:"env"`
		RepositoryURL  string                     `json:"repository_url"`
		Configurations testConfigurations         `json:"configurations"`
		PageInfo       *knownTestsRequestPageInfo `json:"page_info,omitempty"`
	}

	knownTestsRequestPageInfo struct {
		PageState string `json:"page_state,omitempty"`
	}

	knownTestsResponse struct {
		Data struct {
			ID         string                 `json:"id"`
			Type       string                 `json:"type"`
			Attributes KnownTestsResponseData `json:"attributes"`
		} `json:"data"`
	}

	KnownTestsResponseData struct {
		Tests    KnownTestsResponseDataModules `json:"tests"`
		PageInfo *knownTestsResponsePageInfo   `json:"page_info,omitempty"`
	}

	knownTestsResponsePageInfo struct {
		Cursor  string `json:"cursor,omitempty"`
		Size    int    `json:"size,omitempty"`
		HasNext bool   `json:"has_next"`
	}

	KnownTestsResponseDataModules map[string]KnownTestsResponseDataSuites
	KnownTestsResponseDataSuites  map[string][]string
)

func (c *transport) GetKnownTests() (*KnownTestsResponseData, error) {
	if c.repositoryURL == "" || c.commitSha == "" {
		return nil, fmt.Errorf("testoptimization.GetKnownTests: repository URL and commit SHA are required")
	}
	c.knownTestsRawResponse = nil

	allKnownTests := KnownTestsResponseData{
		Tests: KnownTestsResponseDataModules{},
	}
	var allResponse knownTestsResponse
	var firstRawResponse json.RawMessage

	cursor := ""
	pageCount := 0
	for {
		body := knownTestsRequest{
			Data: knownTestsRequestHeader{
				ID:   c.id,
				Type: knownTestsRequestType,
				Attributes: KnownTestsRequestData{
					Service:        c.serviceName,
					Env:            c.environment,
					RepositoryURL:  c.repositoryURL,
					Configurations: c.testConfigurations,
				},
			},
		}
		if cursor != "" {
			body.Data.Attributes.PageInfo = &knownTestsRequestPageInfo{PageState: cursor}
		}

		request := c.getPostRequestConfig(knownTestsURLPath, body)
		response, err := c.handler.SendRequest(*request)

		if err != nil {
			return nil, fmt.Errorf("sending known tests request: %s", err)
		}
		pageCount++
		if pageCount == 1 {
			firstRawResponse = cloneRawMessage(response.Body)
		}

		var responseObject knownTestsResponse
		err = response.Unmarshal(&responseObject)
		if err != nil {
			return nil, fmt.Errorf("unmarshalling known tests response: %s", err)
		}

		if pageCount == 1 {
			allResponse.Data.ID = responseObject.Data.ID
			allResponse.Data.Type = responseObject.Data.Type
		}

		mergeKnownTests(allKnownTests.Tests, responseObject.Data.Attributes.Tests)
		allKnownTests.PageInfo = responseObject.Data.Attributes.PageInfo

		if allKnownTests.PageInfo == nil || !allKnownTests.PageInfo.HasNext {
			break
		}
		if allKnownTests.PageInfo.Cursor == "" {
			return nil, fmt.Errorf("known tests response page_info is missing cursor")
		}
		cursor = allKnownTests.PageInfo.Cursor
	}

	if pageCount == 1 {
		c.knownTestsRawResponse = firstRawResponse
	} else {
		allResponse.Data.Attributes = allKnownTests
		rawResponse, err := json.Marshal(allResponse)
		if err != nil {
			return nil, fmt.Errorf("marshalling known tests response: %s", err)
		}
		c.knownTestsRawResponse = cloneRawMessage(rawResponse)
	}

	return &allKnownTests, nil
}

func mergeKnownTests(dst, src KnownTestsResponseDataModules) {
	for module, suites := range src {
		if _, ok := dst[module]; !ok {
			dst[module] = KnownTestsResponseDataSuites{}
		}
		for suite, tests := range suites {
			dst[module][suite] = append(dst[module][suite], tests...)
		}
	}
}
