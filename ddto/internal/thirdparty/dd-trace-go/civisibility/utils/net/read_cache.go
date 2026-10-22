// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/locking"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

const (
	// readCacheSchema identifies the on-disk cache entry format.
	readCacheSchema = "civisibility-read-cache-v1"

	// readCacheEndpointVersion is bumped when endpoint request semantics change.
	readCacheEndpointVersion = 1
	// readCacheEndpointSkippableTestsVersion is bumped independently because skippable-tests
	// cache entries include backend coverage metadata and safety state.
	readCacheEndpointSkippableTestsVersion = 3

	// readCacheScopeLocal scopes unidentified local runs by parent process.
	readCacheScopeLocal = "local"
	// readCacheScopeCIWeak scopes CI runs by provider plus pipeline identity.
	readCacheScopeCIWeak = "ci_weak"
	// readCacheScopeCIMedium scopes CI runs by provider, pipeline, and job-like names.
	readCacheScopeCIMedium = "ci_medium"
	// readCacheScopeCIStrong scopes CI runs by provider and stable job id.
	readCacheScopeCIStrong = "ci_strong"
	// readCacheScopeCLI scopes unidentified local runs by ddto's invocation,
	// whose Git worker and test processes have different parents.
	readCacheScopeCLI = "cli"
	// readCacheCLIDirectory holds, in the shared directory, one directory of
	// entries per ddto invocation.
	readCacheCLIDirectory = "cli"
	// readCacheCLICleanupMaxDirectories bounds the invocation directories that
	// one cleanup examines.
	readCacheCLICleanupMaxDirectories = 8

	// readCacheEndpointSettings names the settings cache endpoint.
	readCacheEndpointSettings = "settings"
	// readCacheEndpointKnownTests names the known-tests cache endpoint.
	readCacheEndpointKnownTests = "known_tests"
	// readCacheEndpointSkippableTests names the skippable-tests cache endpoint.
	readCacheEndpointSkippableTests = "skippable_tests"
	// readCacheEndpointTestManagementTests names the test-management cache endpoint.
	readCacheEndpointTestManagementTests = "test_management_tests"

	// readCacheWaiterInitialBackoff is the first waiter poll interval.
	readCacheWaiterInitialBackoff = 5 * time.Millisecond
	// readCacheWaiterMaxBackoff caps waiter polling backoff: a poll reads the
	// lock and tries the entry, so a short interval costs little and returns
	// the owner's response sooner.
	readCacheWaiterMaxBackoff = 20 * time.Millisecond
	// readCacheWaiterTimeout is how long waiters wait for another process to
	// fill the cache when they cannot tell whether that process still runs.
	readCacheWaiterTimeout = 2 * time.Second
	// readCacheWaiterLiveOwnerTimeout bounds the wait for an owner that still
	// runs, both from the start of the wait and from the lock's creation. It
	// exceeds what a paginated response takes, so a slow owner serves its
	// waiters instead of each one sending the request again; an owner slower
	// than this costs its waiters this wait and their own request.
	readCacheWaiterLiveOwnerTimeout = 2 * time.Minute
	// readCacheStaleLockTimeout is intentionally much longer than the HTTP request path.
	readCacheStaleLockTimeout = time.Hour
	// readCacheGlobalCleanupMaxEntries bounds opportunistic directory cleanup.
	readCacheGlobalCleanupMaxEntries = 32
	// readCacheGlobalCleanupMaxDuration bounds opportunistic directory cleanup.
	readCacheGlobalCleanupMaxDuration = 5 * time.Millisecond
)

type (
	// readCacheScope is the stable base scope shared by every cacheable read endpoint.
	readCacheScope struct {
		Schema              string             `json:"schema"`
		RepositoryURLHash   string             `json:"repo_url_hash"`
		CommitSHA           string             `json:"commit_sha"`
		Branch              string             `json:"branch"`
		Service             string             `json:"service"`
		Env                 string             `json:"env"`
		TestConfigurations  testConfigurations `json:"test_configurations"`
		Agentless           bool               `json:"agentless"`
		BaseURLHash         string             `json:"base_url_hash"`
		BaseURLSanitized    string             `json:"base_url_sanitized"`
		ScopeKind           string             `json:"scope_kind"`
		ScopeIdentityHash   string             `json:"scope_identity_hash"`
		ScopeIdentitySource []string           `json:"scope_identity_source"`
	}

	// readCacheScopeIdentity is selected once during client construction from already-resolved CI tags.
	readCacheScopeIdentity struct {
		Kind   string
		Source []string
		Hash   string
		TTL    time.Duration
		// directory names the cli scope's entries' directory, which only the
		// state directory identifies.
		directory string
	}

	// readCacheEndpointScope identifies the cacheable endpoint and semantic request payload.
	readCacheEndpointScope struct {
		Endpoint        string `json:"endpoint"`
		EndpointVersion int    `json:"endpoint_version"`
		RequestHash     string `json:"request_hash"`
	}

	// readCacheEntry stores one typed cached response plus validation metadata.
	readCacheEntry[T any] struct {
		CacheKey          string                 `json:"cache_key"`
		CreatedAtUnixNano int64                  `json:"created_at_unix_nano"`
		TTLSeconds        int64                  `json:"ttl_seconds"`
		BaseScope         readCacheScope         `json:"base_scope"`
		EndpointScope     readCacheEndpointScope `json:"endpoint_scope"`
		Response          T                      `json:"response"`
	}

	// readCacheLiveResult carries a live endpoint response and explicit cacheability.
	readCacheLiveResult[T any] struct {
		Value     T
		Cacheable bool
	}

	// readCacheIdentityValue is the canonical name/value form hashed for scope identity.
	readCacheIdentityValue struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	readCachePaths struct {
		Dir       string
		CacheFile string
		LockFile  string
		TmpGlob   string
	}

	readCacheLockEntry struct {
		PID               int    `json:"pid"`
		CreatedAtUnixNano int64  `json:"created_at_unix_nano"`
		CacheKey          string `json:"cache_key"`
		OwnerNonce        string `json:"owner_nonce"`
	}

	readCacheLockOwner struct {
		Path  string
		Entry readCacheLockEntry
	}

	readCacheLockAcquireStatus int

	readCacheHooks struct {
		cacheRoot string
		now       func() time.Time
		pid       func() int
		parentPID func() int
		sleep     func(time.Duration)
		// processAlive reports whether a lock owner's process runs, and
		// whether that is known.
		processAlive func(pid int) (alive bool, known bool)
	}
)

const (
	readCacheLockAcquired readCacheLockAcquireStatus = iota
	readCacheLockActive
	readCacheLockBypass
)

var (
	readCacheHooksMu locking.RWMutex
	readCacheState   readCacheHooks
)

// SetReadCacheHooksForTesting overrides read-cache process hooks for internal tests only.
func SetReadCacheHooksForTesting(cacheRoot string, now func() time.Time, pid func() int, parentPID func() int, sleep func(time.Duration)) {
	readCacheHooksMu.Lock()
	defer readCacheHooksMu.Unlock()

	readCacheState = readCacheHooks{
		cacheRoot: cacheRoot,
		now:       now,
		pid:       pid,
		parentPID: parentPID,
		sleep:     sleep,
		// Test PIDs name no real process: whether an owner runs is unknown.
		processAlive: func(int) (bool, bool) { return false, false },
	}
}

// setReadCacheProcessAliveForTesting overrides whether lock owners run, after
// SetReadCacheHooksForTesting.
func setReadCacheProcessAliveForTesting(alive func(pid int) (bool, bool)) {
	readCacheHooksMu.Lock()
	defer readCacheHooksMu.Unlock()

	readCacheState.processAlive = alive
}

// ResetReadCacheHooksForTesting restores production read-cache hooks after internal tests.
func ResetReadCacheHooksForTesting() {
	readCacheHooksMu.Lock()
	defer readCacheHooksMu.Unlock()

	readCacheState = readCacheHooks{}
}

func newReadCacheScopeIdentity(ciTags map[string]string) readCacheScopeIdentity {
	provider := readCacheTagValue(ciTags, constants.CIProviderName)
	pipelineName, pipelineValue := readCacheSelectedPipeline(ciTags)
	jobID := readCacheTagValue(ciTags, constants.CIJobID)
	jobName := readCacheTagValue(ciTags, constants.CIJobName)
	stageName := readCacheTagValue(ciTags, constants.CIStageName)
	nodeName := readCacheTagValue(ciTags, constants.CINodeName)

	if provider != "" && jobID != "" {
		values := []readCacheIdentityValue{{Name: constants.CIProviderName, Value: provider}}
		if pipelineValue != "" {
			values = append(values, readCacheIdentityValue{Name: pipelineName, Value: pipelineValue})
		}
		values = append(values, readCacheIdentityValue{Name: constants.CIJobID, Value: jobID})
		return newReadCacheScopeIdentityFromValues(readCacheScopeCIStrong, time.Hour, values)
	}

	if provider != "" && pipelineValue != "" && (jobName != "" || stageName != "" || nodeName != "") {
		values := []readCacheIdentityValue{
			{Name: constants.CIProviderName, Value: provider},
			{Name: pipelineName, Value: pipelineValue},
		}
		if jobName != "" {
			values = append(values, readCacheIdentityValue{Name: constants.CIJobName, Value: jobName})
		}
		if stageName != "" {
			values = append(values, readCacheIdentityValue{Name: constants.CIStageName, Value: stageName})
		}
		if nodeName != "" {
			values = append(values, readCacheIdentityValue{Name: constants.CINodeName, Value: nodeName})
		}
		return newReadCacheScopeIdentityFromValues(readCacheScopeCIMedium, 15*time.Minute, values)
	}

	if provider != "" && pipelineValue != "" {
		values := []readCacheIdentityValue{
			{Name: constants.CIProviderName, Value: provider},
			{Name: pipelineName, Value: pipelineValue},
		}
		return newReadCacheScopeIdentityFromValues(readCacheScopeCIWeak, 5*time.Minute, values)
	}

	values := []readCacheIdentityValue{{Name: "parent_pid", Value: strconv.Itoa(readCacheParentPID())}}
	return newReadCacheScopeIdentityFromValues(readCacheScopeLocal, time.Minute, values)
}

// cliReadCacheScopeIdentity returns, for a local run, the scope of ddto's
// invocation whose state is in dir: its Git worker requests ahead for the test
// processes, which have another parent. CI scopes already include both. The
// entries last as long as the invocation, whose build can take minutes: they
// live in its own directory, which ddto removes (RemoveCLIReadCache).
//
// The scope also holds a digest of the process's credential and its Agent's
// socket, whose path the base URL does not keep whole: a test process whose
// API key or Agent differs from the worker's, as one that TestMain changed,
// makes its own requests. The upstream scopes leave both out; here the
// others hold them too, as a CI job's scope, which another invocation with
// another API key, as for another organization, would share.
func cliReadCacheScopeIdentity(identity readCacheScopeIdentity, dir, credential, socket string) readCacheScopeIdentity {
	if identity.Kind != readCacheScopeLocal || dir == "" {
		identity.Source = append(identity.Source, "credential", "socket")
		identity.Hash = readCacheHashValue([]readCacheIdentityValue{
			{Name: "scope", Value: identity.Hash},
			{Name: "credential", Value: readCacheHashString(credential)},
			{Name: "socket", Value: socket},
		})
		return identity
	}
	values := []readCacheIdentityValue{
		{Name: "cli_state", Value: dir},
		{Name: "credential", Value: readCacheHashString(credential)},
		{Name: "socket", Value: socket},
	}
	scoped := newReadCacheScopeIdentityFromValues(readCacheScopeCLI, time.Hour, values)
	scoped.directory = cliReadCacheDirectoryName(dir)
	log.Debug("civisibility.read_cache: invocation scope [directory:%s ttl:%s]", filepath.Join(readCacheCLIDirectory, scoped.directory), scoped.TTL)
	return scoped
}

// cliReadCacheDirectoryName names the directory of the entries of ddto's
// invocation whose state is in dir.
func cliReadCacheDirectoryName(dir string) string {
	return readCacheHashString(dir)
}

// readCacheSubdirectory returns the directory, in the shared one, of the
// identity's entries: each ddto invocation keeps its own.
func readCacheSubdirectory(identity readCacheScopeIdentity) string {
	if identity.Kind != readCacheScopeCLI {
		return ""
	}
	return filepath.Join(readCacheCLIDirectory, identity.directory)
}

// RemoveCLIReadCache removes the read-cache entries of ddto's invocation
// whose state is in stateDir, once its Git worker and test processes exited.
// It also removes the expired entries of invocations that ended without
// removing theirs, and returns how many of their directories it removed.
func RemoveCLIReadCache(stateDir string) (int, error) {
	if stateDir == "" {
		return 0, nil
	}
	dir, err := readCacheDirectory(filepath.Join(readCacheCLIDirectory, cliReadCacheDirectoryName(stateDir)))
	if err != nil {
		return 0, err
	}
	err = os.RemoveAll(dir)
	return removeExpiredCLIReadCaches(filepath.Dir(dir)), err
}

// removeExpiredCLIReadCaches applies, to a bounded number of the invocation
// directories in root, the shared directory's cleanup, which removes only
// expired entries, stale locks and old temporary files, and then removes the
// directories that are empty. A kill or a reboot leaves an invocation's
// directory; its entries expire within the TTL, so it empties and goes, and
// hides the others only for that long. A running invocation's entries do not
// expire, and its process holds a lock file in the directory while it writes,
// so the directory cannot go under it. It returns how many it removed.
func removeExpiredCLIReadCaches(root string) int {
	dirFile, err := os.Open(root)
	if err != nil {
		return 0
	}
	entries, _ := dirFile.ReadDir(readCacheCLICleanupMaxDirectories)
	_ = dirFile.Close()
	start := readCacheNow()
	removed := 0
	for _, entry := range entries {
		if readCacheNow().Sub(start) > readCacheGlobalCleanupMaxDuration {
			return removed
		}
		if entry.IsDir() {
			dir := filepath.Join(root, entry.Name())
			readCacheGlobalCleanup(dir)
			if os.Remove(dir) == nil {
				removed++
			}
		}
	}
	return removed
}

func newReadCacheScopeIdentityFromValues(kind string, ttl time.Duration, values []readCacheIdentityValue) readCacheScopeIdentity {
	return readCacheScopeIdentity{
		Kind:   kind,
		Source: readCacheIdentitySources(values),
		Hash:   readCacheHashValue(values),
		TTL:    ttl,
	}
}

func readCacheIdentitySources(values []readCacheIdentityValue) []string {
	sources := make([]string, 0, len(values))
	for _, value := range values {
		sources = append(sources, value.Name)
	}
	return sources
}

func readCacheSelectedPipeline(ciTags map[string]string) (string, string) {
	if pipelineID := readCacheTagValue(ciTags, constants.CIPipelineID); pipelineID != "" {
		return constants.CIPipelineID, pipelineID
	}
	if pipelineNumber := readCacheTagValue(ciTags, constants.CIPipelineNumber); pipelineNumber != "" {
		return constants.CIPipelineNumber, pipelineNumber
	}
	return "", ""
}

func readCacheTagValue(ciTags map[string]string, name string) string {
	return strings.TrimSpace(ciTags[name])
}

func (c *client) readCacheBaseScope() readCacheScope {
	identity := c.readCacheScopeIdentity
	return readCacheScope{
		Schema:              readCacheSchema,
		RepositoryURLHash:   readCacheHashString(c.repositoryURL),
		CommitSHA:           c.commitSha,
		Branch:              c.branchName,
		Service:             c.serviceName,
		Env:                 c.environment,
		TestConfigurations:  c.testConfigurations,
		Agentless:           c.agentless,
		BaseURLHash:         readCacheHashString(c.baseURL),
		BaseURLSanitized:    readCacheSanitizeBaseURL(c.baseURL),
		ScopeKind:           identity.Kind,
		ScopeIdentityHash:   identity.Hash,
		ScopeIdentitySource: append([]string(nil), identity.Source...),
	}
}

func readThroughShortLivedCache[T any](
	c *client,
	endpoint string,
	semanticRequest any,
	live func() (readCacheLiveResult[T], error),
	onCacheHit func(T),
) (T, error) {
	var zero T

	baseScope := c.readCacheBaseScope()
	requestHash, err := readCacheHashJSON(semanticRequest)
	if err != nil {
		log.Debug("civisibility.read_cache: request hash failed [endpoint:%s error:%s]", endpoint, err.Error())
		return readCacheLiveValue(live)
	}
	endpointScope := readCacheEndpointScope{
		Endpoint:        endpoint,
		EndpointVersion: readCacheEndpointVersionFor(endpoint),
		RequestHash:     requestHash,
	}
	cacheKey, err := readCacheKey(baseScope, endpointScope)
	if err != nil {
		log.Debug("civisibility.read_cache: key build failed [endpoint:%s error:%s]", endpoint, err.Error())
		return readCacheLiveValue(live)
	}

	paths, err := readCachePathsIn(readCacheSubdirectory(c.readCacheScopeIdentity), cacheKey)
	if err != nil {
		log.Debug("civisibility.read_cache: cache path unavailable [endpoint:%s key:%s error:%s]", endpoint, cacheKey, err.Error())
		return readCacheLiveValue(live)
	}
	readCacheCleanupCurrentKey(paths)

	scope := c.readCacheScopeIdentity.Kind
	if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL); ok {
		log.Debug("civisibility.read_cache: hit [endpoint:%s key:%s scope:%s]", endpoint, cacheKey, scope)
		if onCacheHit != nil {
			onCacheHit(value)
		}
		return value, nil
	}

	// asOwner requests and publishes the entry while holding its key's lock.
	asOwner := func(owner *readCacheLockOwner) (T, error) {
		defer releaseReadCacheLock(owner)
		readCacheGlobalCleanup(paths.Dir)

		if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL); ok {
			log.Debug("civisibility.read_cache: hit [endpoint:%s key:%s scope:%s]", endpoint, cacheKey, scope)
			if onCacheHit != nil {
				onCacheHit(value)
			}
			return value, nil
		}

		started := time.Now()
		result, err := live()
		log.Debug("civisibility.read_cache: miss [endpoint:%s key:%s scope:%s duration:%s cacheable:%t error:%t]", endpoint, cacheKey, scope, time.Since(started).Round(time.Microsecond), err == nil && result.Cacheable, err != nil)
		if err != nil {
			return zero, err
		}
		if result.Cacheable {
			writeReadCacheEntry(paths, owner, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL, result.Value)
		}
		return result.Value, nil
	}

	owner, status := acquireReadCacheLock(paths, cacheKey)
	switch status {
	case readCacheLockAcquired:
		return asOwner(owner)
	case readCacheLockActive:
		started := time.Now()
		value, outcome := waitForReadCacheEntry[T](c.requestContext(), paths, cacheKey, baseScope, endpointScope, c.readCacheScopeIdentity.TTL)
		switch outcome {
		case readCacheWaitHit:
			log.Debug("civisibility.read_cache: hit after wait [endpoint:%s key:%s scope:%s waited:%s]", endpoint, cacheKey, scope, time.Since(started).Round(time.Microsecond))
			if onCacheHit != nil {
				onCacheHit(value)
			}
			return value, nil
		case readCacheWaitOwnerGone:
			// The owner's process ended without releasing its lock, which is
			// therefore stale: take its place, so that the entry is written.
			if owner, status := acquireReadCacheLock(paths, cacheKey); status == readCacheLockAcquired {
				log.Debug("civisibility.read_cache: owner gone, taking over [endpoint:%s key:%s scope:%s waited:%s]", endpoint, cacheKey, scope, time.Since(started).Round(time.Microsecond))
				return asOwner(owner)
			}
		}
		log.Debug("civisibility.read_cache: waiter fallback [endpoint:%s key:%s scope:%s waited:%s]", endpoint, cacheKey, scope, time.Since(started).Round(time.Microsecond))
		return readCacheLiveValue(live)
	default:
		log.Debug("civisibility.read_cache: lock bypass [endpoint:%s key:%s]", endpoint, cacheKey)
		return readCacheLiveValue(live)
	}
}

func readCacheEndpointVersionFor(endpoint string) int {
	if endpoint == readCacheEndpointSkippableTests {
		return readCacheEndpointSkippableTestsVersion
	}
	return readCacheEndpointVersion
}

func readCacheLiveValue[T any](live func() (readCacheLiveResult[T], error)) (T, error) {
	result, err := live()
	return result.Value, err
}

func readCacheHashJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func readCacheHashValue(value any) string {
	hash, err := readCacheHashJSON(value)
	if err != nil {
		return readCacheHashString("")
	}
	return hash
}

func readCacheHashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func readCacheKey(baseScope readCacheScope, endpointScope readCacheEndpointScope) (string, error) {
	base, err := json.Marshal(baseScope)
	if err != nil {
		return "", err
	}
	endpoint, err := json.Marshal(endpointScope)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(append(base, '\n'), endpoint...))
	return hex.EncodeToString(sum[:]), nil
}

func readCacheSanitizeBaseURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func readCachePathsForKey(cacheKey string) (readCachePaths, error) {
	return readCachePathsIn("", cacheKey)
}

// readCachePathsIn returns the paths of cacheKey's files in subdir of the
// shared cache directory, or in that directory itself.
func readCachePathsIn(subdir, cacheKey string) (readCachePaths, error) {
	dir, err := readCacheDirectory(subdir)
	if err != nil {
		return readCachePaths{}, err
	}
	if err := ensureReadCacheDir(dir); err != nil {
		return readCachePaths{}, err
	}
	return readCachePaths{
		Dir:       dir,
		CacheFile: filepath.Join(dir, cacheKey+".json"),
		LockFile:  filepath.Join(dir, cacheKey+".lock"),
		TmpGlob:   filepath.Join(dir, cacheKey+".tmp.*"),
	}, nil
}

// readCacheDirectory returns subdir of the shared cache directory, or that
// directory itself.
func readCacheDirectory(subdir string) (string, error) {
	root, err := readCacheBaseRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "dd-trace-go", "civisibility-read-cache", subdir), nil
}

func readCacheBaseRoot() (string, error) {
	if root := readCacheHookSnapshot().cacheRoot; root != "" {
		return root, nil
	}
	root, err := os.UserCacheDir()
	if err == nil && root != "" {
		return root, nil
	}
	if root := os.TempDir(); root != "" {
		return root, nil
	}
	return "", errors.New("no cache root available")
}

func ensureReadCacheDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	info, err = os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is not private", dir)
	}
	return nil
}

func readFreshReadCacheEntry[T any](paths readCachePaths, cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration) (T, bool) {
	var zero T

	raw, err := os.ReadFile(paths.CacheFile)
	if err != nil {
		return zero, false
	}

	var entry readCacheEntry[T]
	if err := json.Unmarshal(raw, &entry); err != nil {
		log.Debug("civisibility.read_cache: invalid entry [key:%s error:%s]", cacheKey, err.Error())
		removeReadCacheFile(paths.CacheFile)
		return zero, false
	}
	if !readCacheEntryValid(entry, cacheKey, baseScope, endpointScope, ttl) {
		removeReadCacheFile(paths.CacheFile)
		return zero, false
	}
	return entry.Response, true
}

func readCacheEntryValid[T any](entry readCacheEntry[T], cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration) bool {
	if entry.CacheKey != cacheKey {
		return false
	}
	if entry.TTLSeconds != int64(ttl.Seconds()) {
		return false
	}
	if !reflect.DeepEqual(entry.BaseScope, baseScope) {
		return false
	}
	if !reflect.DeepEqual(entry.EndpointScope, endpointScope) {
		return false
	}

	now := readCacheNow()
	createdAt := time.Unix(0, entry.CreatedAtUnixNano)
	if createdAt.After(now) {
		return false
	}
	return now.Sub(createdAt) <= ttl
}

func writeReadCacheEntry[T any](paths readCachePaths, owner *readCacheLockOwner, cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration, value T) {
	if owner == nil || !readCacheLockOwnerStillValid(owner) {
		return
	}

	entry := readCacheEntry[T]{
		CacheKey:          cacheKey,
		CreatedAtUnixNano: readCacheNow().UnixNano(),
		TTLSeconds:        int64(ttl.Seconds()),
		BaseScope:         baseScope,
		EndpointScope:     endpointScope,
		Response:          value,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		log.Debug("civisibility.read_cache: marshal failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}

	tmpPath := filepath.Join(paths.Dir, fmt.Sprintf("%s.tmp.%d.%s", cacheKey, readCachePID(), owner.Entry.OwnerNonce))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		log.Debug("civisibility.read_cache: temp create failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}
	defer removeReadCacheFile(tmpPath)

	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		log.Debug("civisibility.read_cache: temp write failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}
	if err := file.Close(); err != nil {
		log.Debug("civisibility.read_cache: temp close failed [key:%s error:%s]", cacheKey, err.Error())
		return
	}
	if !readCacheLockOwnerStillValid(owner) {
		return
	}
	if err := os.Rename(tmpPath, paths.CacheFile); err != nil {
		removeReadCacheFile(paths.CacheFile)
		if retryErr := os.Rename(tmpPath, paths.CacheFile); retryErr != nil {
			log.Debug("civisibility.read_cache: rename failed [key:%s error:%s]", cacheKey, retryErr.Error())
			return
		}
	}
}

func acquireReadCacheLock(paths readCachePaths, cacheKey string) (*readCacheLockOwner, readCacheLockAcquireStatus) {
	owner, err := newReadCacheLockOwner(paths.LockFile, cacheKey)
	if err != nil {
		log.Debug("civisibility.read_cache: lock nonce failed [key:%s error:%s]", cacheKey, err.Error())
		return nil, readCacheLockBypass
	}
	if status := tryCreateReadCacheLock(owner); status == readCacheLockAcquired {
		return owner, status
	} else if status == readCacheLockBypass {
		return nil, status
	}

	stale, ok := readCacheLockIsStale(paths.LockFile)
	if !stale {
		return nil, readCacheLockActive
	}
	if !ok {
		log.Debug("civisibility.read_cache: stale malformed lock [key:%s]", cacheKey)
	}
	removeReadCacheFile(paths.LockFile)
	if status := tryCreateReadCacheLock(owner); status == readCacheLockAcquired {
		return owner, status
	}
	return nil, readCacheLockBypass
}

func newReadCacheLockOwner(lockPath string, cacheKey string) (*readCacheLockOwner, error) {
	nonce, err := readCacheNonce()
	if err != nil {
		return nil, err
	}
	return &readCacheLockOwner{
		Path: lockPath,
		Entry: readCacheLockEntry{
			PID:               readCachePID(),
			CreatedAtUnixNano: readCacheNow().UnixNano(),
			CacheKey:          cacheKey,
			OwnerNonce:        nonce,
		},
	}, nil
}

func tryCreateReadCacheLock(owner *readCacheLockOwner) readCacheLockAcquireStatus {
	raw, err := json.Marshal(owner.Entry)
	if err != nil {
		return readCacheLockBypass
	}
	file, err := os.OpenFile(owner.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsNotExist(err) {
		// The cleanup removed the empty directory of an invocation's entries
		// after this process created it.
		if err = ensureReadCacheDir(filepath.Dir(owner.Path)); err == nil {
			file, err = os.OpenFile(owner.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		}
	}
	if err != nil {
		if os.IsExist(err) {
			return readCacheLockActive
		}
		log.Debug("civisibility.read_cache: lock create failed [key:%s error:%s]", owner.Entry.CacheKey, err.Error())
		return readCacheLockBypass
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		removeReadCacheFile(owner.Path)
		log.Debug("civisibility.read_cache: lock write failed [key:%s error:%s]", owner.Entry.CacheKey, err.Error())
		return readCacheLockBypass
	}
	if err := file.Close(); err != nil {
		removeReadCacheFile(owner.Path)
		log.Debug("civisibility.read_cache: lock close failed [key:%s error:%s]", owner.Entry.CacheKey, err.Error())
		return readCacheLockBypass
	}
	return readCacheLockAcquired
}

// readCacheWaitOutcome is how a wait for another process's entry ended.
type readCacheWaitOutcome int

const (
	// readCacheWaitHit: the entry appeared.
	readCacheWaitHit readCacheWaitOutcome = iota
	// readCacheWaitFallback: the owner released its lock without an entry, the
	// wait timed out or ctx is done; the waiter requests without the lock.
	readCacheWaitFallback
	// readCacheWaitOwnerGone: the owner's process ended without releasing its
	// lock; the waiter can take its place.
	readCacheWaitOwnerGone
)

// waitForReadCacheEntry waits for the lock owner's entry. It waits up to
// readCacheWaiterTimeout, and longer while the owner still runs and its lock
// is younger than readCacheWaiterLiveOwnerTimeout, but never longer than that
// from its own start: a lock that new owners keep replacing cannot extend it.
// It stops when ctx is done: ddto's Git worker, which stopped, does not wait
// for another process.
func waitForReadCacheEntry[T any](ctx context.Context, paths readCachePaths, cacheKey string, baseScope readCacheScope, endpointScope readCacheEndpointScope, ttl time.Duration) (T, readCacheWaitOutcome) {
	var zero T

	if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, ttl); ok {
		return value, readCacheWaitHit
	}

	started := readCacheNow()
	deadline := started.Add(readCacheWaiterTimeout)
	liveDeadline := started.Add(readCacheWaiterLiveOwnerTimeout)
	backoff := readCacheWaiterInitialBackoff
	for {
		if ctx.Err() != nil {
			return zero, readCacheWaitFallback
		}
		lock, err := readCacheLockEntryFromFile(paths.LockFile)
		if os.IsNotExist(err) {
			if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, ttl); ok {
				return value, readCacheWaitHit
			}
			return zero, readCacheWaitFallback
		}
		waitLonger := false
		if err == nil {
			switch alive, known := readCacheProcessAlive(lock.PID); {
			case known && !alive:
				return zero, readCacheWaitOwnerGone
			case known:
				// A lock from the future, as after a clock change, gets the
				// short wait.
				now := readCacheNow()
				age := now.Sub(time.Unix(0, lock.CreatedAtUnixNano))
				waitLonger = age >= 0 && age < readCacheWaiterLiveOwnerTimeout && now.Before(liveDeadline)
			}
		}
		if !waitLonger && !readCacheNow().Before(deadline) {
			return zero, readCacheWaitFallback
		}

		readCacheSleep(backoff)
		if value, ok := readFreshReadCacheEntry[T](paths, cacheKey, baseScope, endpointScope, ttl); ok {
			return value, readCacheWaitHit
		}
		backoff = min(2*backoff, readCacheWaiterMaxBackoff)
	}
}

func readCacheLockOwnerStillValid(owner *readCacheLockOwner) bool {
	if owner == nil {
		return false
	}
	current, err := readCacheLockEntryFromFile(owner.Path)
	if err != nil {
		return false
	}
	return current.CacheKey == owner.Entry.CacheKey && current.OwnerNonce == owner.Entry.OwnerNonce
}

func releaseReadCacheLock(owner *readCacheLockOwner) {
	if !readCacheLockOwnerStillValid(owner) {
		return
	}
	removeReadCacheFile(owner.Path)
}

func readCacheLockIsStale(lockPath string) (stale bool, decoded bool) {
	lockEntry, err := readCacheLockEntryFromFile(lockPath)
	if err == nil {
		// A lock whose owner's process ended is stale too: nothing will
		// release it.
		if alive, known := readCacheProcessAlive(lockEntry.PID); known && !alive {
			return true, true
		}
		return readCacheNow().Sub(time.Unix(0, lockEntry.CreatedAtUnixNano)) > readCacheStaleLockTimeout, true
	}
	info, statErr := os.Stat(lockPath)
	if statErr != nil {
		return false, false
	}
	return readCacheNow().Sub(info.ModTime()) > readCacheStaleLockTimeout, false
}

func readCacheLockEntryFromFile(lockPath string) (readCacheLockEntry, error) {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return readCacheLockEntry{}, err
	}
	var lockEntry readCacheLockEntry
	if err := json.Unmarshal(raw, &lockEntry); err != nil {
		return readCacheLockEntry{}, err
	}
	if lockEntry.OwnerNonce == "" {
		return readCacheLockEntry{}, errors.New("lock missing owner nonce")
	}
	return lockEntry, nil
}

func readCacheCleanupCurrentKey(paths readCachePaths) {
	stale, _ := readCacheLockIsStale(paths.LockFile)
	if stale {
		removeReadCacheFile(paths.LockFile)
	}

	matches, err := filepath.Glob(paths.TmpGlob)
	if err != nil {
		return
	}
	for _, path := range matches {
		if readCachePathOlderThan(path, readCacheStaleLockTimeout) {
			removeReadCacheFile(path)
		}
	}
}

func readCacheGlobalCleanup(dir string) {
	start := readCacheNow()
	dirFile, err := os.Open(dir)
	if err != nil {
		log.Debug("civisibility.read_cache: global cleanup read failed [error:%s]", err.Error())
		return
	}
	defer dirFile.Close()

	entries, err := dirFile.ReadDir(readCacheGlobalCleanupMaxEntries)
	if err != nil && !errors.Is(err, io.EOF) {
		log.Debug("civisibility.read_cache: global cleanup read failed [error:%s]", err.Error())
		return
	}
	for i, entry := range entries {
		if i >= readCacheGlobalCleanupMaxEntries || readCacheNow().Sub(start) > readCacheGlobalCleanupMaxDuration {
			return
		}
		path := filepath.Join(dir, entry.Name())
		switch {
		case strings.HasSuffix(entry.Name(), ".json"):
			if readCacheJSONFileExpired(path) {
				removeExpiredReadCacheEntry(path)
			}
		case strings.HasSuffix(entry.Name(), ".lock"):
			if stale, _ := readCacheLockIsStale(path); stale {
				removeReadCacheFile(path)
			}
		case strings.Contains(entry.Name(), ".tmp."):
			if readCachePathOlderThan(path, readCacheStaleLockTimeout) {
				removeReadCacheFile(path)
			}
		}
	}
}

// removeExpiredReadCacheEntry removes the expired entry at path under its
// key's lock, which a writer holds while it publishes the entry, so that it
// does not remove an entry that a writer has just renewed. With the lock
// taken by a writer, it leaves the entry; a stale lock, which a killed process
// left, it replaces, as a request does.
func removeExpiredReadCacheEntry(path string) {
	cacheKey := strings.TrimSuffix(filepath.Base(path), ".json")
	base := strings.TrimSuffix(path, ".json")
	paths := readCachePaths{Dir: filepath.Dir(path), CacheFile: path, LockFile: base + ".lock", TmpGlob: base + ".tmp.*"}
	owner, status := acquireReadCacheLock(paths, cacheKey)
	if status != readCacheLockAcquired {
		return
	}
	defer releaseReadCacheLock(owner)
	removeExpiredEntryAsOwner(owner, path)
}

// removeExpiredEntryAsOwner removes the expired entry at path if owner still
// holds its key's lock, as writeReadCacheEntry checks before it publishes:
// another process may have replaced the lock as stale.
func removeExpiredEntryAsOwner(owner *readCacheLockOwner, path string) {
	if readCacheJSONFileExpired(path) && readCacheLockOwnerStillValid(owner) {
		removeReadCacheFile(path)
	}
}

func readCacheJSONFileExpired(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var envelope struct {
		CreatedAtUnixNano int64 `json:"created_at_unix_nano"`
		TTLSeconds        int64 `json:"ttl_seconds"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	if envelope.CreatedAtUnixNano <= 0 || envelope.TTLSeconds <= 0 {
		return false
	}
	return readCacheNow().Sub(time.Unix(0, envelope.CreatedAtUnixNano)) > time.Duration(envelope.TTLSeconds)*time.Second
}

func readCachePathOlderThan(path string, maxAge time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return readCacheNow().Sub(info.ModTime()) > maxAge
}

func removeReadCacheFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Debug("civisibility.read_cache: remove failed [path:%s error:%s]", filepath.Base(path), err.Error())
	}
}

func readCacheNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func readCacheHookSnapshot() readCacheHooks {
	readCacheHooksMu.RLock()
	defer readCacheHooksMu.RUnlock()

	return readCacheState
}

func readCacheNow() time.Time {
	if now := readCacheHookSnapshot().now; now != nil {
		return now()
	}
	return time.Now()
}

func readCachePID() int {
	if pid := readCacheHookSnapshot().pid; pid != nil {
		return pid()
	}
	return os.Getpid()
}

func readCacheParentPID() int {
	if parentPID := readCacheHookSnapshot().parentPID; parentPID != nil {
		return parentPID()
	}
	return os.Getppid()
}

// readCacheProcessAlive reports whether the process pid runs, and whether that
// is known.
func readCacheProcessAlive(pid int) (alive bool, known bool) {
	if processAlive := readCacheHookSnapshot().processAlive; processAlive != nil {
		return processAlive(pid)
	}
	return processAlive(pid)
}

func readCacheSleep(duration time.Duration) {
	if sleep := readCacheHookSnapshot().sleep; sleep != nil {
		sleep(duration)
		return
	}
	time.Sleep(duration)
}
