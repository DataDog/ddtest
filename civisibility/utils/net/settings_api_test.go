// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSettingsResponseDataPreservesCoverageReportUploadEnabled verifies that
// coverage_report_upload_enabled returned by the settings API is preserved
// through the SettingsResponseData round-trip. Without a matching struct
// field, encoding/json would silently drop the key when ddtest re-serializes
// the settings into its cache file, disabling coverage upload in downstream
// consumers such as datadog-ci.
func TestSettingsResponseDataPreservesCoverageReportUploadEnabled(t *testing.T) {
	payload := `{"data":{"id":"settings-id","type":"ci_app_test_service_libraries_settings","attributes":{"itr_enabled":true,"tests_skipping":true,"coverage_report_upload_enabled":true}}}`

	var resp settingsResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !resp.Data.Attributes.CoverageReportUploadEnabled {
		t.Fatalf("CoverageReportUploadEnabled = false; want true after unmarshalling %q", payload)
	}

	out, err := json.Marshal(resp.Data.Attributes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"coverage_report_upload_enabled":true`) {
		t.Fatalf("marshalled JSON does not preserve coverage_report_upload_enabled=true: %s", out)
	}
}
