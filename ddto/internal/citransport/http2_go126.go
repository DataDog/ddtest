//go:build go1.26

package citransport

import "net/http"

// strictHTTP2Streams makes requests wait for a stream on the HTTP/2
// connection when the server's stream limit is reached, instead of dialing
// another connection that a request could then leave unused.
func strictHTTP2Streams(t *http.Transport) {
	t.HTTP2 = &http.HTTP2Config{StrictMaxConcurrentRequests: true}
}
