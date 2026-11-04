// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ddtest never recommends changing a CI image, in any output: the setup
// without Bash or curl replaces every such suggestion, and adding Bash and
// curl to an image is the user's recommendation alone. The final report's
// caveat that a Bash-capable image would let Datadog's installer set the jobs
// up, which is simpler and more reliable, is the user's to act on too.
func TestNothingRecommendsChangingAnImage(t *testing.T) {
	banned := regexp.MustCompile(`(?i)Bash-capable image|non-Alpine variant|full image \(without -slim\)|cimg/ convenience image|need(s)? another image|another image with Bash|build FROM the|ask before making it|change only the image|switch (those|these|the) jobs to a|give (it|the job|the step) (a|such an) [a-z ]*image|run (the|its) tests in an image|run them in an image|Give the job a (literal|docker) image|Give the step a literal image|Make the Dockerfile's final FROM|install curl in (an earlier step|a command before|the Dockerfile)|add an entry that installs curl|RUN apk add --no-cache bash`)
	var sources []string
	for _, pattern := range []string{"*.go", "../testdrive/*.go", "../cmd/*.go", "../../docs/*.md"} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)
		sources = append(sources, matches...)
	}
	require.NotEmpty(t, sources)
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.ReplaceAll(line, fallbackSimpler, "")
			require.False(t, banned.MatchString(line), "%s:%d recommends changing an image: %s", path, i+1, strings.TrimSpace(line))
		}
	}
	// The instructions for every provider fixture say nothing like it either.
	for _, tc := range []struct{ fixture, provider, language, framework string }{
		{"gitlab-ruby", "gitlab", "ruby", "rspec"},
		{"gitlab-javascript", "gitlab", "javascript", "jest"},
		{"circleci-ruby", "circleci", "ruby", "rspec"},
		{"circleci-javascript", "circleci", "javascript", "jest"},
		{"buildkite-ruby", "buildkite", "ruby", "rspec"},
		{"buildkite-javascript", "buildkite", "javascript", "jest"},
		{"jenkins-container-ruby", "jenkins", "ruby", "rspec"},
		{"jenkins-container-javascript", "jenkins", "javascript", "jest"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			root := copyFixture(t, tc.fixture)
			t.Chdir(root)
			useCIProvider(t, tc.provider)
			provider, err := currentCIProvider(root)
			require.NoError(t, err)
			text := providerSetupInstructions(root, provider, tc.language, tc.framework, "", true)
			require.False(t, banned.MatchString(text), "%s: %s", tc.fixture, banned.FindString(text))
		})
	}
}
