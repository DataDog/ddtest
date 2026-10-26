package utils

import (
	"net/url"
	"strings"
)

// FileURL returns the file: URL of an absolute path. Node resolves --import
// values as URLs, so a Windows drive path such as C:\x.mjs would be read as
// the URL scheme "c:"; a file URL works on every platform.
func FileURL(absolutePath string) string {
	slashPath := absolutePath
	// A backslash is a valid filename character in an absolute Unix path.
	if !strings.HasPrefix(absolutePath, "/") {
		slashPath = strings.ReplaceAll(absolutePath, `\`, "/")
	}
	if strings.HasPrefix(absolutePath, `\\`) {
		hostAndPath := strings.TrimPrefix(slashPath, "//")
		host, path, _ := strings.Cut(hostAndPath, "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + path}).String()
	}
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}
