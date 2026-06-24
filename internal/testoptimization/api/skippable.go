// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package api

import (
	"fmt"
	"log/slog"

	"github.com/DataDog/ddtest/internal/settings"
)

const (
	skippableRequestType string = "test_params"
	skippableURLPath     string = "api/v2/ci/tests/skippable"
)

type (
	skippableRequest struct {
		Data skippableRequestHeader `json:"data"`
	}

	skippableRequestHeader struct {
		Type       string               `json:"type"`
		Attributes skippableRequestData `json:"attributes"`
	}

	skippableRequestData struct {
		TestLevel      settings.TestSkippingLevel `json:"test_level"`
		Configurations testConfigurations         `json:"configurations"`
		Service        string                     `json:"service"`
		Env            string                     `json:"env"`
		RepositoryURL  string                     `json:"repository_url"`
		Sha            string                     `json:"sha"`
	}

	skippableResponse struct {
		Meta skippableResponseMeta   `json:"meta"`
		Data []skippableResponseData `json:"data"`
	}

	skippableResponseMeta struct {
		CorrelationID string `json:"correlation_id"`
	}

	skippableResponseData struct {
		ID         string                          `json:"id"`
		Type       string                          `json:"type"`
		Attributes SkippableResponseDataAttributes `json:"attributes"`
	}

	SkippableResponseDataAttributes struct {
		Suite          string             `json:"suite"`
		Name           string             `json:"name"`
		Parameters     string             `json:"parameters"`
		Configurations testConfigurations `json:"configurations"`
	}

	SkippableTests map[string]bool

	SkippableSuite struct {
		Module string
		Suite  string
	}

	SkippableSuites map[SkippableSuite]bool

	Skippables struct {
		Tests  SkippableTests
		Suites SkippableSuites
	}
)

func NewSkippables() Skippables {
	return Skippables{
		Tests:  SkippableTests{},
		Suites: SkippableSuites{},
	}
}

func (s Skippables) Count() int {
	return len(s.Tests) + len(s.Suites)
}

func (c *transport) GetSkippableTests() (correlationID string, skippables Skippables, err error) {
	if c.repositoryURL == "" || c.commitSha == "" {
		err = fmt.Errorf("testoptimization.GetSkippableTests: repository URL and commit SHA are required")
		return
	}
	c.skippableTestsRawResponse = nil

	body := skippableRequest{
		Data: skippableRequestHeader{
			Type: skippableRequestType,
			Attributes: skippableRequestData{
				TestLevel:      c.getTestSkippingLevel(),
				Configurations: c.testConfigurations,
				Service:        c.serviceName,
				Env:            c.environment,
				RepositoryURL:  c.repositoryURL,
				Sha:            c.commitSha,
			},
		},
	}

	request := c.getPostRequestConfig(skippableURLPath, body)
	response, err := c.handler.SendRequest(*request)

	if err != nil {
		return "", NewSkippables(), fmt.Errorf("sending skippable tests request: %s", err)
	}
	c.skippableTestsRawResponse = cloneRawMessage(response.Body)

	var responseObject skippableResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return "", NewSkippables(), fmt.Errorf("unmarshalling skippable tests response: %s", err)
	}

	skippables = NewSkippables()
	warnedMissingTestBundle := false
	for _, data := range responseObject.Data {

		// Filter out the tests that do not match the test configurations
		if data.Attributes.Configurations.OsPlatform != "" && c.testConfigurations.OsPlatform != "" &&
			data.Attributes.Configurations.OsPlatform != c.testConfigurations.OsPlatform {
			continue
		}
		if data.Attributes.Configurations.OsArchitecture != "" && c.testConfigurations.OsArchitecture != "" &&
			data.Attributes.Configurations.OsArchitecture != c.testConfigurations.OsArchitecture {
			continue
		}
		if data.Attributes.Configurations.OsVersion != "" && c.testConfigurations.OsVersion != "" &&
			data.Attributes.Configurations.OsVersion != c.testConfigurations.OsVersion {
			continue
		}
		if data.Attributes.Configurations.RuntimeName != "" && c.testConfigurations.RuntimeName != "" &&
			data.Attributes.Configurations.RuntimeName != c.testConfigurations.RuntimeName {
			continue
		}
		if data.Attributes.Configurations.RuntimeArchitecture != "" && c.testConfigurations.RuntimeArchitecture != "" &&
			data.Attributes.Configurations.RuntimeArchitecture != c.testConfigurations.RuntimeArchitecture {
			continue
		}
		if data.Attributes.Configurations.RuntimeVersion != "" && c.testConfigurations.RuntimeVersion != "" &&
			data.Attributes.Configurations.RuntimeVersion != c.testConfigurations.RuntimeVersion {
			continue
		}

		if data.Attributes.Configurations.TestBundle == "" && !warnedMissingTestBundle {
			slog.Warn("Datadog backend did not return test.bundle for skippable test or suite; please contact Datadog support")
			warnedMissingTestBundle = true
		}

		switch data.Type {
		case string(settings.TestSkippingLevelTest):
			skippables.Tests[skippableTestKey(data.Attributes)] = true
		case string(settings.TestSkippingLevelSuite):
			if data.Attributes.Suite != "" {
				skippables.Suites[skippableSuiteKey(data.Attributes)] = true
			}
		}
	}

	return responseObject.Meta.CorrelationID, skippables, nil
}

func skippableTestKey(test SkippableResponseDataAttributes) string {
	return test.Configurations.TestBundle + "." + test.Suite + "." + test.Name + "." + test.Parameters
}

func skippableSuiteKey(test SkippableResponseDataAttributes) SkippableSuite {
	return SkippableSuite{
		Module: test.Configurations.TestBundle,
		Suite:  test.Suite,
	}
}
