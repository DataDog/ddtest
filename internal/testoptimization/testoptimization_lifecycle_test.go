package testoptimization

import (
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/git/gittest"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

type settingsSequenceTransport struct {
	MockAPIClient
	settings []*api.SettingsResponseData
	errors   []error
}

func (s *settingsSequenceTransport) GetSettings() (*api.SettingsResponseData, error) {
	s.SettingsCalls++
	index := s.SettingsCalls - 1
	var err error
	if index < len(s.errors) {
		err = s.errors[index]
	}
	if index < len(s.settings) {
		return s.settings[index], err
	}
	return s.Settings, err
}

func TestTestOptimizationClientFeatureGetters(t *testing.T) {
	settings := &api.SettingsResponseData{
		KnownTestsEnabled: true,
		TestsSkipping:     true,
	}
	settings.TestManagement.Enabled = true

	mockTransport := &MockAPIClient{
		Settings: settings,
		KnownTests: &api.KnownTestsResponseData{
			Tests: api.KnownTestsResponseDataModules{
				"module": {"suite": {"test"}},
			},
		},
		SkippableCorrelationID: "correlation-id",
		Skippables: api.Skippables{
			Tests: api.SkippableTests{
				"module.suite.test.": true,
			},
		},
		TestManagementTestsData: &api.TestManagementTestsResponseDataModules{
			Modules: map[string]api.TestManagementTestsResponseDataSuites{
				"module": {
					Suites: map[string]api.TestManagementTestsResponseDataTests{
						"suite": {
							Tests: map[string]api.TestManagementTestsResponseDataTestProperties{
								"test": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
					},
				},
			},
		},
	}
	client := newTestOptimizationClientForTest(t, mockTransport)
	if err := client.Initialize(map[string]string{"custom": "tag"}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	if known := client.GetKnownTests(); known == nil || len(known.Tests) != 1 {
		t.Fatalf("expected known tests, got %#v", known)
	}
	if skippable := client.GetSkippables().Tests; len(skippable) != 1 || !skippable["module.suite.test."] {
		t.Fatalf("expected skippable tests, got %#v", skippable)
	}
	if managed := client.GetTestManagementTestsData(); managed == nil || len(managed.Modules) != 1 {
		t.Fatalf("expected test management data, got %#v", managed)
	}
	if mockTransport.KnownTestsCalls != 1 || mockTransport.SkippableTestsCalls != 1 || mockTransport.TestManagementTestsCalls != 1 {
		t.Fatalf("expected each feature endpoint once, got known=%d skippable=%d testManagement=%d",
			mockTransport.KnownTestsCalls, mockTransport.SkippableTestsCalls, mockTransport.TestManagementTestsCalls)
	}

	ciTags := environment.GetCITags()
	if ciTags[constants.ItrCorrelationIDTag] != "correlation-id" {
		t.Fatalf("expected correlation id tag, got %#v", ciTags)
	}
}

func TestNewTestOptimizationClientWithTestSkippingLevelPropagatesToTransportFactory(t *testing.T) {
	mockTransport := &MockAPIClient{
		Settings: &api.SettingsResponseData{TestsSkipping: true},
	}
	var capturedLevel settings.TestSkippingLevel
	client := newTestOptimizationClientWithTestSkippingLevel(
		nil,
		func(_ string, testSkippingLevel settings.TestSkippingLevel) api.Transport {
			capturedLevel = testSkippingLevel
			return mockTransport
		},
		func() (int64, error) { return 0, nil },
		false,
		settings.TestSkippingLevelSuite,
	)

	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}
	client.GetSkippables()

	if capturedLevel != settings.TestSkippingLevelSuite {
		t.Fatalf("transport factory test skipping level = %q, want %q", capturedLevel, settings.TestSkippingLevelSuite)
	}
}

func TestTestOptimizationClientFeatureGettersDisabled(t *testing.T) {
	client := newTestOptimizationClientForTest(t, &MockAPIClient{
		Settings: &api.SettingsResponseData{},
	})
	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	if known := client.GetKnownTests(); known != nil {
		t.Fatalf("expected nil known tests when disabled, got %#v", known)
	}
	if managed := client.GetTestManagementTestsData(); managed != nil {
		t.Fatalf("expected nil test management tests when disabled, got %#v", managed)
	}
	if skippable := client.GetSkippables().Tests; len(skippable) != 0 {
		t.Fatalf("expected empty skippable map when disabled, got %#v", skippable)
	}
}

func TestEnsureSettingsInitializationRequireGitRetriesAfterUpload(t *testing.T) {
	firstSettings := &api.SettingsResponseData{RequireGit: true}
	secondSettings := &api.SettingsResponseData{KnownTestsEnabled: true}
	transport := &settingsSequenceTransport{
		settings: []*api.SettingsResponseData{firstSettings, secondSettings},
	}
	client := newTestOptimizationClient(
		transport,
		nil,
		func() (int64, error) { return 42, nil },
		false,
	)

	settings := client.GetSettings()
	if settings != secondSettings {
		t.Fatalf("expected second settings response after git upload, got %#v", settings)
	}
	if transport.SettingsCalls != 2 {
		t.Fatalf("expected settings to be requested twice, got %d", transport.SettingsCalls)
	}
}

func TestEnsureSettingsInitializationHandlesMissingTransportAndErrors(t *testing.T) {
	client := newTestOptimizationClient(nil, func(string, settings.TestSkippingLevel) api.Transport { return nil }, nil, false)
	if settings := client.GetSettings(); settings != nil {
		t.Fatalf("expected nil settings without transport, got %#v", settings)
	}

	transport := &settingsSequenceTransport{
		errors: []error{errors.New("settings failed")},
	}
	client = newTestOptimizationClient(transport, nil, func() (int64, error) { return 0, nil }, false)
	if settings := client.GetSettings(); settings != nil {
		t.Fatalf("expected nil settings on transport error, got %#v", settings)
	}
	if len(client.closeActions) != 1 {
		t.Fatalf("expected close action to wait for upload, got %d", len(client.closeActions))
	}
}

func TestEnsureSettingsInitializationHandlesNilResponsesAfterUpload(t *testing.T) {
	transport := &settingsSequenceTransport{
		settings: []*api.SettingsResponseData{nil},
	}
	client := newTestOptimizationClient(transport, nil, func() (int64, error) { return 0, nil }, false)
	if settings := client.GetSettings(); settings != nil {
		t.Fatalf("expected nil settings from nil response, got %#v", settings)
	}
	if len(client.closeActions) != 1 {
		t.Fatalf("expected close action to wait for upload, got %d", len(client.closeActions))
	}

	transport = &settingsSequenceTransport{
		settings: []*api.SettingsResponseData{{RequireGit: true}, nil},
	}
	client = newTestOptimizationClient(transport, nil, func() (int64, error) { return 0, nil }, false)
	if settings := client.GetSettings(); settings != nil {
		t.Fatalf("expected nil settings when require-git retry returns nil, got %#v", settings)
	}
	if transport.SettingsCalls != 2 {
		t.Fatalf("expected retry after require-git upload, got %d calls", transport.SettingsCalls)
	}

	secondErr := errors.New("second settings failed")
	transport = &settingsSequenceTransport{
		settings: []*api.SettingsResponseData{{RequireGit: true}, nil},
		errors:   []error{nil, secondErr},
	}
	client = newTestOptimizationClient(transport, nil, func() (int64, error) { return 0, nil }, false)
	if settings := client.GetSettings(); settings != nil {
		t.Fatalf("expected nil settings when require-git retry fails, got %#v", settings)
	}
}

func TestEnsureAPITransportWithoutFactory(t *testing.T) {
	client := &TestOptimizationClient{cacheManager: NewCacheManager()}
	if transport := client.ensureAPITransport("service"); transport != nil {
		t.Fatalf("expected nil transport without existing transport or factory, got %#v", transport)
	}
}

func TestEnsureTestOptimizationSessionInitializationBranches(t *testing.T) {
	t.Setenv("DD_TRACE_DEBUG", "true")
	t.Setenv(constants.TestOptimizationEnabledEnvironmentVariable, "")
	t.Setenv("DD_TRACE_SAMPLE_RATE", "")
	environment.ResetCITags()
	t.Cleanup(environment.ResetCITags)
	t.Cleanup(func() {
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	})

	client := newTestOptimizationClient(&MockAPIClient{}, nil, func() (int64, error) { return 0, nil }, true)
	client.ensureTestOptimizationSessionInitialized()

	if got := os.Getenv(constants.TestOptimizationEnabledEnvironmentVariable); got != "1" {
		t.Fatalf("%s = %q, want 1", constants.TestOptimizationEnabledEnvironmentVariable, got)
	}
	if got := os.Getenv("DD_TRACE_SAMPLE_RATE"); got != "1" {
		t.Fatalf("DD_TRACE_SAMPLE_RATE = %q, want 1", got)
	}
}

func TestTraceDebugEnabled(t *testing.T) {
	originalValue, originallySet := os.LookupEnv("DD_TRACE_DEBUG")
	t.Cleanup(func() {
		if originallySet {
			_ = os.Setenv("DD_TRACE_DEBUG", originalValue)
		} else {
			_ = os.Unsetenv("DD_TRACE_DEBUG")
		}
	})

	tests := []struct {
		name     string
		value    string
		set      bool
		expected bool
	}{
		{name: "unset", expected: false},
		{name: "true", value: "true", set: true, expected: true},
		{name: "one", value: "1", set: true, expected: true},
		{name: "false", value: "false", set: true, expected: false},
		{name: "invalid", value: "definitely", set: true, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				_ = os.Setenv("DD_TRACE_DEBUG", tt.value)
			} else {
				_ = os.Unsetenv("DD_TRACE_DEBUG")
			}

			if got := traceDebugEnabled(); got != tt.expected {
				t.Fatalf("traceDebugEnabled() = %t, want %t", got, tt.expected)
			}
		})
	}
}

func TestEnsureTestOptimizationInitializedHandlesNilSettingsAndEndpointErrors(t *testing.T) {
	client := newTestOptimizationClient(&MockAPIClient{}, nil, func() (int64, error) { return 0, nil }, false)
	client.ensureTestOptimizationInitialized()

	settings := &api.SettingsResponseData{
		KnownTestsEnabled: true,
		TestsSkipping:     true,
	}
	settings.TestManagement.Enabled = true
	mockTransport := &MockAPIClient{
		Settings:               settings,
		KnownTestsErr:          errors.New("known tests failed"),
		SkippableErr:           errors.New("skippable tests failed"),
		TestManagementTestsErr: errors.New("test management failed"),
	}
	client = newTestOptimizationClientForTest(t, mockTransport)
	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	client.ensureTestOptimizationInitialized()

	if mockTransport.KnownTestsCalls != 1 || mockTransport.SkippableTestsCalls != 1 || mockTransport.TestManagementTestsCalls != 1 {
		t.Fatalf("expected each feature endpoint once, got known=%d skippable=%d testManagement=%d",
			mockTransport.KnownTestsCalls, mockTransport.SkippableTestsCalls, mockTransport.TestManagementTestsCalls)
	}
}

func TestInitializeAddsGitMetadataFromRealRepository(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	withoutCIProviderEnvironment(t)

	mockTransport := &MockAPIClient{
		Settings: &api.SettingsResponseData{},
	}
	client := newTestOptimizationClientForTest(t, mockTransport)
	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	ciTags := environment.GetCITags()
	if ciTags[constants.GitRepositoryURL] != "https://example.com/org/repo.git" {
		t.Fatalf("repository URL tag = %q", ciTags[constants.GitRepositoryURL])
	}
	if ciTags[constants.GitCommitSHA] != repo.Commits[1] {
		t.Fatalf("commit SHA tag = %q, want %q", ciTags[constants.GitCommitSHA], repo.Commits[1])
	}
	if ciTags[constants.GitBranch] != "main" {
		t.Fatalf("branch tag = %q, want main", ciTags[constants.GitBranch])
	}
	if ciTags[constants.GitCommitMessage] != "second commit\n\nMore details" {
		t.Fatalf("commit message tag = %q", ciTags[constants.GitCommitMessage])
	}
	if ciTags[constants.GitCommitAuthorName] != gittest.AuthorName {
		t.Fatalf("author name tag = %q", ciTags[constants.GitCommitAuthorName])
	}
	if ciTags[constants.GitCommitAuthorEmail] != gittest.AuthorEmail {
		t.Fatalf("author email tag = %q", ciTags[constants.GitCommitAuthorEmail])
	}
	expectedAuthorDate := time.Unix(repo.AuthorDates[1].Unix(), 0).String()
	if ciTags[constants.GitCommitAuthorDate] != expectedAuthorDate {
		t.Fatalf("author date tag = %q, want %q", ciTags[constants.GitCommitAuthorDate], expectedAuthorDate)
	}
	if ciTags[constants.GitCommitCommitterName] != gittest.CommitterName {
		t.Fatalf("committer name tag = %q", ciTags[constants.GitCommitCommitterName])
	}
	if ciTags[constants.GitCommitCommitterEmail] != gittest.CommitterEmail {
		t.Fatalf("committer email tag = %q", ciTags[constants.GitCommitCommitterEmail])
	}
	expectedCommitterDate := time.Unix(repo.AuthorDates[1].Add(time.Second).Unix(), 0).String()
	if ciTags[constants.GitCommitCommitterDate] != expectedCommitterDate {
		t.Fatalf("committer date tag = %q, want %q", ciTags[constants.GitCommitCommitterDate], expectedCommitterDate)
	}
	if ciTags[constants.CIWorkspacePath] != repo.Path {
		t.Fatalf("workspace path tag = %q, want %q", ciTags[constants.CIWorkspacePath], repo.Path)
	}
}

func TestApplyEnvironmentOverrides(t *testing.T) {
	t.Setenv(constants.TestOptimizationFlakyRetryEnabledEnvironmentVariable, "false")
	t.Setenv(constants.TestOptimizationManagementEnabledEnvironmentVariable, "false")
	t.Setenv(constants.TestOptimizationAttemptToFixRetriesEnvironmentVariable, "7")

	settings := &api.SettingsResponseData{
		KnownTestsEnabled:       false,
		FlakyTestRetriesEnabled: true,
	}
	settings.EarlyFlakeDetection.Enabled = true
	settings.TestManagement.Enabled = true

	applyEnvironmentOverrides(settings)

	if settings.EarlyFlakeDetection.Enabled {
		t.Fatal("expected early flake detection to be disabled when known tests are disabled")
	}
	if settings.FlakyTestRetriesEnabled {
		t.Fatal("expected flaky retries env override")
	}
	if settings.TestManagement.Enabled {
		t.Fatal("expected test management env override")
	}
	if settings.TestManagement.AttemptToFixRetries != 7 {
		t.Fatalf("attempt-to-fix retries = %d, want 7", settings.TestManagement.AttemptToFixRetries)
	}
}

func TestApplyEnvironmentOverridesInvalidEnvValuesUseDefaults(t *testing.T) {
	t.Setenv(constants.TestOptimizationFlakyRetryEnabledEnvironmentVariable, "invalid")
	t.Setenv(constants.TestOptimizationManagementEnabledEnvironmentVariable, "invalid")
	t.Setenv(constants.TestOptimizationAttemptToFixRetriesEnvironmentVariable, "invalid")

	settings := &api.SettingsResponseData{
		KnownTestsEnabled:       true,
		FlakyTestRetriesEnabled: true,
	}
	settings.TestManagement.Enabled = true
	settings.TestManagement.AttemptToFixRetries = 3

	applyEnvironmentOverrides(settings)

	if !settings.FlakyTestRetriesEnabled {
		t.Fatal("expected invalid flaky retry env override to keep default true")
	}
	if !settings.TestManagement.Enabled {
		t.Fatal("expected invalid test management env override to keep default true")
	}
	if settings.TestManagement.AttemptToFixRetries != 3 {
		t.Fatalf("attempt-to-fix retries = %d, want 3", settings.TestManagement.AttemptToFixRetries)
	}
}

func TestRepositoryUploadHelpers(t *testing.T) {
	uploadErr := errors.New("upload failed")
	client := newTestOptimizationClient(nil, nil, func() (int64, error) { return 0, uploadErr }, false)
	if bytes, err := client.uploadRepositoryChanges(); bytes != 0 || !errors.Is(err, uploadErr) {
		t.Fatalf("uploadRepositoryChanges() = %d, %v", bytes, err)
	}

	client = newTestOptimizationClient(nil, nil, func() (int64, error) { return 99, nil }, false)
	if bytes, err := client.uploadRepositoryChanges(); bytes != 99 || err != nil {
		t.Fatalf("uploadRepositoryChanges() = %d, %v", bytes, err)
	}

	done := client.uploadRepositoryChangesAsync()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("uploadRepositoryChangesAsync() did not finish")
	}

	client = newTestOptimizationClient(&MockAPIClient{}, nil, nil, false)
	if bytes, err := client.sendObjectsPackFile("commit", nil, nil); bytes != 0 || err != nil {
		t.Fatalf("sendObjectsPackFile() empty = %d, %v", bytes, err)
	}
}

func TestRepositoryUploadAsyncErrorAndGitFallback(t *testing.T) {
	uploadErr := errors.New("upload failed")
	client := newTestOptimizationClient(nil, nil, func() (int64, error) { return 0, uploadErr }, false)
	done := client.uploadRepositoryChangesAsync()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("uploadRepositoryChangesAsync() with error did not finish")
	}

	client = newTestOptimizationClient(nil, nil, nil, false)
	bytes, err := client.uploadRepositoryChanges()
	if err != nil || bytes != 0 {
		t.Fatalf("expected git fallback noop without transport, got bytes=%d err=%v", bytes, err)
	}
}

func TestUploadRepositoryChangesFromGitAllCommitsKnown(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)

	localCommits := []string{repo.Commits[1], repo.Commits[0]}

	mockTransport := &MockAPIClient{RemoteCommits: localCommits}
	client := newTestOptimizationClient(mockTransport, nil, nil, false)

	bytes, err := client.uploadRepositoryChangesFromGit()
	if err != nil {
		t.Fatalf("uploadRepositoryChangesFromGit() returned error: %v", err)
	}
	if bytes != 0 {
		t.Fatalf("expected no packfile upload when all commits are known, got %d bytes", bytes)
	}
	if mockTransport.GetCommitsCalls != 1 {
		t.Fatalf("expected one search commits request, got %d", mockTransport.GetCommitsCalls)
	}
	if len(mockTransport.GetCommitsRequests) != 1 || !slices.Equal(mockTransport.GetCommitsRequests[0], localCommits) {
		t.Fatalf("GetCommits() requests = %#v, want %#v", mockTransport.GetCommitsRequests, localCommits)
	}
	if mockTransport.SendPackFilesCalls != 0 {
		t.Fatalf("expected no packfile upload when all commits are known, got %d calls", mockTransport.SendPackFilesCalls)
	}
}

func TestUploadRepositoryChangesFromGitSearchError(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)

	localCommits := []string{repo.Commits[1], repo.Commits[0]}

	searchErr := errors.New("search commits failed")
	client := newTestOptimizationClient(&MockAPIClient{GetCommitsErr: searchErr}, nil, nil, false)

	bytes, err := client.uploadRepositoryChangesFromGit()
	if bytes != 0 {
		t.Fatalf("expected no bytes on search error, got %d", bytes)
	}
	if err == nil || !strings.Contains(err.Error(), searchErr.Error()) {
		t.Fatalf("expected wrapped search error, got %v", err)
	}
	mockTransport := client.apiTransport.(*MockAPIClient)
	if len(mockTransport.GetCommitsRequests) != 1 || !slices.Equal(mockTransport.GetCommitsRequests[0], localCommits) {
		t.Fatalf("GetCommits() requests = %#v, want %#v", mockTransport.GetCommitsRequests, localCommits)
	}
}

func TestUploadRepositoryChangesFromGitUploadsMissingCommits(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)

	localCommits := []string{repo.Commits[1], repo.Commits[0]}
	remoteCommits := []string{repo.Commits[0]}
	mockTransport := &MockAPIClient{
		RemoteCommits:      remoteCommits,
		SendPackFilesBytes: 123,
	}
	client := newTestOptimizationClient(mockTransport, nil, nil, false)

	bytes, err := client.uploadRepositoryChangesFromGit()
	if err != nil {
		t.Fatalf("uploadRepositoryChangesFromGit() returned error: %v", err)
	}
	if bytes != 123 {
		t.Fatalf("bytes = %d, want mock packfile bytes", bytes)
	}
	if mockTransport.GetCommitsCalls != 1 {
		t.Fatalf("expected one search commits request, got %d", mockTransport.GetCommitsCalls)
	}
	if len(mockTransport.GetCommitsRequests) != 1 || !slices.Equal(mockTransport.GetCommitsRequests[0], localCommits) {
		t.Fatalf("GetCommits() requests = %#v, want %#v", mockTransport.GetCommitsRequests, localCommits)
	}
	if mockTransport.SendPackFilesCalls != 1 {
		t.Fatalf("expected one packfile upload, got %d", mockTransport.SendPackFilesCalls)
	}
	if mockTransport.SentCommitSha != repo.Commits[1] {
		t.Fatalf("sent commit SHA = %q, want %q", mockTransport.SentCommitSha, repo.Commits[1])
	}
	if len(mockTransport.SentPackFiles) == 0 {
		t.Fatal("expected pack files to be sent")
	}
	sentPackDirectory := filepath.Dir(mockTransport.SentPackFiles[0])
	if len(mockTransport.SentPackFileSizes) != len(mockTransport.SentPackFiles) {
		t.Fatalf("packfile sizes = %#v, packfiles = %#v", mockTransport.SentPackFileSizes, mockTransport.SentPackFiles)
	}
	for _, size := range mockTransport.SentPackFileSizes {
		if size <= 0 {
			t.Fatalf("expected non-empty packfiles, got sizes %#v", mockTransport.SentPackFileSizes)
		}
	}
	if _, err := os.Stat(sentPackDirectory); !os.IsNotExist(err) {
		t.Fatalf("expected pack directory %q to be cleaned up, got error %v", sentPackDirectory, err)
	}
}

func TestUploadRepositoryChangesFromGitWithoutTransportIsNoop(t *testing.T) {
	client := newTestOptimizationClient(nil, nil, nil, false)
	bytes, err := client.uploadRepositoryChangesFromGit()
	if err != nil || bytes != 0 {
		t.Fatalf("expected noop without transport, got bytes=%d err=%v", bytes, err)
	}
}

func TestGetSearchCommitsBranches(t *testing.T) {
	localCommits := git.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		t.Skip("no local git commits available")
	}

	client := newTestOptimizationClient(nil, nil, nil, false)
	response, err := client.getSearchCommits()
	if err != nil {
		t.Fatalf("getSearchCommits() without transport returned error: %v", err)
	}
	if response.IsOk {
		t.Fatalf("expected not-ok response without transport, got %#v", response)
	}

	searchErr := errors.New("search failed")
	mockTransport := &MockAPIClient{GetCommitsErr: searchErr}
	client = newTestOptimizationClient(mockTransport, nil, nil, false)
	response, err = client.getSearchCommits()
	if !errors.Is(err, searchErr) {
		t.Fatalf("expected search error, got response=%#v err=%v", response, err)
	}
	if response == nil || !response.IsOk || len(response.LocalCommits) == 0 {
		t.Fatalf("expected local commits in ok response, got %#v", response)
	}
}

func TestGetSearchCommitsWithoutGitRepository(t *testing.T) {
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWorkingDirectory)
	})
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}

	client := newTestOptimizationClient(&MockAPIClient{}, nil, nil, false)
	response, err := client.getSearchCommits()
	if err != nil {
		t.Fatalf("getSearchCommits() returned error: %v", err)
	}
	if response.IsOk || response.hasCommits() {
		t.Fatalf("expected no commits outside a git repository, got %#v", response)
	}
}

func TestCacheManagerAdditionalBranches(t *testing.T) {
	cleanPlanDirectory(t)
	manager := NewCacheManager()

	if err := manager.StoreTestOptimizationPlanCache(map[string]string{"key": "value"}); err != nil {
		t.Fatalf("StoreTestOptimizationPlanCache() returned error: %v", err)
	}
	var decoded map[string]string
	if err := manager.ReadTestOptimizationPlanCache(&decoded); err != nil {
		t.Fatalf("ReadTestOptimizationPlanCache() returned error: %v", err)
	}
	if decoded["key"] != "value" {
		t.Fatalf("decoded cache = %#v", decoded)
	}

	nestedDir := filepath.Join(constants.PlanDirectory, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}
	if err := manager.writeJSONToFile(map[string]string{"ok": "yes"}, filepath.Join(nestedDir, "data.json")); err != nil {
		t.Fatalf("writeJSONToFile() returned error: %v", err)
	}

	if err := manager.writeJSONBytesToFile([]byte("{"), filepath.Join(constants.PlanDirectory, "bad.json")); err != nil {
		t.Fatalf("writeJSONBytesToFile() stores raw bytes and should not validate JSON: %v", err)
	}
	var invalid map[string]string
	if err := manager.readJSONFromFile(filepath.Join(constants.PlanDirectory, "bad.json"), &invalid); err == nil {
		t.Fatal("expected invalid JSON read error")
	}

	if err := manager.readJSONFromFile(filepath.Join(constants.PlanDirectory, "missing.json"), &decoded); err == nil {
		t.Fatal("expected missing cache read error")
	}

	if err := manager.storeHTTPResponse(nil, "empty.json"); err != nil {
		t.Fatalf("storeHTTPResponse(nil) should be a noop, got %v", err)
	}
	if err := manager.writeJSONBytesToFile(nil, filepath.Join(constants.PlanDirectory, "empty.json")); err != nil {
		t.Fatalf("writeJSONBytesToFile(nil) should be a noop, got %v", err)
	}
	if err := manager.writeJSONToFile(make(chan int), filepath.Join(constants.PlanDirectory, "unsupported.json")); err == nil {
		t.Fatal("expected marshal error for unsupported JSON value")
	}
	if err := manager.StoreTestOptimizationPlanCache(make(chan int)); err == nil {
		t.Fatal("expected plan cache marshal error for unsupported JSON value")
	}
}

func TestCacheManagerWriteErrors(t *testing.T) {
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}

	manager := NewCacheManager()
	if err := manager.writeJSONToFile(map[string]string{"x": "y"}, filepath.Join(blockingFile, "data.json")); err == nil {
		t.Fatal("expected writeJSONToFile mkdir error")
	}
	if err := manager.writeJSONBytesToFile(json.RawMessage(`{}`), filepath.Join(blockingFile, "data.json")); err == nil {
		t.Fatal("expected writeJSONBytesToFile mkdir error")
	}

	originalHTTPDir := constants.HTTPCacheDir
	t.Cleanup(func() { constants.HTTPCacheDir = originalHTTPDir })
	constants.HTTPCacheDir = t.TempDir()
	if err := manager.storeHTTPResponse(json.RawMessage(`{"ok":true}`), "."); err == nil {
		t.Fatal("expected HTTP response write error when target path is a directory")
	}

	originalRunnerCacheDir := constants.RunnerCacheDir
	t.Cleanup(func() { constants.RunnerCacheDir = originalRunnerCacheDir })
	constants.RunnerCacheDir = filepath.Join(t.TempDir(), "runner-cache-file")
	if err := os.WriteFile(constants.RunnerCacheDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking runner cache file: %v", err)
	}
	if err := manager.StoreTestOptimizationPlanCache(map[string]string{"x": "y"}); err == nil {
		t.Fatal("expected runner cache directory creation error")
	}

	constants.RunnerCacheDir = t.TempDir()
	if err := os.Mkdir(filepath.Join(constants.RunnerCacheDir, constants.TestOptimizationPlanCacheFile), 0o755); err != nil {
		t.Fatalf("create blocking plan cache directory: %v", err)
	}
	if err := manager.StoreTestOptimizationPlanCache(map[string]string{"x": "y"}); err == nil {
		t.Fatal("expected plan cache write error when target path is a directory")
	}
}

func TestSearchCommitsResponseHelpers(t *testing.T) {
	response := newSearchCommitsResponse([]string{"a", "b", "c"}, []string{"b"}, true)
	if !response.IsOk || !response.hasCommits() {
		t.Fatalf("unexpected response state: %#v", response)
	}
	missing := response.missingCommits()
	if len(missing) != 2 || missing[0] != "a" || missing[1] != "c" {
		t.Fatalf("missingCommits() = %#v", missing)
	}

	empty := newSearchCommitsResponse(nil, nil, false)
	if empty.hasCommits() {
		t.Fatal("empty response should not have commits")
	}
}

func TestExitTestOptimizationRunsCloseActionsOnce(t *testing.T) {
	var calls []string
	client := newTestOptimizationClient(nil, nil, nil, false)
	client.pushTestOptimizationCloseAction(func() { calls = append(calls, "first") })
	client.pushTestOptimizationCloseAction(func() { calls = append(calls, "second") })

	client.exitTestOptimization()

	if len(calls) != 2 || calls[0] != "second" || calls[1] != "first" {
		t.Fatalf("close actions executed in unexpected order: %#v", calls)
	}
	if len(client.closeActions) != 0 {
		t.Fatalf("expected close actions to be cleared, got %d", len(client.closeActions))
	}

	client.exitTestOptimization()
	if len(calls) != 2 {
		t.Fatalf("close actions executed again: %#v", calls)
	}
}

func TestStoreCacheAndExitLogsCacheWriteErrors(t *testing.T) {
	cleanPlanDirectory(t)
	originalHTTPDir := constants.HTTPCacheDir
	t.Cleanup(func() { constants.HTTPCacheDir = originalHTTPDir })

	constants.HTTPCacheDir = filepath.Join(t.TempDir(), "http-cache-file")
	if err := os.WriteFile(constants.HTTPCacheDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking HTTP cache file: %v", err)
	}

	mockTransport := &MockAPIClient{
		Settings:                       &api.SettingsResponseData{},
		SettingsRawResponse:            json.RawMessage(`{"settings":true}`),
		KnownTestsRawResponse:          json.RawMessage(`{"known":true}`),
		TestManagementTestsRawResponse: json.RawMessage(`{"managed":true}`),
	}
	client := newTestOptimizationClientForTest(t, mockTransport)
	client.StoreCacheAndExit()

	if mockTransport.SettingsCalls != 1 {
		t.Fatalf("expected settings request, got %d", mockTransport.SettingsCalls)
	}
}

func TestGetDisabledTestsFromNilTestManagementData(t *testing.T) {
	settings := &api.SettingsResponseData{}
	settings.TestManagement.Enabled = true
	client := newTestOptimizationClientForTest(t, &MockAPIClient{
		Settings: settings,
	})
	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	disabled := client.GetDisabledTests()
	if len(disabled) != 0 {
		t.Fatalf("expected no disabled tests for nil data, got %#v", disabled)
	}
}
