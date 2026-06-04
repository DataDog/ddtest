package runmetadata

import "testing"

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
