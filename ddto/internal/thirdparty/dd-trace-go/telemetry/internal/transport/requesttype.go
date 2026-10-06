// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

// RequestType determines how the Payload of a request should be handled
type RequestType string

const (
	// RequestTypeAppStarted is the first message sent by the telemetry
	// client, containing the configuration loaded at startup
	RequestTypeAppStarted RequestType = "app-started"

	// RequestTypeGenerateMetrics contains count, gauge, or rate metrics accumulated by the
	// client, and is sent periodically at flush
	RequestTypeGenerateMetrics RequestType = "generate-metrics"

	// RequestTypeDistributions is to send distribution type metrics accumulated by the
	// client, and is sent periodically at flush
	RequestTypeDistributions RequestType = "distributions"

	// RequestTypeAppClosing is sent when the telemetry client is stopped
	RequestTypeAppClosing RequestType = "app-closing"

	// RequestTypeAppClientConfigurationChange is sent if there are changes
	// to the client library configuration
	RequestTypeAppClientConfigurationChange RequestType = "app-client-configuration-change"

	// RequestTypeAppProductChange is sent when products are enabled/disabled
	RequestTypeAppProductChange RequestType = "app-product-change"

	// RequestTypeAppIntegrationsChange is sent when the telemetry client starts
	// with info on which integrations are used.
	RequestTypeAppIntegrationsChange RequestType = "app-integrations-change"

	// RequestTypeMessageBatch is a wrapper over a list of payloads
	RequestTypeMessageBatch RequestType = "message-batch"

	// RequestTypeLogs is used to send logs to the backend
	RequestTypeLogs RequestType = "logs"
)
