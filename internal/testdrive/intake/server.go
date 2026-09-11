// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package intake provides the local HTTP intake used by testdrive.
package intake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

const shutdownTimeout = 5 * time.Second

// RawRequest is an HTTP request observed by the local intake.
type RawRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// Server is a local HTTP intake.
type Server struct {
	server    *http.Server
	url       string
	done      chan error
	closeOnce sync.Once
	closeErr  error

	requestsMu sync.Mutex
	requests   []RawRequest
}

// Start starts an HTTP intake on a kernel-assigned loopback port.
func Start() (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local testdrive intake: %w", err)
	}

	server := &Server{
		url:  "http://" + listener.Addr().String(),
		done: make(chan error, 1),
	}
	httpServer := &http.Server{
		Handler:           server.recordRequests(newHandler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	server.server = httpServer

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

// Requests returns a snapshot of the raw requests observed by the intake.
func (s *Server) Requests() []RawRequest {
	s.requestsMu.Lock()
	defer s.requestsMu.Unlock()

	requests := make([]RawRequest, len(s.requests))
	for i, request := range s.requests {
		requests[i] = RawRequest{
			Method: request.Method,
			Path:   request.Path,
			Header: request.Header.Clone(),
			Body:   slices.Clone(request.Body),
		}
	}
	return requests
}

func (s *Server) recordRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusBadRequest)
			return
		}
		_ = request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(body))

		s.requestsMu.Lock()
		s.requests = append(s.requests, RawRequest{
			Method: request.Method,
			Path:   request.URL.Path,
			Header: request.Header.Clone(),
			Body:   slices.Clone(body),
		})
		s.requestsMu.Unlock()

		next.ServeHTTP(w, request)
	})
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
