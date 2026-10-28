// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

// discoveryLanguages registers every language onboarding discovers. This is
// the only shared file that names them; everything else goes through
// discoveryLanguage (see language.go and TestLanguageSpecificsStayInTheirFiles).
var discoveryLanguages = []discoveryLanguage{javascriptDiscovery{}, pythonDiscovery{}, rubyDiscovery{}, javaDiscovery{}}
