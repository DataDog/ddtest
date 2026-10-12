package integration

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

var (
	serverAddressesMu sync.Mutex
	serverAddresses   = map[string]bool{}
	// unstartedServer creates a server and its listener; tests replace it.
	unstartedServer = httptest.NewUnstartedServer
)

// newServer starts an httptest server on a loopback address that no earlier
// server of this test process used. Test binaries keep Datadog's short-lived
// read cache by intake URL, so a server that reused a closed server's port
// could see another test's settings served from that cache instead of being
// asked for its own.
func newServer(handler http.Handler) *httptest.Server {
	serverAddressesMu.Lock()
	defer serverAddressesMu.Unlock()
	var held []*httptest.Server
	defer func() {
		for _, server := range held {
			_ = server.Listener.Close()
		}
	}()
	for {
		server := unstartedServer(handler)
		address := server.Listener.Addr().String()
		if !serverAddresses[address] {
			serverAddresses[address] = true
			server.Start()
			return server
		}
		// Holding this listener makes the next one take another port.
		held = append(held, server)
	}
}

// newServer never returns a server on an address that an earlier server of
// this process used, even after that server closed: it holds the listener
// that the system offered and takes another.
func TestNewServerSkipsUsedAddresses(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	used := newServer(handler)
	used.Close()
	reused, err := net.Listen("tcp", used.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	offers := []net.Listener{reused}
	original := unstartedServer
	unstartedServer = func(handler http.Handler) *httptest.Server {
		server := original(handler)
		if len(offers) > 0 {
			_ = server.Listener.Close()
			server.Listener, offers = offers[0], offers[1:]
		}
		return server
	}
	t.Cleanup(func() { unstartedServer = original })
	server := newServer(handler)
	defer server.Close()
	if server.URL == used.URL {
		t.Fatalf("a used address was offered again: %s", server.URL)
	}
	if again, err := net.Listen("tcp", used.Listener.Addr().String()); err != nil {
		t.Fatalf("the held listener stayed open: %v", err)
	} else {
		_ = again.Close()
	}
}
