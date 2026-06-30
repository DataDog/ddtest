package testoptimization

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

// TestMain runs once for the entire package and handles global setup/teardown
func TestMain(m *testing.M) {
	code := m.Run()

	// remove any .testoptimization directories that might be left behind
	_ = os.RemoveAll(constants.PlanDirectory)

	// Exit with the same code as the tests
	os.Exit(code)
}

type MockAPIClient struct {
	Settings                       *api.SettingsResponseData
	SettingsErr                    error
	SettingsRawResponse            json.RawMessage
	SettingsCalls                  int
	Skippables                     api.Skippables
	SkippableCorrelationID         string
	SkippableErr                   error
	SkippableTestsRawResponse      json.RawMessage
	SkippableTestsCalls            int
	KnownTests                     *api.KnownTestsResponseData
	KnownTestsErr                  error
	KnownTestsRawResponse          json.RawMessage
	KnownTestsCalls                int
	TestManagementTestsData        *api.TestManagementTestsResponseDataModules
	TestManagementTestsErr         error
	TestManagementTestsRawResponse json.RawMessage
	TestManagementTestsCalls       int
	TestSuiteDurations             map[string]map[string]api.TestSuiteDurationInfo
	TestSuiteDurationsCalls        int
	RemoteCommits                  []string
	GetCommitsRequests             [][]string
	GetCommitsErr                  error
	GetCommitsCalls                int
	SentCommitSha                  string
	SentPackFiles                  []string
	SentPackFileSizes              []int64
	SendPackFilesBytes             int64
	SendPackFilesErr               error
	SendPackFilesCalls             int
}

func (m *MockAPIClient) GetSettings() (*api.SettingsResponseData, error) {
	m.SettingsCalls++
	return m.Settings, m.SettingsErr
}

func (m *MockAPIClient) GetSettingsRawResponse() json.RawMessage {
	return m.SettingsRawResponse
}

func (m *MockAPIClient) GetSkippableTests() (string, api.Skippables, error) {
	m.SkippableTestsCalls++
	skippables := m.Skippables
	if skippables.Tests == nil {
		skippables.Tests = api.SkippableTests{}
	}
	if skippables.Suites == nil {
		skippables.Suites = api.SkippableSuites{}
	}
	return m.SkippableCorrelationID, skippables, m.SkippableErr
}

func (m *MockAPIClient) GetSkippableTestsRawResponse() json.RawMessage {
	return m.SkippableTestsRawResponse
}

func (m *MockAPIClient) GetKnownTests() (*api.KnownTestsResponseData, error) {
	m.KnownTestsCalls++
	return m.KnownTests, m.KnownTestsErr
}

func (m *MockAPIClient) GetTestSuiteDurations() *api.TestSuiteDurationsResponseData {
	m.TestSuiteDurationsCalls++
	return &api.TestSuiteDurationsResponseData{TestSuites: m.TestSuiteDurations}
}

func (m *MockAPIClient) GetKnownTestsRawResponse() json.RawMessage {
	return m.KnownTestsRawResponse
}

func (m *MockAPIClient) GetTestManagementTests() (*api.TestManagementTestsResponseDataModules, error) {
	m.TestManagementTestsCalls++
	return m.TestManagementTestsData, m.TestManagementTestsErr
}

func (m *MockAPIClient) GetTestManagementTestsRawResponse() json.RawMessage {
	return m.TestManagementTestsRawResponse
}

func (m *MockAPIClient) GetCommits(localCommits []string) ([]string, error) {
	m.GetCommitsCalls++
	m.GetCommitsRequests = append(m.GetCommitsRequests, slices.Clone(localCommits))
	return m.RemoteCommits, m.GetCommitsErr
}

func (m *MockAPIClient) SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error) {
	m.SendPackFilesCalls++
	m.SentCommitSha = commitSha
	m.SentPackFiles = append([]string(nil), packFiles...)
	m.SentPackFileSizes = m.SentPackFileSizes[:0]
	for _, packFile := range packFiles {
		info, err := os.Stat(packFile)
		if err != nil {
			m.SentPackFileSizes = append(m.SentPackFileSizes, -1)
			continue
		}
		m.SentPackFileSizes = append(m.SentPackFileSizes, info.Size())
	}
	return m.SendPackFilesBytes, m.SendPackFilesErr
}

func cleanPlanDirectory(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(constants.PlanDirectory); err != nil {
		t.Fatalf("Failed to remove plan directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(constants.PlanDirectory)
	})
}

func newTestOptimizationClientForTest(t *testing.T, apiTransport api.Transport) *TestOptimizationClient {
	t.Helper()
	environment.ResetCITags()
	t.Cleanup(environment.ResetCITags)
	return NewTestOptimizationClientWithDependencies(apiTransport)
}

func withoutCIProviderEnvironment(t *testing.T) {
	t.Helper()

	envKeys := []string{
		"APPVEYOR",
		"TF_BUILD",
		"BITBUCKET_COMMIT",
		"BUDDY",
		"BUILDKITE",
		"CIRCLECI",
		"GITHUB_SHA",
		"GITLAB_CI",
		"JENKINS_URL",
		"TEAMCITY_VERSION",
		"TRAVIS",
		"BITRISE_BUILD_SLUG",
		"CF_BUILD_ID",
		"CODEBUILD_INITIATOR",
		"DD_GIT_BRANCH",
		"DD_GIT_TAG",
		"DD_GIT_REPOSITORY_URL",
		"DD_GIT_COMMIT_SHA",
		"DD_GIT_COMMIT_MESSAGE",
		"DD_GIT_COMMIT_AUTHOR_NAME",
		"DD_GIT_COMMIT_AUTHOR_EMAIL",
		"DD_GIT_COMMIT_AUTHOR_DATE",
		"DD_GIT_COMMIT_COMMITTER_NAME",
		"DD_GIT_COMMIT_COMMITTER_EMAIL",
		"DD_GIT_COMMIT_COMMITTER_DATE",
	}

	originalValues := make(map[string]string, len(envKeys))
	originallySet := make(map[string]bool, len(envKeys))
	for _, key := range envKeys {
		if value, ok := os.LookupEnv(key); ok {
			originalValues[key] = value
			originallySet[key] = true
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("failed to unset %s: %v", key, err)
		}
	}
	environment.ResetCITags()

	t.Cleanup(func() {
		for _, key := range envKeys {
			if originallySet[key] {
				_ = os.Setenv(key, originalValues[key])
			} else {
				_ = os.Unsetenv(key)
			}
		}
		environment.ResetCITags()
	})
}

func assertFileDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Expected file to not exist at %s, got error: %v", path, err)
	}
}

func assertJSONFile(t *testing.T, path string, expected json.RawMessage) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read JSON file at %s: %v", path, err)
	}

	if string(data) != string(expected) {
		t.Fatalf("JSON file mismatch at %s\nexpected: %s\nactual:   %s", path, expected, data)
	}
}

func TestNewTestOptimizationClient(t *testing.T) {
	client := NewTestOptimizationClient()

	if client == nil {
		t.Error("NewTestOptimizationClient() should return non-nil client")
	}
}

func TestNewTestOptimizationClientWithDependencies(t *testing.T) {
	mockAPIClient := &MockAPIClient{}

	client := newTestOptimizationClientForTest(t, mockAPIClient)

	if client == nil {
		t.Error("NewTestOptimizationClientWithDependencies() should return non-nil client")
	}
}

func TestTestOptimizationClient_GetTestSuiteDurations(t *testing.T) {
	durations := map[string]map[string]api.TestSuiteDurationInfo{
		"rspec": {
			"Suite": {
				SourceFile: "spec/suite_spec.rb",
				Duration:   api.DurationPercentiles{P50: "42000000"},
			},
		},
	}
	mockAPIClient := &MockAPIClient{TestSuiteDurations: durations}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	result := client.GetTestSuiteDurations()

	if mockAPIClient.TestSuiteDurationsCalls != 1 {
		t.Fatalf("GetTestSuiteDurations() should fetch durations once, got %d calls", mockAPIClient.TestSuiteDurationsCalls)
	}
	if result.TestSuites["rspec"]["Suite"].SourceFile != "spec/suite_spec.rb" {
		t.Fatalf("GetTestSuiteDurations() returned %#v, want %#v", result, durations)
	}
	if client.OperationDurations().TestSuiteDurations <= 0 {
		t.Fatal("GetTestSuiteDurations() should record operation duration")
	}
}

func TestTestOptimizationClient_Initialize(t *testing.T) {
	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	tags := map[string]string{
		"test.key1": "value1",
		"test.key2": "value2",
	}

	startTime := time.Now()
	err := client.Initialize(tags)
	duration := time.Since(startTime)

	if err != nil {
		t.Errorf("Initialize() should not return error, got: %v", err)
	}

	if mockAPIClient.SettingsCalls != 1 {
		t.Errorf("Initialize() should fetch settings once, got %d calls", mockAPIClient.SettingsCalls)
	}

	ciTags := environment.GetCITags()
	for key, expectedValue := range tags {
		if actualValue, exists := ciTags[key]; !exists {
			t.Errorf("Expected tag %s to be added", key)
		} else if actualValue != expectedValue {
			t.Errorf("Expected tag %s to have value %s, got %s", key, expectedValue, actualValue)
		}
	}

	if duration < 0 {
		t.Error("Initialize() duration should be non-negative")
	}
	if client.OperationDurations().Settings <= 0 {
		t.Error("Initialize() should record settings fetch duration")
	}
}

func TestTestOptimizationClient_GetSkippables_NilResponse(t *testing.T) {
	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    false,
			TestsSkipping: false,
		},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	// Initialize the client to set up settings
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippables().Tests

	if result == nil {
		t.Error("GetSkippables().Tests should return non-nil map")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippables().Tests should return empty map when ITR is disabled, got %d items", len(result))
	}
}

func TestTestOptimizationClient_GetSkippables(t *testing.T) {
	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		Skippables: api.Skippables{
			Tests: api.SkippableTests{
				"module1.TestSuite1.test_method_1.param1": true,
				"module1.TestSuite1.test_method_2.param2": true,
				"module2.TestSuite2.test_method_3.param3": true,
			},
		},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	// Initialize the client to set up settings
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippables().Tests

	if result == nil {
		t.Error("GetSkippables().Tests should return non-nil map")
	}

	// Verify expected test FQNs are present
	expectedTests := []string{
		"module1.TestSuite1.test_method_1.param1",
		"module1.TestSuite1.test_method_2.param2",
		"module2.TestSuite2.test_method_3.param3",
	}

	for _, expectedTest := range expectedTests {
		if !result[expectedTest] {
			t.Errorf("Expected test %s to be marked as skippable", expectedTest)
		}
	}

	if len(result) != len(expectedTests) {
		t.Errorf("Expected %d skippable tests, got %d", len(expectedTests), len(result))
	}
	if client.OperationDurations().Skippables <= 0 {
		t.Error("GetSkippables() should record skippables fetch duration")
	}
}

func TestTestOptimizationClient_StoreCacheAndExit(t *testing.T) {
	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	client.StoreCacheAndExit()

	if mockAPIClient.SettingsCalls != 1 {
		t.Errorf("StoreCacheAndExit() should fetch settings once, got %d calls", mockAPIClient.SettingsCalls)
	}
}

func TestTestOptimizationClient_StoreCacheAndExit_WritesHTTPCaches(t *testing.T) {
	cleanPlanDirectory(t)

	settingsResponse := json.RawMessage(`{"data":{"id":"settings","type":"ci_app_test_service_settings","attributes":{"itr_enabled":true}}}`)
	knownTestsResponse := json.RawMessage(`{"data":{"id":"known-tests","type":"ci_app_libraries_tests","attributes":{"tests":{}}}}`)
	testManagementResponse := json.RawMessage(`{"data":{"id":"test-management","type":"test_management_tests","attributes":{"modules":{}}}}`)

	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		SettingsRawResponse: settingsResponse,
		KnownTests: &api.KnownTestsResponseData{
			Tests: api.KnownTestsResponseDataModules{},
		},
		KnownTestsRawResponse: knownTestsResponse,
		TestManagementTestsData: &api.TestManagementTestsResponseDataModules{
			Modules: map[string]api.TestManagementTestsResponseDataSuites{},
		},
		TestManagementTestsRawResponse: testManagementResponse,
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	client.StoreCacheAndExit()

	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "settings.json"), settingsResponse)
	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "known_tests.json"), knownTestsResponse)
	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "test_management.json"), testManagementResponse)
}

func TestTestOptimizationClient_StoreCacheAndExit_SkipsHTTPCacheWithoutResponse(t *testing.T) {
	cleanPlanDirectory(t)

	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	client.StoreCacheAndExit()

	assertFileDoesNotExist(t, constants.HTTPCacheDir)
}

func TestTest_DatadogTestId(t *testing.T) {
	testCases := []struct {
		module     string
		suite      string
		test       string
		parameters string
		expected   string
	}{
		{"module", "TestSuite", "testMethod", "param1", "module.TestSuite.testMethod.param1"},
		{"module", "com.example.TestClass", "test_with_underscores", "param=value", "module.com.example.TestClass.test_with_underscores.param=value"},
		{"", "", "test", "", "..test."},
		{"module", "suite", "", "params", "module.suite..params"},
	}

	for _, tc := range testCases {
		test := Test{
			Module:     tc.module,
			Suite:      tc.suite,
			Name:       tc.test,
			Parameters: tc.parameters,
		}
		result := test.DatadogTestId()
		if result != tc.expected {
			t.Errorf("Test{Module: %q, Suite: %q, Name: %q, Parameters: %q}.DatadogTestId() = %q, expected %q",
				tc.module, tc.suite, tc.test, tc.parameters, result, tc.expected)
		}
	}
}

func TestTest_FQN(t *testing.T) {
	testCases := []struct {
		module   string
		suite    string
		test     string
		expected string
	}{
		{"module", "TestSuite", "testMethod", "module.TestSuite.testMethod"},
		{"module", "com.example.TestClass", "test_with_underscores", "module.com.example.TestClass.test_with_underscores"},
		{"", "", "test", "..test"},
		{"module", "suite", "", "module.suite."},
	}

	for _, tc := range testCases {
		test := Test{
			Module: tc.module,
			Suite:  tc.suite,
			Name:   tc.test,
		}
		result := test.FQN()
		if result != tc.expected {
			t.Errorf("Test{Module: %q, Suite: %q, Name: %q}.FQN() = %q, expected %q",
				tc.module, tc.suite, tc.test, result, tc.expected)
		}
	}
}

func TestTestOptimizationClient_GetDisabledTests(t *testing.T) {
	settings := &api.SettingsResponseData{}
	settings.TestManagement.Enabled = true
	mockAPIClient := &MockAPIClient{
		Settings: settings,
		TestManagementTestsData: &api.TestManagementTestsResponseDataModules{
			Modules: map[string]api.TestManagementTestsResponseDataSuites{
				"module-a": {
					Suites: map[string]api.TestManagementTestsResponseDataTests{
						"suite-a": {
							Tests: map[string]api.TestManagementTestsResponseDataTestProperties{
								"disabled":                {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
								"disabled attempt-to-fix": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true, AttemptToFix: true}},
								"quarantined":             {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
							},
						},
					},
				},
				"module-b": {
					Suites: map[string]api.TestManagementTestsResponseDataTests{
						"suite-b": {
							Tests: map[string]api.TestManagementTestsResponseDataTestProperties{
								"also disabled": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
					},
				},
			},
		},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)
	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	disabledTests := client.GetDisabledTests()

	expected := map[string]bool{
		"module-a.suite-a.disabled":      true,
		"module-b.suite-b.also disabled": true,
	}
	if !maps.Equal(disabledTests, expected) {
		t.Errorf("GetDisabledTests() = %v, expected %v", disabledTests, expected)
	}
	if mockAPIClient.TestManagementTestsCalls != 1 {
		t.Fatalf("expected test management tests to be fetched once, got %d", mockAPIClient.TestManagementTestsCalls)
	}
	if client.OperationDurations().TestManagementTests <= 0 {
		t.Fatal("GetDisabledTests() should record test management fetch duration")
	}
}

func TestTestOptimizationClient_GetDisabledTestsDisabled(t *testing.T) {
	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{},
		TestManagementTestsData: &api.TestManagementTestsResponseDataModules{
			Modules: map[string]api.TestManagementTestsResponseDataSuites{
				"module": {
					Suites: map[string]api.TestManagementTestsResponseDataTests{
						"suite": {
							Tests: map[string]api.TestManagementTestsResponseDataTestProperties{
								"disabled": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
					},
				},
			},
		},
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)
	if err := client.Initialize(map[string]string{}); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	disabledTests := client.GetDisabledTests()
	if len(disabledTests) != 0 {
		t.Fatalf("expected no disabled tests when test management is disabled, got %#v", disabledTests)
	}
	if mockAPIClient.TestManagementTestsCalls != 0 {
		t.Fatalf("expected test management tests not to be fetched, got %d calls", mockAPIClient.TestManagementTestsCalls)
	}
}

func TestTestOptimizationClient_GetSkippables_EmptyData(t *testing.T) {
	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		Skippables: api.NewSkippables(),
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	// Initialize the client to set up settings
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippables().Tests

	if result == nil {
		t.Error("GetSkippables().Tests should return non-nil map even with empty data")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippables().Tests should return empty map with empty data, got %d items", len(result))
	}
}

func TestTestOptimizationClient_GetSkippables_WritesHTTPCache(t *testing.T) {
	cleanPlanDirectory(t)

	skippableTestsResponse := json.RawMessage(`{"data":[{"id":"test-id","type":"test","attributes":{"suite":"TestSuite1","name":"test_method_1"}}]}`)

	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		Skippables: api.Skippables{
			Tests: api.SkippableTests{
				"module1.TestSuite1.test_method_1.param1": true,
			},
		},
		SkippableTestsRawResponse: skippableTestsResponse,
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippables().Tests

	if len(result) != 1 {
		t.Fatalf("Expected 1 skippable test, got %d", len(result))
	}
	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "skippable_tests.json"), skippableTestsResponse)
}

func TestTestOptimizationClient_StoreCacheAndExit_NilTestManagementTests(t *testing.T) {
	cleanPlanDirectory(t)

	mockAPIClient := &MockAPIClient{
		Settings: &api.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		TestManagementTestsData: nil,
	}
	client := newTestOptimizationClientForTest(t, mockAPIClient)

	client.StoreCacheAndExit()

	if mockAPIClient.SettingsCalls != 1 {
		t.Errorf("StoreCacheAndExit() should fetch settings once, got %d calls", mockAPIClient.SettingsCalls)
	}
}
