// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package internal

import (
	"net/http"
	"testing"
	"time"
)

// The Agent's socket takes no proxy: an HTTPS or SOCKS proxy in the
// environment would make the client negotiate with the Agent.
func TestUDSClientUsesNoProxy(t *testing.T) {
	transport, ok := UDSClient("/tmp/agent.sock", time.Second).Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("transport %T with a proxy function", UDSClient("/tmp/agent.sock", time.Second).Transport)
	}
}
