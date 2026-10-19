// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

// ddto's CLI runs the Git work that every test process of a checkout would
// repeat once per invocation, in a worker started in the invocation's
// directory, and writes the results to a state directory that goenv hands to
// the test processes. A process uses the state only for the checkout the
// worker collected from; any other process, or a missing file, keeps its own
// Git commands.
const (
	// CLIGitRecordFile holds the CLIGitRecord.
	CLIGitRecordFile = "git.json"
	// CLIGitUploadFile marks the end of the repository upload.
	CLIGitUploadFile = "upload.json"
	// CLIGitImpactedFile holds the impacted-tests base and modified files.
	CLIGitImpactedFile = "impacted.json"
	// CLIGitUsedFile marks that a test process uses the state. Without it,
	// as when go test takes every result from its cache, the CLI does not
	// wait for the worker's upload.
	CLIGitUsedFile = "used"
	// CLIGitLinkingFile marks that go test links a test binary, which it does
	// only for a result that is not in its cache: a test process will use the
	// state. ddto's tool creates it.
	CLIGitLinkingFile = "linking"
)

// CLIGitUpload records the end of the CLI's repository upload. Error is set
// when the upload failed or the worker stopped before it ended.
type CLIGitUpload struct {
	Bytes int64  `json:"bytes"`
	Error string `json:"error,omitempty"`
}

// CLIGitCommit is a commit's metadata as getLocalGitData and fetchCommitData
// read it. Dates are Unix seconds, so that each process formats them in its
// own location, as it does after git log.
type CLIGitCommit struct {
	Sha            string `json:"sha"`
	AuthorDate     int64  `json:"author_date"`
	AuthorName     string `json:"author_name"`
	AuthorEmail    string `json:"author_email"`
	CommitterDate  int64  `json:"committer_date"`
	CommitterName  string `json:"committer_name"`
	CommitterEmail string `json:"committer_email"`
	Message        string `json:"message"`
}

// CLIGitRecord is the local Git metadata of the worker's checkout.
type CLIGitRecord struct {
	// GitFolder is the .git entry above the worker's directory.
	GitFolder     string       `json:"git_folder"`
	SourceRoot    string       `json:"source_root"`
	RepositoryURL string       `json:"repository_url"`
	Branch        string       `json:"branch"`
	Commit        CLIGitCommit `json:"commit"`
	// HeadCommit is the CI provider's head commit, if any, and Head its
	// metadata or HeadError the reason fetchCommitData could not read it.
	HeadCommit string       `json:"head_commit,omitempty"`
	Head       CLIGitCommit `json:"head"`
	HeadError  string       `json:"head_error,omitempty"`
	// UploadWaitMs is how long test processes wait for upload.json: ddto's
	// --git-upload-timeout, which also bounds its own wait after go test.
	UploadWaitMs int64 `json:"upload_wait_ms"`
}

// UploadWait is how long a test process waits for the worker's upload.
func (r *CLIGitRecord) UploadWait() time.Duration {
	return time.Duration(r.UploadWaitMs) * time.Millisecond
}

func newCLIGitCommit(data localCommitData) CLIGitCommit {
	return CLIGitCommit{
		Sha: data.CommitSha, AuthorDate: data.AuthorDate.Unix(), AuthorName: data.AuthorName,
		AuthorEmail: data.AuthorEmail, CommitterDate: data.CommitterDate.Unix(),
		CommitterName: data.CommitterName, CommitterEmail: data.CommitterEmail, Message: data.CommitMessage,
	}
}

func (c CLIGitCommit) commitData() localCommitData {
	return localCommitData{
		CommitSha: c.Sha, AuthorDate: time.Unix(c.AuthorDate, 0), AuthorName: c.AuthorName,
		AuthorEmail: c.AuthorEmail, CommitterDate: time.Unix(c.CommitterDate, 0),
		CommitterName: c.CommitterName, CommitterEmail: c.CommitterEmail, CommitMessage: c.Message,
	}
}

// localCommitDataFunc reads a commit's metadata without fetching it; tests
// replace it.
var localCommitDataFunc = func(commitSha string) (localCommitData, error) {
	return readCommitData(commitSha, false)
}

// CollectCLIGitRecord reads the current directory's Git metadata with the
// commands each test process runs while it creates its CI tags. A failed
// local read leaves empty fields, as it does there. It changes nothing in the
// repository: when the CI provider's head commit is missing from a shallow
// clone, the record leaves it out, and test processes fetch it as before.
func CollectCLIGitRecord() CLIGitRecord {
	var record CLIGitRecord
	if wd, err := os.Getwd(); err == nil {
		record.GitFolder, _ = getParentGitFolder(wd)
	}
	gitData, _ := getLocalGitDataFunc()
	record.SourceRoot = gitData.SourceRoot
	record.RepositoryURL = gitData.RepositoryURL
	record.Branch = gitData.Branch
	record.Commit = newCLIGitCommit(gitData.localCommitData)
	if headCommitSha, ok := getProviderTagsFunc()[constants.GitHeadCommit]; ok {
		headCommitData, err := localCommitDataFunc(headCommitSha)
		switch {
		case errors.Is(err, errCommitNotFetched):
		case err != nil:
			record.HeadCommit, record.HeadError = headCommitSha, err.Error()
		default:
			record.HeadCommit, record.Head = headCommitSha, newCLIGitCommit(headCommitData)
		}
	}
	return record
}

func (r *CLIGitRecord) localGitData() localGitData {
	return localGitData{localCommitData: r.Commit.commitData(), SourceRoot: r.SourceRoot, RepositoryURL: r.RepositoryURL, Branch: r.Branch}
}

// headCommitData returns what fetchCommitData would return for sha.
func (r *CLIGitRecord) headCommitData(sha string) (localCommitData, bool, error) {
	if r.HeadCommit != sha {
		return localCommitData{}, false, nil
	}
	if r.HeadError != "" {
		return localCommitData{}, true, errors.New(r.HeadError)
	}
	return r.Head.commitData(), true, nil
}

// WriteCLIGitStateFile writes one state file atomically, so a reader never
// sees a partial value.
func WriteCLIGitStateFile(dir, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return err
	}
	_, err = temp.Write(data)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(temp.Name())
	}
	return err
}

// readCLIGitStateFile is ReadCLIGitStateFile; tests replace it to return the
// errors that another process's hold produces.
var readCLIGitStateFile = ReadCLIGitStateFile

// ReadCLIGitStateFile reads one state file into value, without recording it
// in Go's test log (see openUnlogged).
func ReadCLIGitStateFile(dir, name string, value any) error {
	file, err := openUnlogged(filepath.Join(dir, name), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// WaitCLIGitStateFile waits up to timeout for the CLI to write a state file.
// The CLI writes every file it was asked for, if only to record a failure,
// before go test can exit. A file that the CLI is still moving into place can
// refuse to open for a moment, on Windows: see transientStateFileError.
func WaitCLIGitStateFile(dir, name string, value any, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	delay := 5 * time.Millisecond
	for {
		err := readCLIGitStateFile(dir, name, value)
		if err == nil || !errors.Is(err, os.ErrNotExist) && !transientStateFileError(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(2*delay, 100*time.Millisecond)
	}
}

var (
	cliGitStateOnce sync.Once
	cliGitStateDir  string
	cliGitRecord    *CLIGitRecord
	// cliGitStateOwned is set in ddto's Git worker, which changes nothing in
	// the repository before a test process uses its state.
	cliGitStateOwned bool
)

// HandedCLIGitState returns the CLI's state directory and record when ddto
// handed them to this process and the record describes the checkout of the
// current directory.
func HandedCLIGitState() (string, *CLIGitRecord) {
	cliGitStateOnce.Do(func() {
		dir, ok := goenv.GitState()
		if !ok || dir == "" {
			return
		}
		var record CLIGitRecord
		if err := ReadCLIGitStateFile(dir, CLIGitRecordFile, &record); err != nil {
			log.Debug("civisibility: CLI Git state unavailable: %s", err.Error())
			return
		}
		wd, err := os.Getwd()
		if err != nil || record.GitFolder == "" {
			return
		}
		folder, err := getParentGitFolder(wd)
		if err != nil || folder == "" || !sameFile(folder, record.GitFolder) {
			log.Debug("civisibility: CLI Git state belongs to another checkout: %s", record.GitFolder)
			return
		}
		// The worker uploads, and starts the telemetry application, only
		// once a test process marks the state used: without the mark, this
		// process does its own work.
		used, err := openUnlogged(filepath.Join(dir, CLIGitUsedFile), os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			err = used.Close()
		}
		if err != nil {
			log.Debug("civisibility: CLI Git state not marked used: %s", err.Error())
			telemetry.Log(telemetry.NewRecord(telemetry.LogError, "civisibility: CLI Git state not marked used"))
			return
		}
		cliGitStateDir, cliGitRecord = dir, &record
	})
	return cliGitStateDir, cliGitRecord
}

func sameFile(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

// UseCLIGitState makes this process use record, which it collected itself,
// as a test process uses the state that ddto hands it. ddto's Git worker
// calls it before creating its CI tags, so it reads Git only once.
func UseCLIGitState(dir string, record CLIGitRecord) {
	cliGitStateOnce.Do(func() {
		cliGitStateDir, cliGitRecord, cliGitStateOwned = dir, &record, true
	})
}

// FetchCLIHeadCommit fetches the CI provider's head commit that the worker's
// record left out because a shallow clone lacked it, as each test process
// fetches it. The worker calls it once a test process uses its state, before
// the upload and the impacted-tests diff, which needs that commit.
func FetchCLIHeadCommit() {
	headCommitSha, ok := getProviderTagsFunc()[constants.GitHeadCommit]
	if !ok || cliGitRecord == nil || cliGitRecord.HeadCommit == headCommitSha {
		return
	}
	if _, err := fetchCommitDataFunc(headCommitSha); err != nil {
		log.Debug("civisibility: CLI head commit not fetched: %s", err.Error())
	}
}

// ResetHandedCLIGitStateForTesting forgets the handed state; readers must stop.
func ResetHandedCLIGitStateForTesting() {
	cliGitStateOnce = sync.Once{}
	cliGitStateDir, cliGitRecord, cliGitStateOwned = "", nil, false
	cliTestsOnce = sync.Once{}
	cliTests = nil
}

// SameCheckout reports whether dir lies in the checkout whose .git entry is
// gitFolder, found as a test process finds its own.
func SameCheckout(dir, gitFolder string) bool {
	folder, err := getParentGitFolder(dir)
	return err == nil && folder != "" && gitFolder != "" && sameFile(folder, gitFolder)
}
