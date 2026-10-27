//go:build !go1.26

package citransport

import "net/http"

// strictHTTP2Streams does nothing before Go 1.26, whose HTTP2Config cannot
// make a saturated HTTP/2 connection wait for a stream.
func strictHTTP2Streams(*http.Transport) {}
