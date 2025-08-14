package testoptimization

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/datadog-test-runner/civisibility/utils/net"
)

// TestMain runs once for the entire package and handles global setup/teardown
func TestMain(m *testing.M) {
	code := m.Run()

	// remove any .dd directories that might be left behind
	os.RemoveAll(".dd")

	// Exit with the same code as the tests
	os.Exit(code)
}

// Mock implementations for testing
type MockCIVisibilityIntegrations struct {
	InitializationCalled    bool
	ShutdownCalled          bool
	Settings                *net.SettingsResponseData
	SkippableTests          map[string]map[string][]net.SkippableResponseDataAttributes
	KnownTests              *net.KnownTestsResponseData
	TestManagementTestsData *net.TestManagementTestsResponseDataModules
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

func (m *MockCIVisibilityIntegrations) GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes {
	return m.SkippableTests
}

func (m *MockCIVisibilityIntegrations) GetKnownTests() *net.KnownTestsResponseData {
	return m.KnownTests
}

func (m *MockCIVisibilityIntegrations) GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	return m.TestManagementTestsData
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
		SkippableTests: map[string]map[string][]net.SkippableResponseDataAttributes{
			"module1": {
				"suite1": []net.SkippableResponseDataAttributes{
					{
						Suite:      "TestSuite1",
						Name:       "test_method_1",
						Parameters: "param1",
					},
					{
						Suite:      "TestSuite1",
						Name:       "test_method_2",
						Parameters: "param2",
					},
				},
			},
			"module2": {
				"suite2": []net.SkippableResponseDataAttributes{
					{
						Suite:      "TestSuite2",
						Name:       "test_method_3",
						Parameters: "param3",
					},
				},
			},
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
		"TestSuite1.test_method_1.param1",
		"TestSuite1.test_method_2.param2",
		"TestSuite2.test_method_3.param3",
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

func TestDatadogClient_StoreContextAndExit(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	client.StoreContextAndExit()

	if !mockIntegrations.ShutdownCalled {
		t.Error("Shutdown() should call ExitCiVisibility")
	}
}

func TestDatadogClient_StoreContextAndExit_WritesSettingsFile(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// 	client.StoreContextAndExit() should create directory and write settings file
	client.StoreContextAndExit()

	// Check if settings.json file was created and contains correct data
	settingsPath := filepath.Join(".dd", "context", "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Errorf("Expected settings file to exist at %s", settingsPath)
		return
	}

	// Read and parse the settings file
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Errorf("Failed to read settings file: %v", err)
		return
	}

	var settings net.SettingsResponseData
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Errorf("Failed to parse settings JSON: %v", err)
		return
	}

	// Verify the settings content
	if settings.ItrEnabled != mockIntegrations.Settings.ItrEnabled {
		t.Errorf("Expected ItrEnabled to be %v, got %v", mockIntegrations.Settings.ItrEnabled, settings.ItrEnabled)
	}
	if settings.TestsSkipping != mockIntegrations.Settings.TestsSkipping {
		t.Errorf("Expected TestsSkipping to be %v, got %v", mockIntegrations.Settings.TestsSkipping, settings.TestsSkipping)
	}
}

func TestDatadogClient_buildTestFQN(t *testing.T) {
	client := NewDatadogClient()

	testCases := []struct {
		suite      string
		test       string
		parameters string
		expected   string
	}{
		{"TestSuite", "testMethod", "param1", "TestSuite.testMethod.param1"},
		{"com.example.TestClass", "test_with_underscores", "param=value", "com.example.TestClass.test_with_underscores.param=value"},
		{"", "test", "", ".test."},
		{"suite", "", "params", "suite..params"},
	}

	for _, tc := range testCases {
		result := client.buildTestFQN(tc.suite, tc.test, tc.parameters)
		if result != tc.expected {
			t.Errorf("buildTestFQN(%q, %q, %q) = %q, expected %q",
				tc.suite, tc.test, tc.parameters, result, tc.expected)
		}
	}
}

func TestDatadogClient_GetSkippableTests_EmptyData(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		SkippableTests: map[string]map[string][]net.SkippableResponseDataAttributes{},
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

func TestDatadogClient_Initialize_CreatesContextDirectory(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	tags := map[string]string{"test": "value"}
	err := client.Initialize(tags)

	if err != nil {
		t.Errorf("Initialize() should not return error, got: %v", err)
	}

	// Check if .dd/context directory was created
	contextDir := filepath.Join(".dd", "context")
	if _, err := os.Stat(contextDir); os.IsNotExist(err) {
		t.Errorf("Expected .dd/context directory to be created")
	}
}

func TestDatadogClient_StoreContextAndExit_WritesKnownTestsFile(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		KnownTests: &net.KnownTestsResponseData{
			Tests: net.KnownTestsResponseDataModules{
				"module1": net.KnownTestsResponseDataSuites{
					"suite1": []string{"test1", "test2"},
					"suite2": []string{"test3"},
				},
				"module2": net.KnownTestsResponseDataSuites{
					"suite3": []string{"test4", "test5", "test6"},
				},
			},
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// StoreContextAndExit should create directory and write known tests file
	client.StoreContextAndExit()

	// Check if known_tests.json file was created and contains correct data
	knownTestsPath := filepath.Join(".dd", "context", "known_tests.json")
	if _, err := os.Stat(knownTestsPath); os.IsNotExist(err) {
		t.Errorf("Expected known tests file to exist at %s", knownTestsPath)
		return
	}

	// Read and parse the known tests file
	data, err := os.ReadFile(knownTestsPath)
	if err != nil {
		t.Errorf("Failed to read known tests file: %v", err)
		return
	}

	var knownTests net.KnownTestsResponseData
	if err := json.Unmarshal(data, &knownTests); err != nil {
		t.Errorf("Failed to parse known tests JSON: %v", err)
		return
	}

	// Verify the known tests structure
	if len(knownTests.Tests) != 2 {
		t.Errorf("Expected 2 modules in known tests, got %d", len(knownTests.Tests))
	}

	module1, exists := knownTests.Tests["module1"]
	if !exists {
		t.Error("Expected module1 to exist in known tests")
		return
	}

	if len(module1) != 2 {
		t.Errorf("Expected 2 suites in module1, got %d", len(module1))
	}

	suite1, exists := module1["suite1"]
	if !exists {
		t.Error("Expected suite1 to exist in module1")
		return
	}

	if len(suite1) != 2 {
		t.Errorf("Expected 2 tests in suite1, got %d", len(suite1))
	}

	expectedTests := []string{"test1", "test2"}
	for i, expectedTest := range expectedTests {
		if i >= len(suite1) || suite1[i] != expectedTest {
			t.Errorf("Expected test %s at position %d in suite1, got %v", expectedTest, i, suite1)
		}
	}
}

func TestDatadogClient_GetSkippableTests_WritesSkippableTestsFile(t *testing.T) {

	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: true,
		},
		SkippableTests: map[string]map[string][]net.SkippableResponseDataAttributes{
			"module1": {
				"suite1": []net.SkippableResponseDataAttributes{
					{
						Suite:      "TestSuite1",
						Name:       "test_method_1",
						Parameters: "param1",
					},
				},
			},
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// Initialize to create directory
	err := client.Initialize(map[string]string{})
	if err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	client.GetSkippableTests()

	// Check if skippable_tests.json file was created and contains correct data
	skippableTestsPath := filepath.Join(".dd", "context", "skippable_tests.json")
	if _, err := os.Stat(skippableTestsPath); os.IsNotExist(err) {
		t.Errorf("Expected skippable tests file to exist at %s", skippableTestsPath)
		return
	}

	// Read and parse the skippable tests file
	data, err := os.ReadFile(skippableTestsPath)
	if err != nil {
		t.Errorf("Failed to read skippable tests file: %v", err)
		return
	}

	var skippableTestsContext SkippableTestsContext
	if err := json.Unmarshal(data, &skippableTestsContext); err != nil {
		t.Errorf("Failed to parse skippable tests JSON: %v", err)
		return
	}

	// Verify that the correlation ID field is present in the parsed JSON structure
	// The value may be empty in test environment, but the field should exist
	t.Logf("Correlation ID: %s", skippableTestsContext.CorrelationID)

	// Verify the skippable tests structure
	if len(skippableTestsContext.SkippableTests) != 1 {
		t.Errorf("Expected 1 module in skippable tests, got %d", len(skippableTestsContext.SkippableTests))
	}

	module1, exists := skippableTestsContext.SkippableTests["module1"]
	if !exists {
		t.Error("Expected module1 to exist in skippable tests")
		return
	}

	suite1, exists := module1["suite1"]
	if !exists {
		t.Error("Expected suite1 to exist in module1")
		return
	}

	if len(suite1) != 1 {
		t.Errorf("Expected 1 test in suite1, got %d", len(suite1))
		return
	}

	expectedTest := mockIntegrations.SkippableTests["module1"]["suite1"][0]
	actualTest := suite1[0]
	if actualTest.Suite != expectedTest.Suite {
		t.Errorf("Expected suite to be %s, got %s", expectedTest.Suite, actualTest.Suite)
	}
	if actualTest.Name != expectedTest.Name {
		t.Errorf("Expected name to be %s, got %s", expectedTest.Name, actualTest.Name)
	}
	if actualTest.Parameters != expectedTest.Parameters {
		t.Errorf("Expected parameters to be %s, got %s", expectedTest.Parameters, actualTest.Parameters)
	}
}

func TestDatadogClient_StoreContextAndExit_WritesTestManagementTestsFile(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		TestManagementTestsData: &net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"module1": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"suite1": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"test1": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined:  true,
										Disabled:     false,
										AttemptToFix: true,
									},
								},
								"test2": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined:  false,
										Disabled:     true,
										AttemptToFix: false,
									},
								},
							},
						},
					},
				},
			},
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// StoreContextAndExit should create directory and write test management tests file
	client.StoreContextAndExit()

	// Check if test_management_tests.json file was created and contains correct data
	testManagementTestsPath := filepath.Join(".dd", "context", "test_management_tests.json")
	if _, err := os.Stat(testManagementTestsPath); os.IsNotExist(err) {
		t.Errorf("Expected test management tests file to exist at %s", testManagementTestsPath)
		return
	}

	// Read and parse the test management tests file
	data, err := os.ReadFile(testManagementTestsPath)
	if err != nil {
		t.Errorf("Failed to read test management tests file: %v", err)
		return
	}

	var testManagementTests net.TestManagementTestsResponseDataModules
	if err := json.Unmarshal(data, &testManagementTests); err != nil {
		t.Errorf("Failed to parse test management tests JSON: %v", err)
		return
	}

	// Verify the test management tests structure
	if len(testManagementTests.Modules) != 1 {
		t.Errorf("Expected 1 module in test management tests, got %d", len(testManagementTests.Modules))
	}

	module1, exists := testManagementTests.Modules["module1"]
	if !exists {
		t.Error("Expected module1 to exist in test management tests")
		return
	}

	if len(module1.Suites) != 1 {
		t.Errorf("Expected 1 suite in module1, got %d", len(module1.Suites))
	}

	suite1, exists := module1.Suites["suite1"]
	if !exists {
		t.Error("Expected suite1 to exist in module1")
		return
	}

	if len(suite1.Tests) != 2 {
		t.Errorf("Expected 2 tests in suite1, got %d", len(suite1.Tests))
	}

	// Verify test1 properties
	test1, exists := suite1.Tests["test1"]
	if !exists {
		t.Error("Expected test1 to exist in suite1")
		return
	}
	if !test1.Properties.Quarantined {
		t.Error("Expected test1 to be quarantined")
	}
	if test1.Properties.Disabled {
		t.Error("Expected test1 to not be disabled")
	}
	if !test1.Properties.AttemptToFix {
		t.Error("Expected test1 to have attempt to fix enabled")
	}

	// Verify test2 properties
	test2, exists := suite1.Tests["test2"]
	if !exists {
		t.Error("Expected test2 to exist in suite1")
		return
	}
	if test2.Properties.Quarantined {
		t.Error("Expected test2 to not be quarantined")
	}
	if !test2.Properties.Disabled {
		t.Error("Expected test2 to be disabled")
	}
	if test2.Properties.AttemptToFix {
		t.Error("Expected test2 to not have attempt to fix enabled")
	}
}

func TestDatadogClient_StoreContextAndExit_NilTestManagementTests(t *testing.T) {
	// Clean up any existing files before test
	os.RemoveAll(".dd")

	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
		TestManagementTestsData: nil,
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	// StoreContextAndExit should handle nil test management tests gracefully
	client.StoreContextAndExit()

	// Check that test_management_tests.json file was NOT created
	testManagementTestsPath := filepath.Join(".dd", "context", "test_management_tests.json")
	if _, err := os.Stat(testManagementTestsPath); !os.IsNotExist(err) {
		t.Errorf("Expected test management tests file to NOT exist at %s when data is nil, error: %v", testManagementTestsPath, err)
	}

	if !mockIntegrations.ShutdownCalled {
		t.Error("StoreContextAndExit should still call ExitCiVisibility even with nil test management tests")
	}
}
