// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build go1.26

package net

import "net/http"

// strictHTTP2Streams makes requests wait for a stream on the HTTP/2
// connection when the server's stream limit is reached, instead of dialing
// another connection that a request could then leave unused.
func strictHTTP2Streams(t *http.Transport) {
	t.HTTP2 = &http.HTTP2Config{StrictMaxConcurrentRequests: true}
}
