package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/DataDog/ddtest/ddto/internal/version"
)

// Bounds of the TIA artifacts requests, and how many commits a lookup names.
const (
	localTIALookupTimeout    = 10 * time.Second
	localTIAUploadTimeout    = 30 * time.Second
	localTIAUnshallowTimeout = 60 * time.Second
	localTIALookupCommits    = 20
)

// newTIAArtifactsClient returns the client of the TIA artifacts API, or nil
// without credentials; tests replace it.
var newTIAArtifactsClient = func() net.TIAArtifactsClient {
	if client := net.NewTIAArtifactsClient(); client != nil {
		return client
	}
	return nil
}

// localTIAMaxUpload is the largest map that a run uploads, in bytes of JSON.
var localTIAMaxUpload = net.MaxTIAArtifactBytes

// unshallowRepository expands a shallow clone, as the Git worker does before
// it uploads the repository's metadata; tests replace it.
var unshallowRepository = utils.UnshallowGitRepository

// localTIARemote shares a run's map through Datadog's TIA artifacts API: the
// key of the run's maps, and the commits that a lookup names, nearest first,
// the first being the one whose map a recording run uploads.
type localTIARemote struct {
	client  net.TIAArtifactsClient
	key     net.TIAArtifactKey
	commits []string
	root    string
	debug   *cliDebug
}

// newLocalTIARemote returns the run's remote, or nil when there is no client,
// as without Datadog credentials, or no repository or commit to key it.
func newLocalTIARemote(state string, record utils.CLIGitRecord, opts options, goflags, dir, configuration string, debug *cliDebug) *localTIARemote {
	// The client reads the CI tags from the Git worker's record, as the
	// test processes do, without reading Git again.
	utils.UseCLIGitState(state, record)
	client := newTIAArtifactsClient()
	if client == nil {
		debug.printf("local tia remote=off reason=%q", "no client, as without an API key")
		return nil
	}
	commit := utils.GetGitCheckoutTags()[constants.GitCommitSHA]
	if commit == "" {
		commit = record.Commit.Sha
	}
	if client.RepositoryID() == "" || commit == "" {
		debug.printf("local tia remote=off reason=%q", "no repository URL or commit")
		return nil
	}
	return &localTIARemote{
		client: client,
		key: net.TIAArtifactKey{RepositoryID: client.RepositoryID(), SuiteID: localTIASuiteID(opts, goflags, record.SourceRoot, dir),
			EnvHash: configuration, Language: "go", CompatibilityVersion: localTIAMapVersion},
		commits: []string{commit},
		root:    record.SourceRoot,
		debug:   debug,
	}
}

// localTIASuiteID identifies the test command that a map belongs to, so that
// jobs that test other packages keep maps of their own: the working
// directory relative to the repository's root, the package patterns and the
// -run and -skip patterns. Flags that the map's configuration leaves out,
// such as the coverage flags, -v or -count, leave it the same, so a run
// without coverage finds the map that a run with it uploaded.
func localTIASuiteID(opts options, goflags, root, dir string) string {
	h := sha256.New()
	relative, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(relative, "..") {
		relative = ""
	}
	fmt.Fprintf(h, "dir %q\n", filepath.ToSlash(relative))
	for _, pattern := range slices.Sorted(slices.Values(opts.packages)) {
		fmt.Fprintf(h, "package %q\n", pattern)
	}
	var words []string
	for _, argument := range opts.arguments {
		words = append(words, argument.raw...)
	}
	flags, _ := splitFlags(goflags)
	words = append(words, flags...)
	for i, word := range words {
		name, value, hasValue := strings.Cut(strings.TrimLeft(word, "-"), "=")
		name = strings.TrimPrefix(name, "test.")
		if !strings.HasPrefix(word, "-") || name != "run" && name != "skip" {
			continue
		}
		if !hasValue && i+1 < len(words) {
			value = words[i+1]
		}
		fmt.Fprintf(h, "%s %q\n", name, value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lookupCommits returns the commit that keys the run and the most recent
// commits before it, nearest first. A shallow clone with only that commit is
// expanded first, as the Git worker would expand it later.
func (r *localTIARemote) lookupCommits(ctx context.Context) []string {
	commits := r.history(ctx)
	if len(commits) <= 1 {
		started := time.Now()
		gitCtx, cancel := context.WithTimeout(ctx, localTIAUnshallowTimeout)
		utils.SetGitCommandContext(gitCtx)
		expanded, err := unshallowRepository()
		utils.SetGitCommandContext(context.Background())
		cancel()
		r.debug.printf("local tia unshallow expanded=%t duration=%s error=%v", expanded, time.Since(started).Round(time.Millisecond), err)
		if expanded {
			commits = r.history(ctx)
		}
	}
	all := slices.Clone(r.commits)
	for _, commit := range commits {
		if !slices.Contains(all, commit) {
			all = append(all, commit)
		}
	}
	return all[:min(len(all), localTIALookupCommits)]
}

// history returns the run's commit and up to localTIALookupCommits-1 of its
// ancestors, nearest first, or none when Git does not have the commit.
func (r *localTIARemote) history(ctx context.Context) []string {
	out, err := exec.CommandContext(ctx, "git", "-C", r.root, "rev-list", "--max-count="+fmt.Sprint(localTIALookupCommits), r.commits[0]).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// lookup returns the map that the API stores for the nearest of the run's
// commits, without the files' sizes and modification times, or nil.
func (r *localTIARemote) lookup(ctx context.Context) *tiaMap {
	started := time.Now()
	commits := r.lookupCommits(ctx)
	lookupCtx, cancel := context.WithTimeout(ctx, localTIALookupTimeout)
	defer cancel()
	artifact, err := r.client.LookupTIAArtifact(lookupCtx, r.key, commits)
	switch {
	case err != nil:
		r.debug.printf("local tia remote lookup failed commits=%d duration=%s error=%q", len(commits), time.Since(started).Round(time.Millisecond), err.Error())
		return nil
	case artifact == nil:
		r.debug.printf("local tia remote lookup miss commits=%d duration=%s", len(commits), time.Since(started).Round(time.Millisecond))
		return nil
	}
	m, ok := unmarshalTIAMap(artifact.Content)
	if !ok {
		r.debug.printf("local tia remote lookup unreadable bytes=%d", len(artifact.Content))
		return nil
	}
	// Sizes and times come from another machine: they cannot vouch for a
	// hash here, so every file is read and hashed.
	for path, state := range m.Files {
		state.Size, state.ModTime = 0, 0
		m.Files[path] = state
	}
	r.debug.printf("local tia remote lookup hit matched=%s source=%s bytes=%d packages=%d duration=%s", artifact.MatchedCommit, artifact.SourceCommit, len(artifact.Content), len(m.Packages), time.Since(started).Round(time.Millisecond))
	return m
}

// upload stores m as the map of the run's commit, as plain JSON without the
// files' sizes and modification times, as dd-trace-py stores its maps. A map
// larger than the API takes is not sent.
func (r *localTIARemote) upload(ctx context.Context, m *tiaMap, warn func(string)) {
	started := time.Now()
	data, err := marshalTIAMap(m, false)
	if err != nil {
		r.debug.printf("local tia remote upload skipped error=%q", err.Error())
		return
	}
	if len(data) > localTIAMaxUpload {
		warn(fmt.Sprintf("the map, %d bytes, is larger than the %d bytes that Datadog stores; it is not shared", len(data), localTIAMaxUpload))
		return
	}
	uploadCtx, cancel := context.WithTimeout(ctx, localTIAUploadTimeout)
	defer cancel()
	err = r.client.StoreTIAArtifact(uploadCtx, r.key, r.commits[0], data)
	r.debug.printf("local tia remote upload commit=%s bytes=%d duration=%s error=%v", r.commits[0], len(data), time.Since(started).Round(time.Millisecond), err)
}

// localTIAWarn prints a local TIA warning.
func localTIAWarn(r *localTIARun) func(string) {
	return func(message string) {
		fmt.Fprintln(r.stderr, version.BuildLogPrefix+" WARN: local TIA ("+LocalTIAEnv+"): "+message)
	}
}
