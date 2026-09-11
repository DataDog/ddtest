// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package intake provides the local HTTP intake used by testdrive.
package intake

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const shutdownTimeout = 5 * time.Second

// Server is a local HTTP intake.
type Server struct {
	server    *http.Server
	url       string
	done      chan error
	closeOnce sync.Once
	closeErr  error
}

// Start starts an HTTP intake on a kernel-assigned loopback port.
func Start() (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local testdrive intake: %w", err)
	}

	httpServer := &http.Server{
		Handler:           newHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	server := &Server{
		server: httpServer,
		url:    "http://" + listener.Addr().String(),
		done:   make(chan error, 1),
	}

	go func() {
		serveErr := httpServer.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		server.done <- serveErr
		close(server.done)
	}()

	return server, nil
}

// URL returns the base URL of the running intake.
func (s *Server) URL() string {
	return s.url
}

// Close stops the intake and waits for its server goroutine to finish.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		shutdownErr := s.server.Shutdown(ctx)
		serveErr := <-s.done
		s.closeErr = errors.Join(shutdownErr, serveErr)
	})
	return s.closeErr
}
