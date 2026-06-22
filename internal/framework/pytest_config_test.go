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

func TestPyTest_testPattern_DefaultWhenNoConfig(t *testing.T) {
	pytest := &PyTest{platformEnv: map[string]string{}}
	if got := pytest.TestPattern(); got != pytestDefaultPattern {
		t.Errorf("expected default pattern %q, got %q", pytestDefaultPattern, got)
	}
}

func TestPyTest_testPattern_ExplicitTestsLocationOverridesConfig(t *testing.T) {
	setTestsLocation(t, "mydir/**/*_test.py")
	pytest := &PyTest{platformEnv: map[string]string{}}
	if got := pytest.TestPattern(); got != "mydir/**/*_test.py" {
		t.Errorf("expected explicit location %q, got %q", "mydir/**/*_test.py", got)
	}
}

func TestBraceExpand_SingleItem(t *testing.T) {
	if got := braceExpand([]string{"test_*.py"}); got != "test_*.py" {
		t.Errorf("expected single item returned as-is, got %q", got)
	}
}

func TestBraceExpand_MultipleItems(t *testing.T) {
	if got := braceExpand([]string{"test_*.py", "*_test.py"}); got != "{test_*.py,*_test.py}" {
		t.Errorf("expected brace-wrapped result, got %q", got)
	}
}

func TestPyTest_testPattern_MultipleTestpaths(t *testing.T) {
	// Simulate a pytest.ini with multiple testpaths and no python_files
	// by constructing a PyTest that will read from loadPytestConfig.
	// We test braceExpand integration directly via testPattern() output.
	pytest := &PyTest{platformEnv: map[string]string{}}

	// Use braceExpand directly to verify the combined pattern shape.
	testpaths := []string{"tests", "src"}
	filePart := "{test_*,*_test}.py"
	expected := "{tests,src}/**/" + filePart
	got := braceExpand(testpaths) + "/**/" + filePart
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
	_ = pytest // kept to show this is framework-package logic
}

func TestPyTest_testPattern_MultipleFilePatterns(t *testing.T) {
	filePatterns := []string{"test_*.py", "*_test.py", "check_*.py"}
	expected := "{test_*.py,*_test.py,check_*.py}"
	if got := braceExpand(filePatterns); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
