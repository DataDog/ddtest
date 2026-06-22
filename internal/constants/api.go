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
