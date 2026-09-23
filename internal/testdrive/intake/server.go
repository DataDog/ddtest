// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package intake provides the local HTTP intake used by testdrive.
package intake

import (
	"bytes"
	"compress/gzip"
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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/DataDog/ddtest/internal/constants"
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
	Timestamp       time.Time       `json:"timestamp"`
	ContentEncoding string          `json:"content_encoding,omitempty"`
	DecodeError     string          `json:"decode_error,omitempty"`
	RawBody         []byte          `json:"raw_body,omitempty"`
	Method          string          `json:"method"`
	Path            string          `json:"path"`
	ContentType     string          `json:"content_type,omitempty"`
	Body            json.RawMessage `json:"body"`
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

	handlersMu sync.Mutex
	handlers   sync.WaitGroup
	closing    bool

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
		// Register under the same lock used to stop accepting work during Close.
		s.handlersMu.Lock()
		if s.closing {
			s.handlersMu.Unlock()
			http.Error(w, "intake is closing", http.StatusServiceUnavailable)
			return
		}
		s.handlers.Add(1)
		s.handlersMu.Unlock()
		defer s.handlers.Done()

		body, decodeErr := io.ReadAll(request.Body)
		_ = request.Body.Close()
		rawRequest := RawRequest{
			Method: request.Method, Path: request.URL.Path,
			Header: request.Header.Clone(), Body: slices.Clone(body),
		}
		if decodeErr == nil {
			body, decodeErr = uncompressRequestBody(rawRequest)
		}
		var decodedBody json.RawMessage
		if decodeErr == nil {
			decodedBody, decodeErr = decodeRequestBody(body, rawRequest.Header.Get("Content-Type"))
		}
		stored := storedRequest{
			Timestamp: time.Now().UTC(), Method: rawRequest.Method, Path: rawRequest.Path,
			ContentType:     rawRequest.Header.Get("Content-Type"),
			ContentEncoding: rawRequest.Header.Get("Content-Encoding"), Body: decodedBody,
		}
		if decodeErr != nil {
			stored.DecodeError = decodeErr.Error()
			stored.RawBody = rawRequest.Body
		} else if !utf8.Valid(body) {
			stored.RawBody = rawRequest.Body
		}
		s.requestsMu.Lock()
		persistErr := s.persistRequest(len(s.requests)+1, stored)
		s.requests = append(s.requests, rawRequest)
		s.requestsMu.Unlock()
		if persistErr != nil {
			http.Error(w, "save request", http.StatusInternalServerError)
			return
		}
		if decodeErr != nil {
			http.Error(w, "invalid request body: "+decodeErr.Error(), http.StatusBadRequest)
			return
		}

		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.Header.Del("Content-Encoding")
		request.Header.Set("Content-Length", strconv.Itoa(len(body)))
		next.ServeHTTP(w, request)
	})
}

func (s *Server) persistRequest(number int, stored storedRequest) error {
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	encoded = append(encoded, '\n')
	filename := fmt.Sprintf("%03d-%s.json", number, requestFileLabel(stored.Path))
	if err := os.WriteFile(filepath.Join(s.directory, filename), encoded, 0644); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return nil
}

func uncompressRequestBody(request RawRequest) ([]byte, error) {
	contentEncoding := strings.TrimSpace(request.Header.Get("Content-Encoding"))
	if contentEncoding == "" || strings.EqualFold(contentEncoding, "identity") {
		return request.Body, nil
	}
	if !strings.EqualFold(contentEncoding, "gzip") {
		return nil, fmt.Errorf("unsupported content encoding %q", contentEncoding)
	}

	reader, err := gzip.NewReader(bytes.NewReader(request.Body))
	if err != nil {
		return nil, fmt.Errorf("open gzip request body: %w", err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read gzip request body: %w", err)
	}
	return body, nil
}

func decodeRequestBody(body []byte, contentType string) (json.RawMessage, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case constants.ContentTypeJSON:
		if !json.Valid(body) {
			return nil, fmt.Errorf("invalid JSON")
		}
		return slices.Clone(body), nil
	case "application/msgpack", "application/x-msgpack":
		decoded, err := decodeMessagePack(body)
		if err != nil {
			return nil, fmt.Errorf("invalid MessagePack: %w", err)
		}
		return decoded, nil
	case "multipart/form-data":
		return decodeMultipart(body, params["boundary"])
	default:
		if !utf8.Valid(body) {
			return json.Marshal(struct {
				Base64 []byte `json:"base64"`
			}{Base64: body})
		}
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
	case constants.SettingsURLPath:
		return "settings"
	case constants.TestCycleURLPath:
		return "citestcycle"
	case constants.TestCoverageURLPath:
		return "citestcov"
	case constants.KnownTestsURLPath:
		return "known-tests"
	case constants.SkippableTestsURLPath:
		return "skippable-tests"
	case constants.TestManagementTestsURLPath:
		return "test-management"
	default:
		return "request"
	}
}

// Close stops the intake and waits for its server goroutine to finish.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		s.handlersMu.Lock()
		s.closing = true
		s.handlersMu.Unlock()
		shutdownErr := s.server.Shutdown(ctx)
		var closeErr error
		if shutdownErr != nil {
			closeErr = s.server.Close()
		}
		s.handlers.Wait()
		serveErr := <-s.done
		s.closeErr = errors.Join(shutdownErr, closeErr, serveErr)
	})
	return s.closeErr
}
