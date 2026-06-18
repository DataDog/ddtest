package runmetadata

import (
	"os"
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/git"
)

var repositoryNameRegex = regexp.MustCompile(`(?m)/([a-zA-Z0-9\-_.]+)$`)

type RunInfo struct {
	Service    string `json:"service"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Branch     string `json:"branch"`
}

func New(ciTags map[string]string) RunInfo {
	repository := ciTags[git.GitRepositoryURL]
	return RunInfo{
		Service:    ResolveServiceName(repository),
		Repository: repository,
		Commit:     ciTags[git.GitCommitSHA],
		Branch:     ciTags[git.GitBranch],
	}
}

func (r RunInfo) IsZero() bool {
	return r.Service == "" &&
		r.Repository == "" &&
		r.Commit == "" &&
		r.Branch == ""
}

func ResolveServiceName(repositoryURL string) string {
	if service := os.Getenv("DD_SERVICE"); service != "" {
		return service
	}
	return ServiceNameFromRepositoryURL(repositoryURL)
}

func ServiceNameFromRepositoryURL(repositoryURL string) string {
	normalizedRepositoryURL := strings.TrimRight(repositoryURL, "/")
	matches := repositoryNameRegex.FindStringSubmatch(normalizedRepositoryURL)
	if len(matches) > 1 {
		return strings.TrimSuffix(matches[1], ".git")
	}
	return normalizedRepositoryURL
}
