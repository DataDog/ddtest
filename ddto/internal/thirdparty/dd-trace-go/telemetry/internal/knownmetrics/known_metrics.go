// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package knownmetrics

import (
	"slices"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

type Declaration struct {
	Namespace transport.Namespace  `json:"namespace"`
	Type      transport.MetricType `json:"type"`
	Name      string               `json:"name"`
}

// IsKnownMetric returns true if the given metric name is a known metric by the backend
// This is linked to generated common_metrics.json file and golang_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsKnownMetric(namespace transport.Namespace, typ transport.MetricType, name string) bool {
	return IsCommonMetric(namespace, typ, name) || IsLanguageMetric(typ, name)
}

// IsCommonMetric returns true if the given metric name is a known common (cross-language) metric by the backend
// This is linked to the generated common_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsCommonMetric(namespace transport.Namespace, typ transport.MetricType, name string) bool {
	decl := Declaration{Namespace: namespace, Type: typ, Name: name}
	return slices.Contains(commonMetrics, decl)
}

// IsLanguageMetric returns true if the given metric name is a known Go language metric by the backend
// This is linked to the generated golang_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsLanguageMetric(typ transport.MetricType, name string) bool {
	decl := Declaration{Type: typ, Name: name}
	return slices.Contains(golangMetrics, decl)
}
