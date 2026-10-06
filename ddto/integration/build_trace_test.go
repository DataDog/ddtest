package integration

import (
	"regexp"
	"strings"
	"testing"
)

// go test -x prints quoted executable paths on Windows and when Unix paths
// contain spaces. Cache assertions must count those compilers too.
var compilerTracePattern = regexp.MustCompile(`[/\\]compile(?:\.exe)?["']?[ \t]`)

func compilerTraceLines(trace string) []string {
	var lines []string
	for _, line := range strings.Split(trace, "\n") {
		if compilerTracePattern.MatchString(line) {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestCompilerTraceLines(t *testing.T) {
	// The quoted Windows command is the format observed in the failed cache
	// assertion. Include wrapper arguments and paths containing spaces.
	compilers := []string{
		`/goroot/pkg/tool/linux_amd64/compile -o "$WORK/b001/_pkg_.a" -p testing`,
		`"/Go tools/pkg/tool/darwin_arm64/compile" -o "$WORK/b001/_pkg_.a" -p testing`,
		`"C:\\Program Files\\ddtest.exe" tool-overlay testify "C:\\plan.json" "C:\\Go\\pkg\\tool\\windows_amd64\\compile.exe" -o "$WORK\\b135\\_pkg_.a" -p github.com/stretchr/testify/suite`,
		`C:\Go\pkg\tool\windows_amd64\compile.exe -o archive -p testing`,
	}
	noise := []string{
		`WORK=/tmp/go-build123`,
		`/goroot/pkg/tool/linux_amd64/asm -o archive`,
		`"C:\\Go\\pkg\\tool\\windows_amd64\\link.exe" -o binary`,
		`/goroot/pkg/tool/linux_amd64/compile-other -o archive`,
		`cp compile.exe.go output.go`,
	}
	trace := strings.Join(append(append([]string{}, noise...), compilers...), "\n")
	got := compilerTraceLines(trace)
	if len(got) != len(compilers) {
		t.Fatalf("compiler count = %d, want %d: %q", len(got), len(compilers), got)
	}
	for i, line := range got {
		if line != compilers[i] {
			t.Fatalf("compiler line %d = %q, want %q", i, line, compilers[i])
		}
	}
}
