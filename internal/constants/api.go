package constants

import "time"

const (
	ContentTypeJSON        = "application/json"
	ContentTypeOctetStream = "application/octet-stream"
	FormatJSON             = "json"
	SearchCommitsType      = "commit"

	DefaultMaxRetries = 3
	DefaultBackoff    = 100 * time.Millisecond
)

// Test Optimization API endpoint paths.
const (
	TestSuiteDurationsURLPath  = "/api/v2/ci/ddtest/test_suite_durations"
	TestCycleURLPath           = "/api/v2/citestcycle"
	TestCoverageURLPath        = "/api/v2/citestcov"
	SettingsURLPath            = "/api/v2/libraries/tests/services/setting"
	KnownTestsURLPath          = "/api/v2/ci/libraries/tests"
	SkippableTestsURLPath      = "/api/v2/ci/tests/skippable"
	TestManagementTestsURLPath = "/api/v2/test/libraries/test-management/tests"
	SearchCommitsURLPath       = "/api/v2/git/repository/search_commits"
	SendPackFilesURLPath       = "/api/v2/git/repository/packfile"
)

// JSON API type identifiers. Settings requests and responses use different types.
const (
	SettingsRequestType       = "ci_app_test_service_libraries_settings"
	SettingsResponseType      = "ci_app_tracers_test_service_settings"
	LibrariesTestsRequestType = "ci_app_libraries_tests_request"
	SkippableTestsRequestType = "test_params"
)
