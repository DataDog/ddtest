package offlineagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server serves the agent on a listener until Stop.
type Server struct {
	server  *http.Server
	address string
	done    chan struct{}
	err     error
}

// Serve starts serving the agent on listener.
func Serve(listener net.Listener, config Config) *Server {
	s := &Server{
		server:  &http.Server{Handler: NewHandler(config), ReadHeaderTimeout: 10 * time.Second},
		address: listener.Addr().String(),
		done:    make(chan struct{}),
	}
	go func() {
		s.err = s.server.Serve(listener)
		close(s.done)
	}()
	return s
}

// URL is the agent URL for tracing libraries, such as DD_TRACE_AGENT_URL.
func (s *Server) URL() string {
	return "http://" + s.address
}

// Done is closed when the server stops, either because of Stop or because
// serving failed; Err then reports why.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// Err is the reason the server stopped, once Done is closed.
func (s *Server) Err() error {
	return s.err
}

// Stop stops accepting requests and waits, until ctx is done, for the
// requests in progress, so every payload a library sent is saved.
func (s *Server) Stop(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("stop the offline agent: %w", err)
	}
	<-s.done
	if !errors.Is(s.err, http.ErrServerClosed) {
		return fmt.Errorf("offline agent stopped: %w", s.err)
	}
	return nil
}
