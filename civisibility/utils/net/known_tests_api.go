// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
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
		Service        string             `json:"service"`
		Env            string             `json:"env"`
		RepositoryURL  string             `json:"repository_url"`
		Configurations testConfigurations `json:"configurations"`
	}

	knownTestsResponse struct {
		Data struct {
			ID         string                 `json:"id"`
			Type       string                 `json:"type"`
			Attributes KnownTestsResponseData `json:"attributes"`
		} `json:"data"`
	}

	KnownTestsResponseData struct {
		Tests KnownTestsResponseDataModules `json:"tests"`
	}

	KnownTestsResponseDataModules map[string]KnownTestsResponseDataSuites
	KnownTestsResponseDataSuites  map[string][]string
)

func (c *client) GetKnownTests() (*KnownTestsResponseData, error) {
	if c.repositoryURL == "" || c.commitSha == "" {
		return nil, fmt.Errorf("civisibility.GetKnownTests: repository URL and commit SHA are required")
	}

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

	request := c.getPostRequestConfig(knownTestsURLPath, body)

	response, err := c.handler.SendRequest(*request)

	if err != nil {
		return nil, fmt.Errorf("sending known tests request: %s", err)
	}

	var responseObject knownTestsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling known tests response: %s", err)
	}

	return &responseObject.Data.Attributes, nil
}
