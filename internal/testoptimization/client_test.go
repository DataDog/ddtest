package testoptimization

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/civisibility/utils/net"
	"github.com/DataDog/ddtest/internal/constants"
)

// TestMain runs once for the entire package and handles global setup/teardown
func TestMain(m *testing.M) {
	code := m.Run()

	// remove any .testoptimization directories that might be left behind
	_ = os.RemoveAll(constants.PlanDirectory)

	// Exit with the same code as the tests
	os.Exit(code)
}

// Mock implementations for testing
type MockCIVisibilityIntegrations struct {
	InitializationCalled           bool
	ShutdownCalled                 bool
	Settings                       *net.SettingsResponseData
	SettingsRawResponse            json.RawMessage
	SkippableTests                 net.SkippableTests
	SkippableTestsRawResponse      json.RawMessage
	KnownTests                     *net.KnownTestsResponseData
	KnownTestsRawResponse          json.RawMessage
	TestManagementTestsData        *net.TestManagementTestsResponseDataModules
	TestManagementTestsRawResponse json.RawMessage
}

func (m *MockCIVisibilityIntegrations) EnsureCiVisibilityInitialization() {
	m.InitializationCalled = true
}

func (m *MockCIVisibilityIntegrations) ExitCiVisibility() {
	m.ShutdownCalled = true
}

func (m *MockCIVisibilityIntegrations) GetSettings() *net.SettingsResponseData {
	return m.Settings
}

func (m *MockCIVisibilityIntegrations) GetSettingsRawResponse() json.RawMessage {
	return m.SettingsRawResponse
}

func (m *MockCIVisibilityIntegrations) GetSkippableTests() net.SkippableTests {
	return m.SkippableTests
}

func (m *MockCIVisibilityIntegrations) GetSkippableTestsRawResponse() json.RawMessage {
	return m.SkippableTestsRawResponse
}

func (m *MockCIVisibilityIntegrations) GetKnownTests() *net.KnownTestsResponseData {
	return m.KnownTests
}

func (m *MockCIVisibilityIntegrations) GetKnownTestsRawResponse() json.RawMessage {
	return m.KnownTestsRawResponse
}

func (m *MockCIVisibilityIntegrations) GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	return m.TestManagementTestsData
}

func (m *MockCIVisibilityIntegrations) GetTestManagementTestsRawResponse() json.RawMessage {
	return m.TestManagementTestsRawResponse
}

type MockUtils struct {
	AddedTags map[string]string
}

func (m *MockUtils) AddCITagsMap(tags map[string]string) {
	if m.AddedTags == nil {
		m.AddedTags = make(map[string]string)
	}
	maps.Copy(m.AddedTags, tags)
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

func TestNewDatadogClient(t *testing.T) {
	client := NewDatadogClient()

	if client == nil {
		t.Error("NewDatadogClient() should return non-nil client")
	}
}

func TestNewDatadogClientWithDependencies(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{}
	mockUtils := &MockUtils{}

	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	if client == nil {
		t.Error("NewDatadogClientWithDependencies() should return non-nil client")
	}
}

func TestDatadogClient_Initialize(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

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

	if !mockIntegrations.InitializationCalled {
		t.Error("Initialize() should call EnsureCiVisibilityInitialization")
	}

	if mockUtils.AddedTags == nil {
		t.Error("Initialize() should call AddCITagsMap")
	} else {
		for key, expectedValue := range tags {
			if actualValue, exists := mockUtils.AddedTags[key]; !exists {
				t.Errorf("Expected tag %s to be added", key)
			} else if actualValue != expectedValue {
				t.Errorf("Expected tag %s to have value %s, got %s", key, expectedValue, actualValue)
			}
		}
	}

	if duration < 0 {
		t.Error("Initialize() duration should be non-negative")
	}
}

func TestDatadogClient_GetSkippableTests_NilResponse(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    false,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// Initialize the client to set up settings
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippableTests() should return empty map when ITR is disabled, got %d items", len(result))
	}
}

func TestDatadogClient_GetSkippableTests(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		SkippableTests: net.SkippableTests{
			"module1.TestSuite1.test_method_1.param1": true,
			"module1.TestSuite1.test_method_2.param2": true,
			"module2.TestSuite2.test_method_3.param3": true,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// Initialize the client to set up settings
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map")
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
}

func TestDatadogClient_StoreCacheAndExit(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	client.StoreCacheAndExit()

	if !mockIntegrations.ShutdownCalled {
		t.Error("Shutdown() should call ExitCiVisibility")
	}
}

func TestDatadogClient_StoreCacheAndExit_WritesHTTPCaches(t *testing.T) {
	cleanPlanDirectory(t)

	settingsResponse := json.RawMessage(`{"data":{"id":"settings","type":"ci_app_test_service_settings","attributes":{"itr_enabled":true}}}`)
	knownTestsResponse := json.RawMessage(`{"data":{"id":"known-tests","type":"ci_app_libraries_tests","attributes":{"tests":{}}}}`)
	testManagementResponse := json.RawMessage(`{"data":{"id":"test-management","type":"test_management_tests","attributes":{"modules":{}}}}`)

	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		SettingsRawResponse: settingsResponse,
		KnownTests: &net.KnownTestsResponseData{
			Tests: net.KnownTestsResponseDataModules{},
		},
		KnownTestsRawResponse: knownTestsResponse,
		TestManagementTestsData: &net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{},
		},
		TestManagementTestsRawResponse: testManagementResponse,
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	client.StoreCacheAndExit()

	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "settings.json"), settingsResponse)
	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "known_tests.json"), knownTestsResponse)
	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "test_management.json"), testManagementResponse)
}

func TestDatadogClient_StoreCacheAndExit_SkipsHTTPCacheWithoutResponse(t *testing.T) {
	cleanPlanDirectory(t)

	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	client.StoreCacheAndExit()

	assertFileDoesNotExist(t, constants.HTTPCacheDir)
}

func TestTest_FQN(t *testing.T) {
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
		result := test.FQN()
		if result != tc.expected {
			t.Errorf("Test{Module: %q, Suite: %q, Name: %q, Parameters: %q}.FQN() = %q, expected %q",
				tc.module, tc.suite, tc.test, tc.parameters, result, tc.expected)
		}
	}
}

func TestDisabledTestsFromTestManagementData(t *testing.T) {
	disabledTests := DisabledTestsFromTestManagementData(&net.TestManagementTestsResponseDataModules{
		Modules: map[string]net.TestManagementTestsResponseDataSuites{
			"module-a": {
				Suites: map[string]net.TestManagementTestsResponseDataTests{
					"suite-a": {
						Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
							"disabled":    {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							"quarantined": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
						},
					},
				},
			},
			"module-b": {
				Suites: map[string]net.TestManagementTestsResponseDataTests{
					"suite-b": {
						Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
							"also disabled": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
						},
					},
				},
			},
		},
	})

	expected := map[string]bool{
		"module-a.suite-a.disabled.":      true,
		"module-b.suite-b.also disabled.": true,
	}
	if !maps.Equal(disabledTests, expected) {
		t.Errorf("DisabledTestsFromTestManagementData() = %v, expected %v", disabledTests, expected)
	}
}

func TestDatadogClient_GetSkippableTests_EmptyData(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		SkippableTests: net.SkippableTests{},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// Initialize the client to set up settings
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map even with empty data")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippableTests() should return empty map with empty data, got %d items", len(result))
	}
}

func TestDatadogClient_GetSkippableTests_WritesHTTPCache(t *testing.T) {
	cleanPlanDirectory(t)

	skippableTestsResponse := json.RawMessage(`{"data":[{"id":"test-id","type":"test","attributes":{"suite":"TestSuite1","name":"test_method_1"}}]}`)

	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		SkippableTests: net.SkippableTests{
			"module1.TestSuite1.test_method_1.param1": true,
		},
		SkippableTestsRawResponse: skippableTestsResponse,
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	result := client.GetSkippableTests()

	if len(result) != 1 {
		t.Fatalf("Expected 1 skippable test, got %d", len(result))
	}
	assertJSONFile(t, filepath.Join(constants.HTTPCacheDir, "skippable_tests.json"), skippableTestsResponse)
}

func TestDatadogClient_StoreCacheAndExit_NilTestManagementTests(t *testing.T) {
	cleanPlanDirectory(t)

	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		TestManagementTestsData: nil,
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	client.StoreCacheAndExit()

	if !mockIntegrations.ShutdownCalled {
		t.Error("StoreCacheAndExit should still call ExitCiVisibility even with nil test management tests")
	}
}
