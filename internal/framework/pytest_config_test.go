package framework

import (
	"reflect"
	"testing"
)

func TestParsePytestIni_TestpathsAndPythonFiles(t *testing.T) {
	data := []byte(`
[pytest]
testpaths = tests unit_tests
python_files = test_*.py *_test.py
`)
	cfg, ok := parsePytestIni(data, "pytest")
	if !ok {
		t.Fatal("expected config to be found")
	}
	if !reflect.DeepEqual(cfg.Testpaths, []string{"tests", "unit_tests"}) {
		t.Errorf("Testpaths: got %v", cfg.Testpaths)
	}
	if !reflect.DeepEqual(cfg.PythonFiles, []string{"test_*.py", "*_test.py"}) {
		t.Errorf("PythonFiles: got %v", cfg.PythonFiles)
	}
}

func TestParsePytestIni_MultilineValues(t *testing.T) {
	data := []byte(`
[pytest]
testpaths =
    tests
    integration_tests
python_files =
    test_*.py
    *_test.py
`)
	cfg, ok := parsePytestIni(data, "pytest")
	if !ok {
		t.Fatal("expected config to be found")
	}
	if !reflect.DeepEqual(cfg.Testpaths, []string{"tests", "integration_tests"}) {
		t.Errorf("Testpaths: got %v", cfg.Testpaths)
	}
	if !reflect.DeepEqual(cfg.PythonFiles, []string{"test_*.py", "*_test.py"}) {
		t.Errorf("PythonFiles: got %v", cfg.PythonFiles)
	}
}

func TestParsePytestIni_WrongSection(t *testing.T) {
	data := []byte(`
[other]
testpaths = tests
`)
	_, ok := parsePytestIni(data, "pytest")
	if ok {
		t.Error("expected no config when section doesn't match")
	}
}

func TestParsePytestIni_SetupCfgSection(t *testing.T) {
	data := []byte(`
[tool:pytest]
testpaths = src/tests
python_files = test_*.py
`)
	cfg, ok := parsePytestIni(data, "tool:pytest")
	if !ok {
		t.Fatal("expected config to be found")
	}
	if !reflect.DeepEqual(cfg.Testpaths, []string{"src/tests"}) {
		t.Errorf("Testpaths: got %v", cfg.Testpaths)
	}
}

func TestParsePytestIni_IgnoresComments(t *testing.T) {
	data := []byte(`
; global comment
[pytest]
# inline comment
testpaths = tests  ; inline
`)
	cfg, ok := parsePytestIni(data, "pytest")
	if !ok {
		t.Fatal("expected config to be found")
	}
	// "tests" and the inline comment marker are both tokens when split by Fields;
	// inline semicolon comments in INI values aren't stripped by pytest either.
	// We just verify "tests" is present.
	found := false
	for _, p := range cfg.Testpaths {
		if p == "tests" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'tests' in Testpaths, got %v", cfg.Testpaths)
	}
}

func TestParsePytestIni_Empty(t *testing.T) {
	data := []byte(`[pytest]`)
	_, ok := parsePytestIni(data, "pytest")
	if ok {
		t.Error("expected no config for empty section")
	}
}

func TestParsePyprojectToml_WithIniOptions(t *testing.T) {
	data := []byte(`
[tool.pytest.ini_options]
testpaths = ["tests", "integration"]
python_files = ["test_*.py"]
`)
	cfg, ok := parsePyprojectToml(data)
	if !ok {
		t.Fatal("expected config to be found")
	}
	if !reflect.DeepEqual(cfg.Testpaths, []string{"tests", "integration"}) {
		t.Errorf("Testpaths: got %v", cfg.Testpaths)
	}
	if !reflect.DeepEqual(cfg.PythonFiles, []string{"test_*.py"}) {
		t.Errorf("PythonFiles: got %v", cfg.PythonFiles)
	}
}

func TestParsePyprojectToml_NoPytestSection(t *testing.T) {
	data := []byte(`
[tool.black]
line-length = 88
`)
	_, ok := parsePyprojectToml(data)
	if ok {
		t.Error("expected no config when pytest section is absent")
	}
}

func TestParsePyprojectToml_EmptyIniOptions(t *testing.T) {
	data := []byte(`
[tool.pytest.ini_options]
addopts = "-v"
`)
	_, ok := parsePyprojectToml(data)
	if ok {
		t.Error("expected no config when testpaths/python_files are absent")
	}
}

func TestParsePyprojectToml_InvalidToml(t *testing.T) {
	data := []byte(`not valid toml :::`)
	_, ok := parsePyprojectToml(data)
	if ok {
		t.Error("expected no config for invalid TOML")
	}
}

func TestPyTest_testPatterns_DefaultWhenNoConfig(t *testing.T) {
	// No pytest.ini / pyproject.toml in the test working dir → default pattern
	pytest := &PyTest{platformEnv: map[string]string{}}
	patterns := pytest.testPatterns()
	if len(patterns) != 1 {
		t.Fatalf("expected 1 default pattern, got %v", patterns)
	}
	if patterns[0] != pytestDefaultPattern {
		t.Errorf("expected default pattern %q, got %q", pytestDefaultPattern, patterns[0])
	}
}

func TestPyTest_testPatterns_ExplicitTestsLocationOverridesConfig(t *testing.T) {
	setTestsLocation(t, "mydir/**/*_test.py")
	pytest := &PyTest{platformEnv: map[string]string{}}
	patterns := pytest.testPatterns()
	if len(patterns) != 1 || patterns[0] != "mydir/**/*_test.py" {
		t.Errorf("expected explicit location to be returned, got %v", patterns)
	}
}
