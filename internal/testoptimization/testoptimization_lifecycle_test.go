package testoptimization

import (
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	testoptimizationstate "github.com/DataDog/ddtest/civisibility"
	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
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
		SkippableTests: api.SkippableTests{
			"module.suite.test.": true,
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
	if skippable := client.GetSkippableTests(); len(skippable) != 1 || !skippable["module.suite.test."] {
		t.Fatalf("expected skippable tests, got %#v", skippable)
	}
	if managed := client.GetTestManagementTestsData(); managed == nil || len(managed.Modules) != 1 {
		t.Fatalf("expected test management data, got %#v", managed)
	}
	if mockTransport.KnownTestsCalls != 1 || mockTransport.SkippableTestsCalls != 1 || mockTransport.TestManagementTestsCalls != 1 {
		t.Fatalf("expected each feature endpoint once, got known=%d skippable=%d testManagement=%d",
			mockTransport.KnownTestsCalls, mockTransport.SkippableTestsCalls, mockTransport.TestManagementTestsCalls)
	}

	ciTags := utils.GetCITags()
	if ciTags[ciConstants.ItrCorrelationIDTag] != "correlation-id" {
		t.Fatalf("expected correlation id tag, got %#v", ciTags)
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
	if skippable := client.GetSkippableTests(); len(skippable) != 0 {
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
	client := newTestOptimizationClient(nil, func(string) api.Transport { return nil }, nil, false)
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
	t.Setenv(ciConstants.TestOptimizationEnabledEnvironmentVariable, "")
	t.Setenv("DD_TRACE_SAMPLE_RATE", "")
	utils.ResetCITags()
	t.Cleanup(utils.ResetCITags)
	t.Cleanup(func() {
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		testoptimizationstate.SetState(testoptimizationstate.StateExited)
	})

	client := newTestOptimizationClient(&MockAPIClient{}, nil, func() (int64, error) { return 0, nil }, true)
	client.ensureTestOptimizationSessionInitialized()

	if got := os.Getenv(ciConstants.TestOptimizationEnabledEnvironmentVariable); got != "1" {
		t.Fatalf("%s = %q, want 1", ciConstants.TestOptimizationEnabledEnvironmentVariable, got)
	}
	if got := os.Getenv("DD_TRACE_SAMPLE_RATE"); got != "1" {
		t.Fatalf("DD_TRACE_SAMPLE_RATE = %q, want 1", got)
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

func TestApplyEnvironmentOverrides(t *testing.T) {
	t.Setenv(ciConstants.TestOptimizationFlakyRetryEnabledEnvironmentVariable, "false")
	t.Setenv(ciConstants.TestOptimizationManagementEnabledEnvironmentVariable, "false")
	t.Setenv(ciConstants.TestOptimizationAttemptToFixRetriesEnvironmentVariable, "7")
	t.Setenv(ciConstants.TestOptimizationSubtestFeaturesEnabledEnvironmentVariable, "false")

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
	if settings.SubtestFeaturesEnabled {
		t.Fatal("expected subtest features env override")
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
	localCommits := utils.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		t.Skip("no local git commits available")
	}

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
}

func TestUploadRepositoryChangesFromGitSearchError(t *testing.T) {
	localCommits := utils.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		t.Skip("no local git commits available")
	}

	searchErr := errors.New("search commits failed")
	client := newTestOptimizationClient(&MockAPIClient{GetCommitsErr: searchErr}, nil, nil, false)

	bytes, err := client.uploadRepositoryChangesFromGit()
	if bytes != 0 {
		t.Fatalf("expected no bytes on search error, got %d", bytes)
	}
	if err == nil || !strings.Contains(err.Error(), searchErr.Error()) {
		t.Fatalf("expected wrapped search error, got %v", err)
	}
}

func TestUploadRepositoryChangesFromGitUploadsMissingCommits(t *testing.T) {
	localCommits := utils.GetLastLocalGitCommitShas()
	if len(localCommits) < 1 {
		t.Skip("no local git commits available")
	}

	remoteCommits := append([]string(nil), localCommits[1:]...)
	mockTransport := &MockAPIClient{
		RemoteCommits:      remoteCommits,
		SendPackFilesBytes: 123,
	}
	client := newTestOptimizationClient(mockTransport, nil, nil, false)

	bytes, err := client.uploadRepositoryChangesFromGit()
	if err != nil {
		t.Fatalf("uploadRepositoryChangesFromGit() returned error: %v", err)
	}
	if mockTransport.SendPackFilesCalls > 0 && bytes != 123 {
		t.Fatalf("bytes = %d, want mock packfile bytes", bytes)
	}
	if mockTransport.GetCommitsCalls == 0 {
		t.Fatal("expected search commits request")
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
	localCommits := utils.GetLastLocalGitCommitShas()
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
	if err := os.Mkdir(filepath.Join(constants.RunnerCacheDir, TestOptimizationPlanCacheFile), 0o755); err != nil {
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

func TestExitTestOptimizationRunsCloseActionsOnlyWhenInitialized(t *testing.T) {
	testoptimizationstate.SetState(testoptimizationstate.StateInitialized)
	t.Cleanup(func() { testoptimizationstate.SetState(testoptimizationstate.StateExited) })

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
	if state := testoptimizationstate.GetState(); state != testoptimizationstate.StateExited {
		t.Fatalf("state = %v, want exited", state)
	}

	client.exitTestOptimization()
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

func TestDisabledTestsFromNilTestManagementData(t *testing.T) {
	disabled := DisabledTestsFromTestManagementData(nil)
	if len(disabled) != 0 {
		t.Fatalf("expected no disabled tests for nil data, got %#v", disabled)
	}
}
