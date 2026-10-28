// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !go1.26

package net

import "net/http"

// strictHTTP2Streams does nothing before Go 1.26, whose HTTP2Config cannot
// make a saturated HTTP/2 connection wait for a stream.
func strictHTTP2Streams(*http.Transport) {}
