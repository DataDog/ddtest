// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	assert "github.com/tonyredondo/dd-ci-testing-poc/internal/testassert"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	civisibilityutils "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

// A local run's read-cache scope is ddto's invocation when the process uses
// its state: the Git worker and the test processes, whose parents differ,
// share entries. CI scopes already include both and stay.
func TestCLIReadCacheScopeIdentity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
	local := newReadCacheScopeIdentity(map[string]string{})
	worker := cliReadCacheScopeIdentity(local, "/state", "", "")
	setReadCacheHooksForTest(t, t.TempDir(), &now, 333, 444)
	process := cliReadCacheScopeIdentity(newReadCacheScopeIdentity(map[string]string{}), "/state", "", "")

	require.Equal(t, readCacheScopeCLI, worker.Kind)
	require.Equal(t, time.Hour, worker.TTL)
	require.Equal(t, worker, process)
	assert.NotEqual(t, worker.Hash, cliReadCacheScopeIdentity(local, "/other", "", "").Hash)
	// The other scopes keep their kind and lifetime, and also separate API
	// keys and Agents: a CI job's scope outlives one invocation.
	strong := newReadCacheScopeIdentity(map[string]string{constants.CIProviderName: "github", constants.CIJobID: "job"})
	for _, tc := range []struct {
		identity readCacheScopeIdentity
		dir      string
	}{{local, ""}, {strong, "/state"}} {
		scoped := cliReadCacheScopeIdentity(tc.identity, tc.dir, "key-a", "")
		require.Equal(t, tc.identity.Kind, scoped.Kind)
		require.Equal(t, tc.identity.TTL, scoped.TTL)
		require.Equal(t, scoped, cliReadCacheScopeIdentity(tc.identity, tc.dir, "key-a", ""))
		assert.NotEqual(t, scoped.Hash, cliReadCacheScopeIdentity(tc.identity, tc.dir, "key-b", "").Hash)
		assert.NotEqual(t, scoped.Hash, cliReadCacheScopeIdentity(tc.identity, tc.dir, "key-a", "/run/agent.sock").Hash)
	}
}

// The worker's copies of its client request as the test processes do,
// although the worker runs another toolchain: the read cache's scope and every
// field of their requests match a test process's client. There is one copy per
// service that the test processes report.
func TestCLITestProcessClients(t *testing.T) {
	origEnv := saveEnv()
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(os.Getenv("PATH"), "https://custom.agentless.url")

	process, ok := NewClient().(*client)
	require.True(t, ok)
	worker, ok := NewClient().(*client)
	require.True(t, ok)
	worker.testConfigurations.RuntimeVersion = "go1.0-ddto"
	worker.testConfigurations.OsArchitecture = "ddto-arch"

	clients := CLITestProcessClients(worker, process.testConfigurations.RuntimeVersion, process.testConfigurations.OsArchitecture, nil)
	require.Len(t, clients, 1)
	copied, ok := clients[0].(*client)
	require.True(t, ok)
	request := func(c *client) client {
		fields := *c
		fields.id, fields.headers, fields.handler = "", nil, nil
		return fields
	}
	require.Equal(t, request(process), request(copied))
	require.Equal(t, process.readCacheBaseScope(), copied.readCacheBaseScope())
	require.Equal(t, "go1.0-ddto", worker.testConfigurations.RuntimeVersion)

	dirs := []string{t.TempDir(), t.TempDir()}
	clients = CLITestProcessClients(worker, "go1.99.0", "", dirs)
	require.Len(t, clients, 1)
	require.Equal(t, "dd-trace-go", clients[0].(*client).serviceName)
	require.Equal(t, "go1.99.0", clients[0].(*client).testConfigurations.RuntimeVersion)
	require.Equal(t, "ddto-arch", clients[0].(*client).testConfigurations.OsArchitecture)
	os.Setenv("DD_SERVICE", "named")
	clients = CLITestProcessClients(worker, "go1.99.0", "", dirs)
	require.Len(t, clients, 1)
	require.Equal(t, "named", clients[0].(*client).serviceName)
}

// The invocation's entries outlast the local TTL, as a long build does, and
// live in the invocation's own directory, which ddto removes afterwards.
func TestCLIReadCacheLastsTheInvocation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)
	// Without custom configurations, as clients have them, entries survive
	// their JSON encoding.
	worker := newReadCacheTestClient(map[string]string{})
	worker.testConfigurations.Custom = nil
	worker.readCacheScopeIdentity = cliReadCacheScopeIdentity(worker.readCacheScopeIdentity, "/state", "", "")
	setReadCacheHooksForTest(t, root, &now, 333, 444)
	process := newReadCacheTestClient(map[string]string{})
	process.testConfigurations.Custom = nil
	process.readCacheScopeIdentity = cliReadCacheScopeIdentity(process.readCacheScopeIdentity, "/state", "", "")
	request := map[string]string{"request": "ahead"}
	live := 0
	fetch := func(c *client) string {
		t.Helper()
		value, err := readThroughShortLivedCache(c, "unit", request, func() (readCacheLiveResult[string], error) {
			live++
			return readCacheLiveResult[string]{Value: "response", Cacheable: true}, nil
		}, nil)
		require.NoError(t, err)
		return value
	}

	require.Equal(t, "response", fetch(worker))
	now = now.Add(5 * time.Minute)
	require.Equal(t, "response", fetch(process))
	require.Equal(t, 1, live)

	shared := filepath.Join(root, "dd-trace-go", "civisibility-read-cache")
	entries, err := os.ReadDir(shared)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, readCacheCLIDirectory, entries[0].Name())
	own := filepath.Join(shared, readCacheSubdirectory(worker.readCacheScopeIdentity))
	_, err = os.Stat(own)
	require.NoError(t, err)
	removeCLIReadCache(t)
	_, err = os.Stat(own)
	require.True(t, os.IsNotExist(err))
}

// A stopped worker does not wait for another process's request: the read
// cache's waiter stops with the client's context.
func TestReadCacheWaiterStopsWithContext(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)
	c := newReadCacheTestClient(map[string]string{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.handler = &RequestHandler{Context: ctx}
	request := map[string]string{"request": "stopped"}
	cacheKey, paths := readCacheTestKeyAndPaths(t, c, "unit", request)
	owner, status := acquireReadCacheLock(paths, cacheKey)
	require.Equal(t, readCacheLockAcquired, status)
	defer releaseReadCacheLock(owner)
	sleeps := 0
	SetReadCacheHooksForTesting(root, func() time.Time { return now }, func() int { return 111 }, func() int { return 222 }, func(d time.Duration) {
		sleeps++
		cancel()
		now = now.Add(d)
	})

	_, err := readThroughShortLivedCache(c, "unit", request, func() (readCacheLiveResult[string], error) {
		return readCacheLiveResult[string]{}, ctx.Err()
	}, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, sleeps)
}

// writeCLIEntry writes, in the invocation directory dir, an entry named name
// created at createdAt that lasts ttl.
func writeCLIEntry(t *testing.T, dir, name string, createdAt time.Time, ttl time.Duration) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	entry := fmt.Sprintf(`{"created_at_unix_nano":%d,"ttl_seconds":%d}`, createdAt.UnixNano(), int64(ttl.Seconds()))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(entry), 0o600))
}

// A kill or a reboot leaves an invocation's directory. A later invocation
// removes its expired entries, as the shared directory's cleanup does, and
// then the emptied directory. A running invocation's entries, which have not
// expired, stay, and so does a directory where a process holds a lock.
func TestRemoveCLIReadCacheRemovesExpiredEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)
	shared := filepath.Join(root, "dd-trace-go", "civisibility-read-cache")
	cli := filepath.Join(shared, readCacheCLIDirectory)
	writeCLIEntry(t, filepath.Join(cli, "ended"), "ended.json", now.Add(-2*time.Hour), time.Hour)
	writeCLIEntry(t, filepath.Join(cli, "running"), "running.json", now.Add(-time.Minute), time.Hour)
	writeCLIEntry(t, filepath.Join(cli, "writing"), "writing.lock", now.Add(-time.Minute), time.Hour)
	writeCLIEntry(t, shared, "shared.json", now.Add(-2*time.Hour), time.Hour)

	require.Equal(t, 1, removeCLIReadCache(t))
	for name, kept := range map[string]bool{
		filepath.Join(readCacheCLIDirectory, "ended"):                   false,
		filepath.Join(readCacheCLIDirectory, "running", "running.json"): true,
		filepath.Join(readCacheCLIDirectory, "writing", "writing.lock"): true,
		"shared.json": true,
	} {
		_, err := os.Stat(filepath.Join(shared, name))
		require.Equal(t, kept, err == nil, name)
	}
}

// Each invocation examines a bounded number of directories. Directories that
// stay, whatever their order, hide the others only until their entries expire.
func TestRemoveCLIReadCacheReachesEveryDirectory(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)
	cli := filepath.Join(root, "dd-trace-go", "civisibility-read-cache", readCacheCLIDirectory)
	for i := range readCacheCLICleanupMaxDirectories {
		writeCLIEntry(t, filepath.Join(cli, fmt.Sprint("fresh-", i)), "entry.json", now.Add(-time.Minute), time.Hour)
	}
	for i := range 2 {
		writeCLIEntry(t, filepath.Join(cli, fmt.Sprint("expired-", i)), "entry.json", now.Add(-2*time.Hour), time.Hour)
	}
	count := func() int {
		entries, err := os.ReadDir(cli)
		require.NoError(t, err)
		return len(entries)
	}

	// The first batch, in the directory's order, may hold the expired
	// directories or not; the fresh ones stay either way.
	before := count()
	removed := removeCLIReadCache(t)
	for i := range readCacheCLICleanupMaxDirectories {
		_, err := os.Stat(filepath.Join(cli, fmt.Sprint("fresh-", i), "entry.json"))
		require.NoError(t, err)
	}
	left := count()
	require.Equal(t, before-left, removed)
	// Once every entry has expired, a call removes the bounded number of
	// directories, and the next one the rest.
	now = now.Add(2 * time.Hour)
	require.Equal(t, readCacheCLICleanupMaxDirectories, removeCLIReadCache(t))
	require.Equal(t, left-readCacheCLICleanupMaxDirectories, count())
	require.Equal(t, left-readCacheCLICleanupMaxDirectories, removeCLIReadCache(t))
	require.Equal(t, 0, count())
}

// The cleanup removes an expired entry under its key's lock: a writer that
// holds the lock is renewing the entry, which stays.
func TestReadCacheCleanupLeavesAnEntryBeingRenewed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
	paths, err := readCachePathsIn("", "renewed")
	require.NoError(t, err)
	writeCLIEntry(t, paths.Dir, "renewed.json", now.Add(-2*time.Hour), time.Hour)
	writeCLIEntry(t, paths.Dir, "expired.json", now.Add(-2*time.Hour), time.Hour)
	writer, status := acquireReadCacheLock(paths, "renewed")
	require.Equal(t, readCacheLockAcquired, status)

	readCacheGlobalCleanup(paths.Dir)
	_, err = os.Stat(paths.CacheFile)
	require.NoError(t, err, "the cleanup removed an entry under a writer's lock")
	_, err = os.Stat(filepath.Join(paths.Dir, "expired.json"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(paths.Dir, "expired.lock"))
	require.True(t, os.IsNotExist(err), "the cleanup left its lock")
	releaseReadCacheLock(writer)
}

// The cleanup can remove an invocation's empty directory after a process
// created it for a request: the process creates it again for its lock, and the
// directory, which then holds the lock, cannot go while the process writes.
func TestReadCacheLockRecreatesRemovedDirectory(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
	identity := cliReadCacheScopeIdentity(newReadCacheScopeIdentity(map[string]string{}), "/state", "", "")
	paths, err := readCachePathsIn(readCacheSubdirectory(identity), "key")
	require.NoError(t, err)
	require.NoError(t, os.Remove(paths.Dir))

	owner, status := acquireReadCacheLock(paths, "key")
	require.Equal(t, readCacheLockAcquired, status)
	require.Error(t, os.Remove(paths.Dir))
	releaseReadCacheLock(owner)
}

// A process killed while it held a key's lock leaves a stale lock, which the
// cleanup may not list: it replaces it, as a request does, and removes the
// expired entry and its own lock.
func TestReadCacheCleanupReplacesStaleLocks(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
	paths, err := readCachePathsIn("", "abandoned")
	require.NoError(t, err)
	writeCLIEntry(t, paths.Dir, "abandoned.json", now.Add(-2*time.Hour), time.Hour)
	stale := fmt.Sprintf(`{"pid":1,"created_at_unix_nano":%d,"cache_key":"abandoned","owner_nonce":"killed"}`, now.Add(-2*time.Hour).UnixNano())
	require.NoError(t, os.WriteFile(paths.LockFile, []byte(stale), 0o600))

	removeExpiredReadCacheEntry(paths.CacheFile)
	for _, path := range []string{paths.CacheFile, paths.LockFile} {
		_, err := os.Stat(path)
		require.True(t, os.IsNotExist(err), path)
	}
}

// The cleanup removes an expired entry only while it still holds the key's
// lock: another process may have replaced it as stale, and a writer may then
// renew the entry.
func TestReadCacheCleanupRemovesOnlyAsLockOwner(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	setReadCacheHooksForTest(t, t.TempDir(), &now, 111, 222)
	paths, err := readCachePathsIn("", "replaced")
	require.NoError(t, err)
	writeCLIEntry(t, paths.Dir, "replaced.json", now.Add(-2*time.Hour), time.Hour)
	cleaner, status := acquireReadCacheLock(paths, "replaced")
	require.Equal(t, readCacheLockAcquired, status)
	replacement := fmt.Sprintf(`{"pid":333,"created_at_unix_nano":%d,"cache_key":"replaced","owner_nonce":"other"}`, now.UnixNano())
	require.NoError(t, os.WriteFile(paths.LockFile, []byte(replacement), 0o600))

	removeExpiredEntryAsOwner(cleaner, paths.CacheFile)
	_, err = os.Stat(paths.CacheFile)
	require.NoError(t, err, "a cleaner that lost the lock removed the entry")

	require.NoError(t, os.Remove(paths.LockFile))
	owner, status := acquireReadCacheLock(paths, "replaced")
	require.Equal(t, readCacheLockAcquired, status)
	removeExpiredEntryAsOwner(owner, paths.CacheFile)
	releaseReadCacheLock(owner)
	_, err = os.Stat(paths.CacheFile)
	require.True(t, os.IsNotExist(err))
}

// A test process whose credential differs from the worker's, as one whose
// TestMain changed DD_API_KEY, takes none of the worker's responses: they are
// another account's. Both keep the invocation's one directory, and no entry
// holds a credential.
func TestCLIReadCacheSeparatesCredentials(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	setReadCacheHooksForTest(t, root, &now, 111, 222)
	withKey := func(key string) *client {
		c := newReadCacheTestClient(map[string]string{})
		c.testConfigurations.Custom = nil
		c.readCacheScopeIdentity = cliReadCacheScopeIdentity(c.readCacheScopeIdentity, "/state", key, "")
		return c
	}
	fetch := func(c *client, response string) string {
		t.Helper()
		value, err := readThroughShortLivedCache(c, "unit", map[string]string{"request": "settings"}, func() (readCacheLiveResult[string], error) {
			return readCacheLiveResult[string]{Value: response, Cacheable: true}, nil
		}, nil)
		require.NoError(t, err)
		return value
	}

	require.Equal(t, "A", fetch(withKey("key-a"), "A"))
	require.Equal(t, "B", fetch(withKey("key-b"), "B"))
	require.Equal(t, "A", fetch(withKey("key-a"), "not requested"))
	directory := readCacheSubdirectory(withKey("key-a").readCacheScopeIdentity)
	require.Equal(t, directory, readCacheSubdirectory(withKey("key-b").readCacheScopeIdentity))
	entries, err := os.ReadDir(filepath.Join(root, "dd-trace-go", "civisibility-read-cache", directory))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, "dd-trace-go", "civisibility-read-cache", directory, entry.Name()))
		require.NoError(t, err)
		require.False(t, strings.Contains(string(data), "key-a") || strings.Contains(string(data), "key-b"), entry.Name())
	}
}

// The clients of ddto's invocation take their scope's credential from their
// own API key, and their Agent's socket from their own Agent URL.
func TestCLIClientScopeIncludesCredential(t *testing.T) {
	origEnv := saveEnv()
	defer restoreEnv(origEnv)
	civisibilityutils.ResetHandedCLIGitStateForTesting()
	t.Cleanup(civisibilityutils.ResetHandedCLIGitStateForTesting)
	civisibilityutils.UseCLIGitState(t.TempDir(), civisibilityutils.CLIGitRecord{})
	path := os.Getenv("PATH")
	scope := func(key string) readCacheScopeIdentity {
		t.Helper()
		setCiVisibilityEnv(path, "https://custom.agentless.url")
		os.Setenv("DD_API_KEY", key)
		c, ok := NewClient().(*client)
		require.True(t, ok)
		return c.readCacheScopeIdentity
	}
	a, b := scope("key-a"), scope("key-b")
	require.Equal(t, readCacheScopeCLI, a.Kind)
	assert.NotEqual(t, a.Hash, b.Hash)
	require.Equal(t, readCacheSubdirectory(a), readCacheSubdirectory(b))
	require.Equal(t, a, scope("key-a"))

	// Both sockets give the same base URL, UDS__tmp_ci_agent.sock.
	agent := func(socket string) (string, readCacheScopeIdentity) {
		t.Helper()
		setCiVisibilityEnv(path, "")
		os.Unsetenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable)
		os.Setenv("DD_TRACE_AGENT_URL", "unix://"+socket)
		c, ok := NewClient().(*client)
		require.True(t, ok)
		return c.baseURL, c.readCacheScopeIdentity
	}
	aURL, aScope := agent("/tmp/ci/agent.sock")
	bURL, bScope := agent("/tmp/ci_agent.sock")
	require.Equal(t, aURL, bURL)
	require.Equal(t, readCacheScopeCLI, aScope.Kind)
	assert.NotEqual(t, aScope.Hash, bScope.Hash)
}

// removeCLIReadCache removes the "/state" invocation's entries and returns how
// many left directories it removed.
func removeCLIReadCache(t *testing.T) int {
	t.Helper()
	removed, err := RemoveCLIReadCache("/state")
	require.NoError(t, err)
	return removed
}
