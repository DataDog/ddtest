package framework

import (
	"bufio"
	"bytes"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// pytestConfig holds the subset of pytest config relevant for test file discovery.
type pytestConfig struct {
	Testpaths   []string
	PythonFiles []string
}

// loadPytestConfig reads testpaths and python_files from the first pytest config
// file found, checking in pytest's own precedence order.
// Returns a zero-value config when no file is found or no relevant keys are set.
func loadPytestConfig() pytestConfig {
	if data, err := os.ReadFile("pytest.ini"); err == nil {
		if cfg, ok := parsePytestIni(data, "pytest"); ok {
			return cfg
		}
	}
	if data, err := os.ReadFile("pyproject.toml"); err == nil {
		if cfg, ok := parsePyprojectToml(data); ok {
			return cfg
		}
	}
	if data, err := os.ReadFile("tox.ini"); err == nil {
		if cfg, ok := parsePytestIni(data, "pytest"); ok {
			return cfg
		}
	}
	if data, err := os.ReadFile("setup.cfg"); err == nil {
		if cfg, ok := parsePytestIni(data, "tool:pytest"); ok {
			return cfg
		}
	}
	return pytestConfig{}
}

// parsePytestIni extracts testpaths and python_files from an INI-format config.
// section is the section name to look for (e.g. "pytest" or "tool:pytest").
// Values may be space-separated on the same line, or newline-indented continuations.
func parsePytestIni(data []byte, section string) (pytestConfig, bool) {
	var cfg pytestConfig
	inSection := false
	var currentKey string
	var currentValues []string

	flush := func() {
		switch currentKey {
		case "testpaths":
			cfg.Testpaths = currentValues
		case "python_files":
			cfg.PythonFiles = currentValues
		}
		currentKey = ""
		currentValues = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flush()
			inSection = trimmed[1:len(trimmed)-1] == section
			continue
		}

		if !inSection {
			continue
		}

		// Continuation lines are indented
		if line[0] == ' ' || line[0] == '\t' {
			currentValues = append(currentValues, strings.Fields(trimmed)...)
			continue
		}

		idx := strings.IndexByte(trimmed, '=')
		if idx < 0 {
			continue
		}
		flush()
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])
		if key == "testpaths" || key == "python_files" {
			currentKey = key
			if value != "" {
				currentValues = strings.Fields(value)
			}
		}
	}
	flush()

	return cfg, len(cfg.Testpaths) > 0 || len(cfg.PythonFiles) > 0
}

type pyprojectTomlFile struct {
	Tool struct {
		Pytest struct {
			IniOptions struct {
				Testpaths   []string `toml:"testpaths"`
				PythonFiles []string `toml:"python_files"`
			} `toml:"ini_options"`
		} `toml:"pytest"`
	} `toml:"tool"`
}

func parsePyprojectToml(data []byte) (pytestConfig, bool) {
	var parsed pyprojectTomlFile
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return pytestConfig{}, false
	}
	opts := parsed.Tool.Pytest.IniOptions
	if len(opts.Testpaths) == 0 && len(opts.PythonFiles) == 0 {
		return pytestConfig{}, false
	}
	return pytestConfig{
		Testpaths:   opts.Testpaths,
		PythonFiles: opts.PythonFiles,
	}, true
}
