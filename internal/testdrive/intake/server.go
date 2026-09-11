// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package intake provides the local HTTP intake used by testdrive.
package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/tinylib/msgp/msgp"
)

const (
	shutdownTimeout     = 5 * time.Second
	intakeDirectoryName = "intake"
)

// RawRequest is an HTTP request observed by the local intake.
type RawRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type storedRequest struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	ContentType string          `json:"content_type,omitempty"`
	Body        json.RawMessage `json:"body"`
}

type storedMultipartPart struct {
	Name        string          `json:"name"`
	Filename    string          `json:"filename,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	Body        json.RawMessage `json:"body"`
}

// Server is a local HTTP intake.
type Server struct {
	server    *http.Server
	url       string
	done      chan error
	closeOnce sync.Once
	closeErr  error
	directory string

	requestsMu sync.Mutex
	requests   []RawRequest
}

// Start starts an HTTP intake on a kernel-assigned loopback port and stores
// every request under the testdrive session directory.
func Start(sessionDirectory string) (*Server, error) {
	intakeDirectory := filepath.Join(sessionDirectory, intakeDirectoryName)
	if err := os.MkdirAll(intakeDirectory, 0755); err != nil {
		return nil, fmt.Errorf("create local testdrive intake directory: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local testdrive intake: %w", err)
	}

	server := &Server{
		url:       "http://" + listener.Addr().String(),
		done:      make(chan error, 1),
		directory: intakeDirectory,
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

		rawRequest := RawRequest{
			Method: request.Method,
			Path:   request.URL.Path,
			Header: request.Header.Clone(),
			Body:   slices.Clone(body),
		}
		s.requestsMu.Lock()
		requestNumber := len(s.requests) + 1
		persistErr := s.persistRequest(requestNumber, rawRequest)
		s.requests = append(s.requests, rawRequest)
		s.requestsMu.Unlock()
		if persistErr != nil {
			http.Error(w, "save request", http.StatusInternalServerError)
			return
		}

		next.ServeHTTP(w, request)
	})
}

func (s *Server) persistRequest(number int, request RawRequest) error {
	decodedBody, err := decodeRequestBody(request.Body, request.Header.Get("Content-Type"))
	if err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	stored := storedRequest{
		Method:      request.Method,
		Path:        request.Path,
		ContentType: request.Header.Get("Content-Type"),
		Body:        decodedBody,
	}
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	encoded = append(encoded, '\n')
	filename := fmt.Sprintf("%03d-%s.json", number, requestFileLabel(request.Path))
	if err := os.WriteFile(filepath.Join(s.directory, filename), encoded, 0644); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return nil
}

func decodeRequestBody(body []byte, contentType string) (json.RawMessage, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "application/json":
		if !json.Valid(body) {
			return nil, fmt.Errorf("invalid JSON")
		}
		return slices.Clone(body), nil
	case "application/msgpack", "application/x-msgpack":
		return decodeMessagePack(body)
	case "multipart/form-data":
		return decodeMultipart(body, params["boundary"])
	default:
		return json.Marshal(string(body))
	}
}

func decodeMessagePack(body []byte) (json.RawMessage, error) {
	decoded, rest, err := msgp.ReadIntfBytes(body)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("unexpected trailing MessagePack bytes")
	}
	return json.Marshal(decoded)
}

func decodeMultipart(body []byte, boundary string) (json.RawMessage, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := make([]storedMultipartPart, 0)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return json.Marshal(struct {
				Parts []storedMultipartPart `json:"parts"`
			}{Parts: parts})
		}
		if err != nil {
			return nil, err
		}

		partBody, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return nil, readErr
		}
		contentType := part.Header.Get("Content-Type")
		decodedBody, decodeErr := decodeRequestBody(partBody, contentType)
		if decodeErr != nil {
			return nil, decodeErr
		}
		parts = append(parts, storedMultipartPart{
			Name:        part.FormName(),
			Filename:    part.FileName(),
			ContentType: contentType,
			Body:        decodedBody,
		})
	}
}

func requestFileLabel(requestPath string) string {
	switch requestPath {
	case settingsPath:
		return "settings"
	case testCyclePath:
		return "citestcycle"
	case testCoveragePath:
		return "citestcov"
	default:
		return "request"
	}
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
