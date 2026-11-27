package bazeldoctor

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// configValue is config[key]; a missing key raises in Python.
func (d *doctor) configValue(key string) any {
	value, ok := d.config.Get(key)
	if !ok {
		unsupported("config has no " + key)
	}
	return value
}

// configGet is config.get(key, fallback).
func (d *doctor) configGet(key string, fallback any) any {
	return d.config.Lookup(key, fallback)
}

// typeName is type(value).__name__ for a JSON value.
func typeName(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case pyjson.Number:
		if _, isInt, _ := pyjson.Int(typed); isInt {
			return "int"
		}
		return "float"
	case []any:
		return "list"
	case *pyjson.Object:
		return "dict"
	}
	return "object"
}

// toRepr converts a JSON value to what pyformat.Repr prints as Python.
func toRepr(value any) any {
	switch typed := value.(type) {
	case pyjson.Number:
		if integer, isInt, err := pyjson.Int(typed); err == nil && isInt {
			return integer
		} else if isInt {
			unsupported("huge integer")
		}
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil && parsed == 0 {
			unsupported("number " + string(typed))
		}
		return parsed
	case []any:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = toRepr(item)
		}
		return items
	case *pyjson.Object:
		values := map[string]any{}
		for i, key := range typed.Keys {
			values[key] = toRepr(typed.Values[i])
		}
		return &pyformat.Ordered{Keys: typed.Keys, Values: values}
	}
	return value
}

func repr(value any) string {
	return pyformat.Repr(toRepr(value))
}

// equalsOne is value == 1 in Python, which holds for 1, 1.0 and True.
func equalsOne(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case pyjson.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		return err == nil && parsed == 1
	}
	return false
}

// pyEqual is == between two JSON values where Python compares a string.
func stringEquals(value any, text string) bool {
	typed, ok := value.(string)
	return ok && typed == text
}

// loadJSON is _load_json: a missing file fails; other errors and invalid
// JSON are reported with Python's exception text, by the Python doctor.
func (d *doctor) loadJSON(file string) any {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			d.out.fail("missing JSON file: " + file)
		}
		unsupported("read " + file + ": " + err.Error())
	}
	return parseDocument(file, data)
}

// parseDocument decodes a file as Python's utf-8-sig and json.load do.
func parseDocument(file string, data []byte) any {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		unsupported("invalid UTF-8 in " + file)
	}
	value, err := pyjson.Parse(data)
	if err != nil {
		unsupported("JSON in " + file)
	}
	return value
}

// validateExpectedTargetLabel is _validate_expected_target_label.
func (d *doctor) validateExpectedTargetLabel(value any) string {
	label, ok := value.(string)
	if !ok {
		d.out.fail("expected target labels must be strings, got " + typeName(value))
	}
	switch {
	case label != pythonStrip(label):
		d.out.fail("expected target label must not contain surrounding whitespace: " + repr(label))
	case strings.ContainsFunc(label, func(r rune) bool { return r <= 32 || r == 127 }):
		d.out.fail("expected target label must not contain whitespace or control characters: " + repr(label))
	case strings.HasPrefix(label, "@"):
		d.out.fail("expected_targets does not support external labels, got " + repr(label))
	case !strings.HasPrefix(label, "//"):
		d.out.fail("expected_targets only supports local labels, got " + repr(label))
	}
	parts := strings.Split(label[2:], ":")
	if len(parts) != 2 {
		d.out.fail("expected target label must include exactly one ':', got " + repr(label))
	}
	pkg, target := parts[0], parts[1]
	if target == "" {
		d.out.fail("expected target label has empty target name: " + repr(label))
	}
	badComponent := func(text string) bool {
		for _, component := range strings.Split(text, "/") {
			if component == "" || component == "." || component == ".." {
				return true
			}
		}
		return false
	}
	if pkg != "" && badComponent(pkg) {
		d.out.fail("expected target label has unsupported package path: " + repr(label))
	}
	if badComponent(target) {
		d.out.fail("expected target label has unsupported target path: " + repr(label))
	}
	if strings.Contains(label, "*") || strings.Contains(label, "...") || strings.Contains(label, `\`) {
		d.out.fail("expected target label must be fully expanded, got " + repr(label))
	}
	return label
}

// pythonStrip is str.strip() for the doctor's inputs.
func pythonStrip(text string) string {
	return strings.TrimFunc(text, isPythonSpace)
}

func isPythonSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f,
		0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// expectedTargetRoot is _expected_target_root.
func expectedTargetRoot(testlogs, label string) string {
	body := label[2:]
	pkg, target, _ := strings.Cut(body, ":")
	parts := []string{testlogs}
	for _, part := range strings.Split(pkg, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return joinPath(parts[0], append(parts[1:], target)...)
}

// loadExpectedTargetsFile is _load_expected_targets_file.
func (d *doctor) loadExpectedTargetsFile(file string) []string {
	info, ok := stat(file, true)
	if !ok || !info.Mode().IsRegular() {
		d.out.fail("expected_targets_file does not exist or is not a file: " + file)
	}
	value, isObject := d.loadJSON(file).(*pyjson.Object)
	if !isObject {
		d.out.fail("expected_targets_file root must be a JSON object")
	}
	keys := append([]string{}, value.Keys...)
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "schema_version" || keys[1] != "targets" {
		d.out.fail("expected_targets_file must contain exactly schema_version and targets; got " + pyformat.Repr(keys))
	}
	if version := value.Lookup("schema_version", nil); !equalsOne(version) {
		d.out.fail("expected_targets_file schema_version must be 1, got " + repr(version))
	}
	targets, isList := value.Lookup("targets", nil).([]any)
	if !isList {
		d.out.fail("expected_targets_file targets must be a JSON array")
	}
	labels := make([]string, len(targets))
	for i, target := range targets {
		labels[i] = d.validateExpectedTargetLabel(target)
	}
	if len(uniqueStrings(labels)) != len(labels) {
		d.out.fail("expected_targets_file contains duplicate target labels")
	}
	if !sort.StringsAreSorted(labels) {
		d.out.fail("expected_targets_file targets must be sorted")
	}
	return labels
}

// resolveExpectedTargets is _resolve_expected_targets.
func (d *doctor) resolveExpectedTargets(runtimeSelection bool) ([]string, string) {
	staticTargets, isList := d.configValue("expected_targets").([]any)
	if !isList {
		unsupported("expected_targets is not a list")
	}
	static := make([]string, len(staticTargets))
	for i, label := range staticTargets {
		static[i] = d.validateExpectedTargetLabel(label)
	}
	configured, source := static, "discovery"
	if len(static) > 0 {
		source = "static"
	}
	if d.request.ExpectedTargetsFile != "" {
		dynamic := d.loadExpectedTargetsFile(d.request.ExpectedTargetsFile)
		if len(static) > 0 && !sameSet(static, dynamic) {
			d.out.fail("static expected_targets and expected_targets_file contain different target sets")
		}
		if len(static) > 0 {
			configured, source = sortedUnique(static), "static_and_file"
		} else {
			configured, source = dynamic, "file"
		}
	}
	runtime := make([]string, len(d.request.Args.ExpectedTargets))
	for i, label := range d.request.Args.ExpectedTargets {
		runtime[i] = d.validateExpectedTargetLabel(label)
	}
	if len(uniqueStrings(runtime)) != len(runtime) {
		d.out.fail("runtime --expected-target arguments contain duplicate target labels")
	}
	var fileTargets []string
	if d.request.Args.ExpectedTargetsFile != "" {
		fileTargets = d.loadExpectedTargetsFile(d.request.Args.ExpectedTargetsFile)
	}
	runtime = sortedUnique(append(runtime, fileTargets...))
	if len(runtime) > 0 {
		if len(configured) > 0 && !sameSet(configured, runtime) {
			d.out.fail("configured and runtime expected targets contain different target sets")
		}
		if len(configured) > 0 {
			return runtime, "runtime_and_configured"
		}
		return runtime, "runtime"
	}
	if runtimeSelection {
		d.out.fail("runtime selection requires at least one --expected-target argument or a nonempty --expected-targets-file")
	}
	return configured, source
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

func sortedUnique(values []string) []string {
	unique := uniqueStrings(values)
	sort.Strings(unique)
	if unique == nil {
		return []string{}
	}
	return unique
}

func sameSet(a, b []string) bool {
	x, y := sortedUnique(a), sortedUnique(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

const apparentRepoNameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._+-"

type runtimeContext struct{ repo, path string }

// loadRuntimeContexts is _load_runtime_contexts.
func (d *doctor) loadRuntimeContexts() []runtimeContext {
	var contexts []runtimeContext
	seenRepos, seenPaths := map[string]bool{}, map[string]bool{}
	for _, entry := range d.request.Args.ContextEntries {
		repo, raw, found := strings.Cut(entry, "=")
		if !found || raw == "" {
			d.out.fail("--context-entry must use <apparent-repo-name>=<context-json-path>")
		}
		if repo == "" || strings.ContainsFunc(repo, func(r rune) bool { return !strings.ContainsRune(apparentRepoNameChars, r) }) {
			d.out.fail("context entry has invalid apparent repository name: " + repr(repo))
		}
		if seenRepos[repo] {
			d.out.fail("duplicate --context-entry repository name: " + repr(repo))
		}
		contextPath := resolvePath(expandUser(raw))
		if pathName(contextPath) != "context.json" || !isFile(contextPath) {
			d.out.fail("context entry for " + repr(repo) + " must reference an existing regular context.json file")
		}
		if seenPaths[contextPath] {
			d.out.fail("duplicate --context-entry path: " + contextPath)
		}
		telemetryPath := joinPath(pathParent(contextPath), "telemetry_facts.json")
		if !isFile(telemetryPath) {
			d.out.fail("context entry for " + repr(repo) + " is missing sibling telemetry_facts.json")
		}
		context, isObject := d.loadJSON(contextPath).(*pyjson.Object)
		if !isObject {
			d.out.fail("runtime context for " + repr(repo) + " must be a JSON object")
		}
		contextRepo := context.Lookup("topt.sync.repository_name", nil)
		service := context.Lookup("service.name", nil)
		runtime := context.Lookup("runtime.name", nil)
		if !stringEquals(contextRepo, repo) {
			d.out.fail("runtime context repository mismatch: argument=" + repr(repo) + " context=" + repr(contextRepo))
		}
		serviceName, _ := service.(string)
		if serviceName == "" {
			d.out.fail("runtime context for " + repr(repo) + " is missing service.name")
		}
		runtimeName, _ := runtime.(string)
		if runtimeName == "" {
			d.out.fail("runtime context for " + repr(repo) + " is missing runtime.name")
		}
		telemetry, isObject := d.loadJSON(telemetryPath).(*pyjson.Object)
		if !isObject {
			d.out.fail("telemetry facts for " + repr(repo) + " must be a JSON object")
		}
		if !equalsOne(telemetry.Lookup("schema_version", nil)) {
			d.out.fail("telemetry facts for " + repr(repo) + " must use schema_version 1")
		}
		_, countsList := telemetry.Lookup("counts", nil).([]any)
		_, distributionsList := telemetry.Lookup("distributions", nil).([]any)
		if !countsList || !distributionsList {
			d.out.fail("telemetry facts for " + repr(repo) + " has an invalid facts schema")
		}
		if !stringEquals(telemetry.Lookup("service_name", nil), serviceName) {
			d.out.fail("runtime context and telemetry service mismatch for " + repr(repo))
		}
		if !stringEquals(telemetry.Lookup("runtime_name", nil), runtimeName) {
			d.out.fail("runtime context and telemetry runtime mismatch for " + repr(repo))
		}
		seenRepos[repo], seenPaths[contextPath] = true, true
		contexts = append(contexts, runtimeContext{repo, contextPath})
	}
	sort.SliceStable(contexts, func(a, b int) bool { return contexts[a].repo < contexts[b].repo })
	return contexts
}

// manifestContexts is _load_contexts for the configured context manifest.
func (d *doctor) manifestContexts() []runtimeContext {
	manifest := d.request.ContextManifest
	if manifest == nil || !exists(manifest.Path) {
		return nil
	}
	data, err := os.ReadFile(manifest.Path)
	if err != nil || !utf8.Valid(data) {
		unsupported("context manifest " + manifest.Path)
	}
	var contexts []runtimeContext
	for _, line := range splitLines(string(data)) {
		if pythonStrip(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			d.out.fail("invalid context manifest line in " + manifest.Path + ": " + repr(line))
		}
		resolved, ok := manifest.Resolved[line]
		if !ok {
			unsupported("unresolved context manifest line")
		}
		contexts = append(contexts, runtimeContext{parts[0], resolved})
	}
	return contexts
}

// validateGitMetadata is _validate_git_metadata_contexts.
func (d *doctor) validateGitMetadata(contexts []runtimeContext) {
	if len(contexts) == 0 {
		d.out.fail("require_git_metadata=True but no context.json was provided in data")
	}
	for _, context := range contexts {
		document, isObject := d.loadJSON(context.path).(*pyjson.Object)
		if !isObject {
			unsupported("context " + context.path + " is not an object")
		}
		var missing []string
		if !pyjson.Truthy(document.Lookup("git.repository_url", nil)) {
			missing = append(missing, "git.repository_url")
		}
		if !pyjson.Truthy(document.Lookup("git.commit.sha", nil)) {
			missing = append(missing, "git.commit.sha")
		}
		if !pyjson.Truthy(document.Lookup("git.branch", nil)) && !pyjson.Truthy(document.Lookup("git.tag", nil)) {
			missing = append(missing, "git.branch or git.tag")
		}
		if len(missing) > 0 {
			d.out.fail("context " + context.repo + " is missing required git metadata: " + strings.Join(missing, ", "))
		}
	}
}

// splitLines is str.splitlines().
func splitLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, text[start:i])
			start = i + size
		case '\r':
			lines = append(lines, text[start:i])
			if strings.HasPrefix(text[i+1:], "\n") {
				size++
			}
			start = i + size
		}
		i += size
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

var forbiddenTestEnv = regexp.MustCompile(`--test_env(?:=|[\t\n\v\f\r \x{1c}-\x{1f}\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}` +
	`\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]+)(DD_GIT_[A-Z0-9_]*|DD_API_KEY|DD_SITE|DD_TEST_OPTIMIZATION_AGENT_URL|` +
	`DD_TEST_OPTIMIZATION_AGENTLESS_URL)`)

// validateBazelrc is _validate_bazelrc.
func (d *doctor) validateBazelrc() {
	var candidates []string
	candidates = append(candidates, globNames(d.workspace, ".bazelrc*")...)
	tools := joinPath(d.workspace, "tools", "bazelrc")
	if exists(tools) {
		candidates = append(candidates, globNames(tools, "*.bazelrc")...)
	}
	candidates = uniqueStrings(candidates)
	sort.Slice(candidates, func(a, b int) bool { return lessPath(candidates[a], candidates[b]) })
	for _, candidate := range candidates {
		if !isFile(candidate) {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err != nil {
			unsupported("read " + candidate)
		}
		text := strings.ToValidUTF8(string(data), "�")
		for number, line := range splitLines(text) {
			active, _, _ := strings.Cut(line, "#")
			if match := forbiddenTestEnv.FindStringSubmatch(active); match != nil {
				d.out.fail(candidate + ":" + strconv.Itoa(number+1) + " sets " + match[1] + " with --test_env; " +
					"use --repo_env for sync metadata and pass upload credentials only to the uploader runtime")
			}
		}
	}
}

// globNames is Path(directory).glob(pattern) for a pattern without
// separators: every entry, hidden ones included, whose name matches.
func globNames(directory, pattern string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if matched, _ := path.Match(pattern, entry.Name()); matched {
			names = append(names, joinPath(directory, entry.Name()))
		}
	}
	return names
}
