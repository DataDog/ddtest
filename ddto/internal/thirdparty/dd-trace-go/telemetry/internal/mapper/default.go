// Copyright 2025 Datadog, Inc.
// Licensed under the Apache License, Version 2.0.

package mapper

import "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"

// NewDefaultMapper batches CI telemetry; it emits no SDK heartbeats.
func NewDefaultMapper() Mapper { return &messageBatchReducer{} }

type messageBatchReducer struct{}

func (t *messageBatchReducer) Transform(payloads []transport.Payload) ([]transport.Payload, Mapper) {
	if len(payloads) <= 1 {
		return payloads, t
	}
	messages := make([]transport.Message, len(payloads))
	for i, p := range payloads {
		messages[i] = transport.Message{RequestType: p.RequestType(), Payload: p}
	}
	return []transport.Payload{transport.MessageBatch(messages)}, t
}
