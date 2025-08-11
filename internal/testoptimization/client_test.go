package testoptimization

import (
	"maps"
	"testing"
	"time"

	"github.com/DataDog/datadog-test-runner/civisibility/utils/net"
)

// Mock implementations for testing
type MockCIVisibilityIntegrations struct {
	InitializationCalled bool
	ShutdownCalled       bool
	Settings             *net.SettingsResponseData
	SkippableTests       map[string]map[string][]net.SkippableResponseDataAttributes
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
	mockIntegrations := &MockCIVisibilityIntegrations{}
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

func TestDatadogClient_GetSkippableTests_Disabled(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    false,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippableTests() should return empty map when ITR is disabled, got %d items", len(result))
	}
}

func TestDatadogClient_GetSkippableTests_Enabled(t *testing.T) {
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

func TestDatadogClient_GetSkippableTests_ItrEnabledButSkippingDisabled(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippableTests() should return empty map when tests skipping is disabled, got %d items", len(result))
	}
}

func TestDatadogClient_Shutdown(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	client.Shutdown()

	if !mockIntegrations.ShutdownCalled {
		t.Error("Shutdown() should call ExitCiVisibility")
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

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map even with empty data")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippableTests() should return empty map with empty data, got %d items", len(result))
	}
}

func TestDatadogClient_GetSkippableTests_NilSettings(t *testing.T) {
	mockIntegrations := &MockCIVisibilityIntegrations{
		Settings: nil, // nil settings
	}
	mockUtils := &MockUtils{}
	client := NewDatadogClientWithDependencies(mockIntegrations, mockUtils)

	result := client.GetSkippableTests()

	if result == nil {
		t.Error("GetSkippableTests() should return non-nil map even with nil settings")
	}

	if len(result) != 0 {
		t.Errorf("GetSkippableTests() should return empty map with nil settings, got %d items", len(result))
	}
}
