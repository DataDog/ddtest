package runmetadata

import (
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestServiceNameFromRepositoryURL(t *testing.T) {
	tests := []struct {
		name          string
		repositoryURL string
		want          string
	}{
		{
			name:          "repository URL without suffix",
			repositoryURL: "https://github.com/DataDog/ddtest",
			want:          "ddtest",
		},
		{
			name:          "repository URL with git suffix",
			repositoryURL: "https://github.com/DataDog/ddtest.git",
			want:          "ddtest",
		},
		{
			name:          "repository URL with trailing slash",
			repositoryURL: "ssh://host.xz/path/to/repo.git/",
			want:          "repo",
		},
		{
			name:          "repository URL with multiple trailing slashes",
			repositoryURL: "ssh://host.xz/path/to/repo.git///",
			want:          "repo",
		},
		{
			name:          "fallback without path separator",
			repositoryURL: "ddtest",
			want:          "ddtest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceNameFromRepositoryURL(tt.repositoryURL)
			if got != tt.want {
				t.Errorf("ServiceNameFromRepositoryURL(%q) = %q, want %q", tt.repositoryURL, got, tt.want)
			}
		})
	}
}

func TestResolveServiceNamePrefersDDService(t *testing.T) {
	t.Setenv("DD_SERVICE", "custom-service")

	got := ResolveServiceName("https://github.com/DataDog/ddtest.git")
	if got != "custom-service" {
		t.Errorf("ResolveServiceName() = %q, want %q", got, "custom-service")
	}
}

func TestNewRunInfo(t *testing.T) {
	info := New(map[string]string{
		constants.GitRepositoryURL: "https://github.com/DataDog/ddtest.git",
		constants.GitCommitSHA:     "abc123",
		constants.GitBranch:        "main",
	})

	if info.Service != "ddtest" || info.Repository != "https://github.com/DataDog/ddtest.git" ||
		info.Commit != "abc123" || info.Branch != "main" {
		t.Fatalf("New() = %+v", info)
	}
}

func TestRunInfoIsZero(t *testing.T) {
	if !(RunInfo{}).IsZero() {
		t.Fatal("empty RunInfo should be zero")
	}
	if (RunInfo{Service: "ddtest"}).IsZero() {
		t.Fatal("RunInfo with service should not be zero")
	}
	if (RunInfo{Repository: "https://github.com/DataDog/ddtest.git"}).IsZero() {
		t.Fatal("RunInfo with repository should not be zero")
	}
	if (RunInfo{Commit: "abc123"}).IsZero() {
		t.Fatal("RunInfo with commit should not be zero")
	}
	if (RunInfo{Branch: "main"}).IsZero() {
		t.Fatal("RunInfo with branch should not be zero")
	}
}
