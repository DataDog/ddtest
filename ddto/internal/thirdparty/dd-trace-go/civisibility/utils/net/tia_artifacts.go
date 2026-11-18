package net

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

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
	body, err := json.Marshal(map[string]any{"data": map[string]any{"id": c.id, "type": tiaArtifactLookupType, "attributes": attributes}})
	if err != nil {
		return nil, err
	}
	response, err := c.tiaRequest(ctx, "POST", c.getURLPath(tiaArtifactsURLPath+"/lookup"), body, map[string]string{HeaderContentType: ContentTypeJSONAlternative, "Accept": ContentTypeJSON})
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
	response, err := c.tiaRequest(ctx, "PUT", c.getURLPath(tiaArtifactsURLPath)+"?"+query.Encode(), content, map[string]string{HeaderContentType: tiaArtifactContentType})
	if err != nil {
		return err
	}
	log.Debug("civisibility.tia: artifact upload returned HTTP %d [bytes:%d]", response.StatusCode, len(content))
	if response.StatusCode != 201 {
		return fmt.Errorf("civisibility.tia: artifact upload returned HTTP %d", response.StatusCode)
	}
	return nil
}

// tiaResponse is a TIA artifacts response: its status, headers and body.
type tiaResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// errTIAResponseTooLarge is a response body over MaxTIAArtifactBytes, before
// or after HTTP's gzip encoding.
var errTIAResponseTooLarge = fmt.Errorf("civisibility.tia: a response over %d bytes", MaxTIAArtifactBytes)

// tiaRequest sends a request with the client's headers and headers, with ctx
// and at most one retry, after a transport error, also while it reads the
// body, a 429 or a 5xx. It reads at
// most MaxTIAArtifactBytes of the response, which it decodes when it is
// gzip-encoded, also at most MaxTIAArtifactBytes: the shared request handler
// reads whole bodies, which a lookup must not.
func (c *client) tiaRequest(ctx context.Context, method, url string, body []byte, headers map[string]string) (*tiaResponse, error) {
	var last error
	for attempt := range 2 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(DefaultBackoff):
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, redactURLError(err)
		}
		for key, value := range c.headers {
			request.Header.Set(key, value)
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		request.Header.Set(HeaderAcceptEncoding, ContentEncodingGzip)
		response, err := c.handler.Client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = redactURLError(err)
			continue
		}
		data, err := readTIABody(response)
		_ = response.Body.Close()
		switch {
		case errors.Is(err, errTIAResponseTooLarge):
			return nil, err
		case err != nil:
			// A body that breaks off is retried as a failed request is.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = err
			continue
		}
		if response.StatusCode == HTTPStatusTooManyRequests || response.StatusCode >= 500 {
			last = fmt.Errorf("civisibility.tia: HTTP %d", response.StatusCode)
			continue
		}
		return &tiaResponse{Body: data, StatusCode: response.StatusCode, Header: response.Header}, nil
	}
	return nil, last
}

// readTIABody reads at most MaxTIAArtifactBytes of a response, decoded when
// it is gzip-encoded.
func readTIABody(response *http.Response) ([]byte, error) {
	reader := io.LimitReader(response.Body, MaxTIAArtifactBytes+1)
	if response.Header.Get(HeaderContentEncoding) == ContentEncodingGzip {
		decoded, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		reader = io.LimitReader(decoded, MaxTIAArtifactBytes+1)
	}
	data, err := io.ReadAll(reader)
	switch {
	case err != nil:
		return nil, err
	case len(data) > MaxTIAArtifactBytes:
		return nil, errTIAResponseTooLarge
	}
	return data, nil
}
