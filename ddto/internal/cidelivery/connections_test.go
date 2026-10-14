package cidelivery

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A request can take a connection that another request released while its own
// dial still runs. net/http then adds the dial's connection to the idle pool in
// a goroutine of its own, reading the clock, and so time.Local, while tests may
// change it. CloseConnections, which runs before the first test, must stop such
// a dial or close its late connection instead.
func TestCloseConnectionsKeepsLateDialsOutOfTheIdlePool(t *testing.T) {
	for _, connected := range []bool{false, true} {
		name := "dialing"
		if connected {
			name = "connected"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			states := map[net.Conn][]http.ConnState{}
			firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/first" {
					close(firstEntered)
					<-releaseFirst
				}
			}))
			server.Config.ConnState = func(c net.Conn, state http.ConnState) {
				mu.Lock()
				states[c] = append(states[c], state)
				mu.Unlock()
			}
			server.Start()
			defer server.Close()

			// The second dial waits until the test releases it. While dialing,
			// it also returns when net/http cancels it.
			secondDialing, releaseDial := make(chan struct{}), make(chan struct{})
			dialResult := make(chan error, 1)
			var dials sync.Mutex
			dialCount := 0
			var dialer net.Dialer
			var lateAddress string // The late connection's client address.
			transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Lock()
				dialCount++
				n := dialCount
				dials.Unlock()
				if n != 2 {
					return dialer.DialContext(ctx, network, address)
				}
				var conn net.Conn
				var err error
				if connected {
					conn, err = dialer.DialContext(ctx, network, address)
					if err == nil {
						lateAddress = conn.LocalAddr().String()
					}
					close(secondDialing)
					<-releaseDial
				} else {
					close(secondDialing)
					select {
					case <-ctx.Done():
						err = ctx.Err()
					case <-releaseDial:
						conn, err = dialer.DialContext(ctx, network, address)
					}
				}
				dialResult <- err
				return conn, err
			}}
			client := &http.Client{Transport: transport}
			defer transport.CloseIdleConnections()
			remove := RegisterConnectionCloser(client.CloseIdleConnections)
			defer remove()

			get := func(path string) <-chan error {
				done := make(chan error, 1)
				go func() {
					resp, err := client.Get(server.URL + path)
					if err == nil {
						_, err = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
					done <- err
				}()
				return done
			}
			first := get("/first")
			<-firstEntered
			second := get("/second")
			<-secondDialing
			// The first connection becomes idle and serves the second request.
			close(releaseFirst)
			for _, done := range []<-chan error{first, second} {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}

			CloseConnections()
			close(releaseDial)
			err := <-dialResult
			if !connected {
				if err == nil {
					t.Fatal("the unused dial was not canceled")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// The late connection itself must close instead of waiting in the
			// pool; the first connection closes as an idle one either way.
			deadline := time.Now().Add(10 * time.Second)
			for {
				mu.Lock()
				var late []http.ConnState
				for conn, s := range states {
					if conn.RemoteAddr().String() == lateAddress {
						late = s
					}
				}
				mu.Unlock()
				if len(late) != 0 && late[len(late)-1] == http.StateClosed {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("the late connection stayed open in the idle pool: states %v", late)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
