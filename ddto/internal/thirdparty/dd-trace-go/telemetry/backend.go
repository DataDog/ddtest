// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/stacktrace"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

const (
	stackTraceKey      = "stacktrace"
	telemetryStackSkip = 2 // Skip loggerBackend.add and loggerBackend.Add.
)

type loggerKey struct {
	tags    string
	message string
	level   LogLevel

	// captureStackNow is true for entries whose stack was captured synchronously at
	// the call site (WithCaptureStacktraceNow), i.e. ReportError/ReportPanic reports.
	// It keeps such reports out of the dedup bucket of plain, stackless log
	// entries with the same message, level, and tags — otherwise a report
	// would merge into the plain entry and silently lose both its stack trace
	// and its error/panic attributes.
	captureStackNow bool
}

type loggerValue struct {
	count  atomic.Uint32
	record Record

	captureStacktrace bool
	// stacktraceCaptured is true if rawStack was already populated eagerly
	// (WithCaptureStacktraceNow), so add() must not re-capture it — a re-capture at
	// this point could run on a queued-and-replayed call's stack, not the
	// original caller's.
	stacktraceCaptured bool
	rawStack           stacktrace.RawStackTrace
}

type formatter struct {
	buffer  *bytes.Buffer
	handler slog.Handler
}

type loggerBackend struct {
	mu    sync.Mutex
	store map[loggerKey]*loggerValue

	distinctLogs       atomic.Int32
	maxDistinctLogs    int32
	onceMaxLogsReached sync.Once

	formatters *sync.Pool
}

func newLoggerBackend(maxDistinctLogs int32) *loggerBackend {
	return &loggerBackend{
		store:           make(map[loggerKey]*loggerValue),
		maxDistinctLogs: maxDistinctLogs,

		formatters: &sync.Pool{
			New: func() any {
				buf := &bytes.Buffer{}
				return &formatter{
					buffer: buf,
					handler: slog.NewTextHandler(buf, &slog.HandlerOptions{
						ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
							// Remove time, level, source attributes, stacktrace and empty message attributes
							if a.Key == slog.TimeKey ||
								a.Key == slog.LevelKey ||
								a.Key == slog.SourceKey ||
								a.Key == stackTraceKey {
								return slog.Attr{}
							}
							if a.Key == slog.MessageKey && a.Value.String() == "" {
								return slog.Attr{}
							}
							return a
						},
					}),
				}
			},
		},
	}
}

func (logger *loggerBackend) Add(record Record, opts ...LogOption) {
	if logger.distinctLogs.Load() >= logger.maxDistinctLogs {
		logger.onceMaxLogsReached.Do(func() {
			logger.add(NewRecord(LogError, "telemetry: log count exceeded maximum, dropping log"), WithStacktrace())
		})
		return
	}

	logger.add(record, opts...)
}

func (logger *loggerBackend) add(record Record, opts ...LogOption) {
	key := loggerKey{
		level:   slogLevelToLogLevel(record.Level),
		message: record.Message,
	}

	for _, opt := range opts {
		opt(&key, nil)
	}

	logger.mu.Lock()
	if value, ok := logger.store[key]; ok {
		value.count.Add(1)
		logger.mu.Unlock()
		return
	}
	logger.mu.Unlock()

	// Create the record at capture time, not send time. Capture before entering
	// the registry lock so callbacks and stack capture run outside it.
	candidate := &loggerValue{
		record: record,
	}
	for _, opt := range opts {
		opt(nil, candidate)
	}
	if candidate.captureStacktrace && len(candidate.rawStack.PCs) == 0 {
		// A pre-captured stack (see withRawStacktrace, WithCaptureStacktraceNow)
		// is already the right one — it was captured at the call site,
		// precisely to avoid capturing this replay goroutine's stack instead.
		candidate.rawStack = stacktrace.CaptureRaw(telemetryStackSkip)
	}

	logger.mu.Lock()
	value, loaded := logger.store[key]
	if !loaded {
		value = candidate
		logger.store[key] = value
		logger.distinctLogs.Add(1)
	}
	value.count.Add(1)
	logger.mu.Unlock()
}

func (logger *loggerBackend) Payload() transport.Payload {
	// Detach the batch while additions are excluded. A concurrent Add either
	// increments this batch before detachment or enters the next one.
	logger.mu.Lock()
	entries := logger.store
	if len(entries) == 0 {
		logger.mu.Unlock()
		return nil
	}
	logger.store = make(map[loggerKey]*loggerValue)
	logger.distinctLogs.Add(-int32(len(entries)))
	logger.mu.Unlock()

	logs := make([]transport.LogMessage, 0, len(entries))
	for key, value := range entries {
		msg := transport.LogMessage{
			Message:    logger.formatMessage(value.record),
			Level:      key.level,
			Tags:       key.tags,
			Count:      value.count.Load(),
			TracerTime: value.record.Time.Unix(),
		}
		if value.captureStacktrace {
			msg.StackTrace = stacktrace.Format(value.rawStack.SymbolicateWithRedaction())
		}
		logs = append(logs, msg)
	}

	if len(logs) == 0 {
		return nil
	}

	return transport.Logs{Logs: logs}
}

func (logger *loggerBackend) formatMessage(record Record) string {
	if logger.formatters == nil {
		return record.Message
	}

	hasAttrs := false
	record.Attrs(func(attr slog.Attr) bool {
		hasAttrs = true
		return false
	})

	if !hasAttrs {
		return record.Message
	}

	// Capture the message before clearing it.
	message := record.Message

	formatter := logger.formatters.Get().(*formatter)
	defer func() {
		formatter.buffer.Reset()
		logger.formatters.Put(formatter)
	}()

	// Clear the message so TextHandler only formats attributes.
	record.Message = ""
	formatter.handler.Handle(context.Background(), record.Record)
	formattedAttrs := strings.TrimSpace(formatter.buffer.String())

	if formattedAttrs == "" {
		return message
	}

	return message + ": " + formattedAttrs
}
