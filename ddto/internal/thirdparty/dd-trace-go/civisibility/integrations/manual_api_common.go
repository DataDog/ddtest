// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"sync"
	"time"

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/ddtrace/ext"
)

func getMeta(s *tracer.Span, key string) (string, bool)    { return s.Meta(key) }
func getMetric(s *tracer.Span, key string) (float64, bool) { return s.Metric(key) }

// common
var _ ddTslvEvent = (*ciVisibilityCommon)(nil)

// ciVisibilityCommon is a struct that implements the ddTslvEvent interface and provides common functionality for CI visibility.
type ciVisibilityCommon struct {
	mutex     sync.Mutex
	startTime time.Time

	tags   []tracer.StartSpanOption
	span   *tracer.Span
	closed bool

	ctxMutex sync.Mutex
	ctx      context.Context
}

// Context returns the context of the event.
func (c *ciVisibilityCommon) Context() context.Context {
	c.ctxMutex.Lock()
	defer c.ctxMutex.Unlock()
	return c.ctx
}

// StartTime returns the start time of the event.
func (c *ciVisibilityCommon) StartTime() time.Time { return c.startTime }

// SetError sets an error on the event.
func (c *ciVisibilityCommon) SetError(options ...ErrorOption) {
	defaults := &tslvErrorOptions{}
	for _, o := range options {
		o(defaults)
	}

	// if there is an error, set the span with the error
	if defaults.err != nil {
		c.span.SetTag(ext.Error, defaults.err)
		setCIVisibilitySpanTag(c.span, ext.ErrorMsg, defaults.err.Error())
		return
	}

	// if there is no error, set the span with error the error info

	// set the span with error:1
	setCIVisibilitySpanTag(c.span, ext.Error, true)

	// set the error type
	if defaults.errType != "" {
		setCIVisibilitySpanTag(c.span, ext.ErrorType, defaults.errType)
	}

	// set the error message
	if defaults.message != "" {
		setCIVisibilitySpanTag(c.span, ext.ErrorMsg, defaults.message)
	}

	// set the error stacktrace
	if defaults.callstack != "" {
		setCIVisibilitySpanTag(c.span, ext.ErrorStack, defaults.callstack)
	}
}

// SetTag sets a tag on the event.
func (c *ciVisibilityCommon) SetTag(key string, value any) {
	setCIVisibilitySpanTag(c.span, key, value)
}

// GetTag retrieves a tag from the event.
func (c *ciVisibilityCommon) GetTag(key string) (any, bool) {
	// Check if the span is nil
	if c.span == nil {
		return nil, false
	}

	// Check if the key is a meta key
	metaVal, ok := getMeta(c.span, key)
	if ok {
		return metaVal, true
	}

	// Check if the key is a metric key
	metricVal, ok := getMetric(c.span, key)
	return metricVal, ok
}

// fillCommonTags adds common tags to the span options for CI visibility.
func fillCommonTags(opts []tracer.StartSpanOption) []tracer.StartSpanOption {
	common, ciMetrics := commonTagOptions(), utils.GetCIMetrics()
	combined := make([]tracer.StartSpanOption, len(opts), len(opts)+len(common)+len(ciMetrics)+2)
	copy(combined, opts)
	opts = combined
	opts = append(opts, originOption, manualKeepOption)

	opts = append(opts, common...)

	// Apply CI metrics
	for k, v := range ciMetrics {
		opts = append(opts, ciVisibilityTag(k, v))
	}

	return opts
}

var commonTagsCache struct {
	sync.Mutex
	revision     uint64
	payloadFiles bool
	options      []tracer.StartSpanOption
}

// Common options own an immutable, truncated copy. Revision changes include
// AddCITags, AddCITagsMap and ResetCITags, so late feature discovery stays fresh.
func commonTagOptions() []tracer.StartSpanOption {
	tags, revision := utils.GetCITagsSnapshot()
	payloadFiles := bazel.IsPayloadFilesModeEnabled()
	commonTagsCache.Lock()
	defer commonTagsCache.Unlock()
	if commonTagsCache.options != nil && commonTagsCache.revision == revision && commonTagsCache.payloadFiles == payloadFiles {
		return commonTagsCache.options
	}
	shared := make(map[string]string)
	opts := make([]tracer.StartSpanOption, 0, len(tags)+1)
	for key, value := range tags {
		if key == constants.TestSessionName {
			continue // Session name already belongs to envelope metadata.
		}
		if tracer.IsSharedCITag(key) {
			if !payloadFiles {
				shared[key] = truncateCIVisibilityMetaValue(value)
			}
		} else {
			opts = append(opts, ciVisibilityTag(key, value))
		}
	}
	if len(shared) != 0 {
		opts = append(opts, tracer.NewCommonTags(shared).Option())
	}
	commonTagsCache.revision, commonTagsCache.payloadFiles = revision, payloadFiles
	commonTagsCache.options = opts
	return opts
}

func (c *ciVisibilityCommon) getContextValue(key any) any {
	c.ctxMutex.Lock()
	defer c.ctxMutex.Unlock()
	return c.ctx.Value(key)
}

func (c *ciVisibilityCommon) setContextValue(key, value any) {
	c.ctxMutex.Lock()
	defer c.ctxMutex.Unlock()
	c.ctx = context.WithValue(c.ctx, key, value)
}

// Constant options are built once instead of for every event.
var (
	originOption     = ciVisibilityTag(constants.Origin, constants.CIAppTestOrigin)
	manualKeepOption = ciVisibilityTag(ext.ManualKeep, true)
)
