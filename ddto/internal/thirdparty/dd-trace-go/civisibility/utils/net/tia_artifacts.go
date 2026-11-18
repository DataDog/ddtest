package net

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/log"
)

// ddto's local TIA shares its maps through the TIA artifacts API: a lookup
// returns the map stored for the nearest of a list of commits, and an upload
// stores a map for one commit.
const (
	tiaArtifactsURLPath     = "api/unstable/ci/tia/artifacts"
	tiaArtifactLookupType   = "ci_app_tia_artifact_lookup"
	tiaMatchedCommitHeader  = "x-dd-tia-git-commit-sha"
	tiaSourceCommitHeader   = "x-dd-tia-source-commit"
	maxTIAArtifactLookupSHA = 100
	// tiaArtifactContentType is an upload's content type, as dd-trace-py's;
	// its body is the map's JSON as it is.
	tiaArtifactContentType = ContentTypeOctetStream
	// MaxTIAArtifactBytes is the largest body that the API takes.
	MaxTIAArtifactBytes = 5 << 20
)

var gitSHA = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)

// TIAArtifactKey identifies the maps that a lookup or an upload is about,
// with the commit that each takes.
type TIAArtifactKey struct {
	RepositoryID         string `json:"repository_id"`
	SuiteID              string `json:"suite_id"`
	EnvHash              string `json:"env_hash"`
	Language             string `json:"language"`
	CompatibilityVersion int    `json:"compatibility_version"`
}

// TIAArtifact is a stored map: its JSON, the requested commit that it
// matched, and the commit whose run produced it.
type TIAArtifact struct {
	Content       []byte
	MatchedCommit string
	SourceCommit  string
}

// TIAArtifactsClient looks maps up and stores them.
type TIAArtifactsClient interface {
	// RepositoryID is the repository URL that Test Optimization reports,
	// sanitized, which keys the maps.
	RepositoryID() string
	// LookupTIAArtifact returns the map for the nearest of commits, which
	// are nearest first, or nil when none has one.
	LookupTIAArtifact(ctx context.Context, key TIAArtifactKey, commits []string) (*TIAArtifact, error)
	// StoreTIAArtifact stores a map's JSON, content, as the map of commit,
	// sent as it is, as dd-trace-py sends it.
	StoreTIAArtifact(ctx context.Context, key TIAArtifactKey, commit string, content []byte) error
}

// NewTIAArtifactsClient returns a client of the TIA artifacts API, agentless
// or through the Agent's EVP proxy as Test Optimization's other requests, or
// nil when agentless mode has no API key. It starts no telemetry.
func NewTIAArtifactsClient() TIAArtifactsClient {
	c, _ := newClientWithServiceNameAndSubdomain("", "api")
	if c == nil {
		return nil
	}
	return c.(*client)
}

// RepositoryID returns the repository URL of the client's CI tags.
func (c *client) RepositoryID() string { return c.repositoryURL }

// LookupTIAArtifact implements TIAArtifactsClient.
func (c *client) LookupTIAArtifact(ctx context.Context, key TIAArtifactKey, commits []string) (*TIAArtifact, error) {
	commits = commits[:min(len(commits), maxTIAArtifactLookupSHA)]
	if len(commits) == 0 || slices.ContainsFunc(commits, func(commit string) bool { return !gitSHA.MatchString(commit) }) {
		return nil, errors.New("civisibility.tia: lookup needs commit SHAs")
	}
	attributes := map[string]any{"git_commit_shas": commits}
	encoded, _ := json.Marshal(key)
	_ = json.Unmarshal(encoded, &attributes)
	body := map[string]any{"data": map[string]any{"id": c.id, "type": tiaArtifactLookupType, "attributes": attributes}}
	config := RequestConfig{Method: "POST", URL: c.getURLPath(tiaArtifactsURLPath + "/lookup"), Body: body, Format: FormatJSON}
	// The handler asks for, and decodes, a gzip-encoded response.
	response, err := c.tiaRequestWith(ctx, config, map[string]string{HeaderContentType: ContentTypeJSONAlternative, "Accept": ContentTypeJSON})
	if err != nil {
		return nil, err
	}
	log.Debug("civisibility.tia: artifact lookup returned HTTP %d [commits:%d bytes:%d]", response.StatusCode, len(commits), len(response.Body))
	switch {
	case response.StatusCode == 404:
		return nil, nil
	case response.StatusCode != 200:
		return nil, fmt.Errorf("civisibility.tia: artifact lookup returned HTTP %d", response.StatusCode)
	}
	artifact := &TIAArtifact{Content: response.Body, MatchedCommit: response.Header.Get(tiaMatchedCommitHeader), SourceCommit: response.Header.Get(tiaSourceCommitHeader)}
	if !slices.Contains(commits, artifact.MatchedCommit) || !gitSHA.MatchString(artifact.SourceCommit) || len(artifact.Content) == 0 {
		return nil, fmt.Errorf("civisibility.tia: artifact lookup returned matched commit %q and source commit %q, or no content", artifact.MatchedCommit, artifact.SourceCommit)
	}
	return artifact, nil
}

// StoreTIAArtifact implements TIAArtifactsClient.
func (c *client) StoreTIAArtifact(ctx context.Context, key TIAArtifactKey, commit string, content []byte) error {
	switch {
	case !gitSHA.MatchString(commit):
		return fmt.Errorf("civisibility.tia: %q is not a commit SHA", commit)
	case len(content) == 0 || len(content) > MaxTIAArtifactBytes:
		return fmt.Errorf("civisibility.tia: an artifact of %d bytes is not stored", len(content))
	}
	query := url.Values{}
	query.Set("repository_id", key.RepositoryID)
	query.Set("suite_id", key.SuiteID)
	query.Set("env_hash", key.EnvHash)
	query.Set("language", key.Language)
	query.Set("compatibility_version", strconv.Itoa(key.CompatibilityVersion))
	query.Set("git_commit_sha", commit)
	config := RequestConfig{Method: "PUT", URL: c.getURLPath(tiaArtifactsURLPath) + "?" + query.Encode(), Body: content}
	response, err := c.tiaRequestWith(ctx, config, map[string]string{HeaderContentType: tiaArtifactContentType})
	if err != nil {
		return err
	}
	log.Debug("civisibility.tia: artifact upload returned HTTP %d [bytes:%d]", response.StatusCode, len(content))
	if response.StatusCode != 201 {
		return fmt.Errorf("civisibility.tia: artifact upload returned HTTP %d", response.StatusCode)
	}
	return nil
}

// tiaRequestWith sends config with the client's headers and headers, with
// ctx and at most one retry.
func (c *client) tiaRequestWith(ctx context.Context, config RequestConfig, headers map[string]string) (*Response, error) {
	config.Headers = maps.Clone(c.headers)
	maps.Copy(config.Headers, headers)
	config.MaxRetries = 1
	handler := &RequestHandler{Client: c.handler.Client, Context: ctx}
	response, err := handler.SendRequest(config)
	if err == nil && response == nil {
		err = errors.New("civisibility.tia: no response")
	}
	return response, err
}
