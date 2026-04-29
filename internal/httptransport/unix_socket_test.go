package httptransport

import (
	"net/http"
	"testing"
	"time"
)

func TestUnixSocketURL(t *testing.T) {
	u := UnixSocketURL("/tmp/ddtest-agent.sock")

	if got := u.String(); got != "http://UDS__tmp_ddtest-agent.sock" {
		t.Errorf("Expected sanitized UDS URL, got %q", got)
	}
}

func TestUnixSocketClient(t *testing.T) {
	client := UnixSocketClient("/tmp/ddtest-agent.sock", 45*time.Second)

	if client.Timeout != 45*time.Second {
		t.Errorf("Expected timeout 45s, got %s", client.Timeout)
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("Expected HTTP transport, got %T", client.Transport)
	}
}
