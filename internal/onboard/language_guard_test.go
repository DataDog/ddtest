// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"testing"

	"github.com/DataDog/ddtest/internal/languageguard"
)

// Shared files talk to languages only through discoveryLanguage in language.go. Python and
// Ruby code lives in python*.go and ruby*.go, and languages.go is the only file
// that names every language. Tests and comments are not checked.
func TestLanguageSpecificsStayInTheirFiles(t *testing.T) {
	languageguard.Check(t, ".")
}
