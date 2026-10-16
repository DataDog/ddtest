package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

// Runtime details describe the environment selected before instrumentation.
// The presentation is shared by platforms; selecting a runtime stays platform-specific.
type environmentInfo struct {
	Source     string `json:"source"`
	Runtime    string `json:"runtime"`
	Executable string `json:"executable,omitempty"`
	Version    string `json:"version,omitempty"`
}

func (info environmentInfo) write(output io.Writer) {
	if info.Source == "" {
		return
	}
	_, _ = fmt.Fprintf(output, "Environment: %s\n", info.Source)
	if info.Executable != "" {
		_, _ = fmt.Fprintf(output, "  %s %s: %s\n", info.Runtime, info.Version, info.Executable)
	}
}

func environmentFinding(kind, title, detail, advice string) *reportCard {
	return &reportCard{
		Kind: kind, Scope: runFindingScope, Severity: "error", Title: title, Count: 1, Context: detail,
		Advices: []intake.Advice{{Finding: kind, Title: "Prepare the test environment", Text: advice}},
	}
}

// Use a private result file so startup hooks writing to stdout cannot corrupt it.
func probeEnvironment(ctx context.Context, command string, args []string, env map[string]string, result any) error {
	file, err := os.CreateTemp("", "ddtest-environment-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Close(); err != nil {
		return err
	}
	executor := &ext.DefaultCommandExecutor{}
	output, err := executor.CombinedOutput(ctx, command, append(args, file.Name()), env)
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", command, err, strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func nodeEnvironmentInfo(ctx context.Context, command string) environmentInfo {
	info := environmentInfo{Source: "Node.js from PATH", Runtime: "Node.js"}
	if strings.TrimSuffix(filepath.Base(command), ".exe") != "node" {
		command = "node"
	} else {
		info.Source = "Selected Node.js command"
	}
	_ = probeEnvironment(ctx, command, []string{"-e", `require('fs').writeFileSync(process.argv[1], JSON.stringify({executable: process.execPath, version: process.version}))`}, nil, &info)
	return info
}
