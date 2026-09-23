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

// API paths omit the leading slash so clients can prepend Agent/EVP routes.
const (
	SettingsURLPath            = "api/v2/libraries/tests/services/setting"
	KnownTestsURLPath          = "api/v2/ci/libraries/tests"
	SkippableTestsURLPath      = "api/v2/ci/tests/skippable"
	TestManagementTestsURLPath = "api/v2/test/libraries/test-management/tests"
	SearchCommitsURLPath       = "api/v2/git/repository/search_commits"
	SendPackFilesURLPath       = "api/v2/git/repository/packfile"
)
