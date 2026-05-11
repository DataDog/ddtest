package runner

import (
	"os"
	"regexp"
	"strings"
)

var repositoryNameRegex = regexp.MustCompile(`(?m)/([a-zA-Z0-9\-_.]+)$`)

func resolveServiceName(repositoryURL string) string {
	if service := os.Getenv("DD_SERVICE"); service != "" {
		return service
	}
	return serviceNameFromRepositoryURL(repositoryURL)
}

func serviceNameFromRepositoryURL(repositoryURL string) string {
	normalizedRepositoryURL := strings.TrimRight(repositoryURL, "/")
	matches := repositoryNameRegex.FindStringSubmatch(normalizedRepositoryURL)
	if len(matches) > 1 {
		return strings.TrimSuffix(matches[1], ".git")
	}
	return normalizedRepositoryURL
}
