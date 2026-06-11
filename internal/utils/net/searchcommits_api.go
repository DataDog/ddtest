// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
)

const (
	searchCommitsType    string = "commit"
	searchCommitsURLPath string = "api/v2/git/repository/search_commits"
)

type (
	searchCommits struct {
		Data []searchCommitsData `json:"data"`
		Meta searchCommitsMeta   `json:"meta"`
	}
	searchCommitsData struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	searchCommitsMeta struct {
		RepositoryURL string `json:"repository_url"`
	}
)

func (c *client) GetCommits(localCommits []string) ([]string, error) {
	if c.repositoryURL == "" {
		return nil, fmt.Errorf("civisibility.GetCommits: repository URL is required")
	}

	body := searchCommits{
		Data: []searchCommitsData{},
		Meta: searchCommitsMeta{
			RepositoryURL: c.repositoryURL,
		},
	}

	for _, localCommit := range localCommits {
		body.Data = append(body.Data, searchCommitsData{
			ID:   localCommit,
			Type: searchCommitsType,
		})
	}

	request := c.getPostRequestConfig(searchCommitsURLPath, body)
	response, err := c.handler.SendRequest(*request)
	if err != nil {
		return nil, fmt.Errorf("sending search commits request: %s", err.Error())
	}

	var responseObject searchCommits
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling search commits response: %s", err.Error())
	}

	var commits []string
	for _, commit := range responseObject.Data {
		commits = append(commits, commit.ID)
	}
	return commits, nil
}
