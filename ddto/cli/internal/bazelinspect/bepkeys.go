package bazelinspect

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The functions below follow the BEP helpers of the rules_test_optimization
// doctor, test_optimization_doctor.py, one for one. They take ASCII text
// without newlines: Python's str.lower, unquote and regular expressions treat
// other text differently, so the evaluation leaves it to the Python.

var (
	retryLogPattern     = regexp.MustCompile(`(?:^|/)test_attempts/attempt_([0-9]+)[.](?:log|xml)$`)
	retryOutputsPattern = regexp.MustCompile(`(?:^|/)test_attempts/attempt_[0-9]+[.]outputs(?:/|$)`)
	remoteSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://`)
	remoteDigestPattern = regexp.MustCompile(`^[0-9a-f]{32,}/[0-9]+$`)
)

// plainText reports whether text is ASCII without newlines.
func plainText(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf || text[i] == '\n' {
			return false
		}
	}
	return true
}

// StripFileURI is _strip_file_uri for callers outside this package; it
// fails for text that the Python reads in its own way.
func StripFileURI(value string) (string, error) {
	if !plainText(value) {
		return "", errUnsupported
	}
	return stripFileURI(value)
}

// stripFileURI is _strip_file_uri: the path of a file:// URI, as urlparse and
// unquote give it, with a host other than localhost kept as //host.
func stripFileURI(value string) (string, error) {
	if len(value) < 7 || !strings.EqualFold(value[:7], "file://") {
		return value, nil
	}
	// urlsplit drops tabs and carriage returns; newlines are not plain text.
	url := strings.ReplaceAll(strings.ReplaceAll(value, "\t", ""), "\r", "")[len("file:"):]
	end := len(url)
	if index := strings.IndexAny(url[2:], "/?#"); index >= 0 {
		end = index + 2
	}
	netloc, path := url[2:end], url[end:]
	if strings.ContainsAny(netloc, "[]") {
		// urlsplit validates bracketed hosts and may raise.
		return "", errUnsupported
	}
	path, _, _ = strings.Cut(path, "#")
	path, _, _ = strings.Cut(path, "?")
	path, err := unquote(path)
	if err != nil {
		return "", err
	}
	if netloc != "" && netloc != "localhost" {
		path = "//" + netloc + path
	}
	return path, nil
}

// unquote is urllib.parse.unquote for ASCII text: %XX escapes decode to UTF-8;
// a result that is not plain text is unsupported.
func unquote(text string) (string, error) {
	if !strings.Contains(text, "%") {
		return text, nil
	}
	decoded := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) && isHex(text[i+1]) && isHex(text[i+2]) {
			decoded = append(decoded, hexValue(text[i+1])<<4|hexValue(text[i+2]))
			i += 2
			continue
		}
		decoded = append(decoded, text[i])
	}
	result := string(decoded)
	if !plainText(result) {
		return "", errUnsupported
	}
	return result, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// purePosixParts is PurePosixPath(value): its root and its parts.
func purePosixParts(value string) (string, []string) {
	root, rest := "", value
	switch {
	case !strings.HasPrefix(value, "/"):
	case strings.HasPrefix(value, "//") && !strings.HasPrefix(value, "///"):
		root, rest = "//", value[2:]
	default:
		root, rest = "/", value[1:]
	}
	var parts []string
	for _, part := range strings.Split(rest, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return root, parts
}

// CanonicalOutputDirectoryKey is canonical_output_directory_key in
// bep_execution.py.
func CanonicalOutputDirectoryKey(value string) string {
	root, parts := purePosixParts(strings.ReplaceAll(value, `\`, "/"))
	posix := root + strings.Join(parts, "/")
	if posix == "" {
		posix = "."
	}
	name, parent := "", ""
	if len(parts) > 0 {
		name = parts[len(parts)-1]
	}
	if len(parts) > 1 {
		parent = parts[len(parts)-2]
	}
	if parent == "test_attempts" && retryOutputDirectory.MatchString(name) {
		return posix + "/test.outputs"
	}
	return posix
}

// BepTestOutputKey is _bep_test_output_key for callers outside this
// package; it fails for text that the Python reads in its own way.
func BepTestOutputKey(pathValue, artifactName string) (string, error) {
	if !plainText(pathValue) || !plainText(artifactName) {
		return "", errUnsupported
	}
	return bepTestOutputKey(pathValue, artifactName)
}

// bepTestOutputKey is _bep_test_output_key: the bazel-testlogs-relative
// test.outputs key of a BEP reference, or "".
func bepTestOutputKey(pathValue, artifactName string) (string, error) {
	if pathValue == "" {
		return "", nil
	}
	normalized, err := stripFileURI(pathValue)
	if err != nil {
		return "", err
	}
	if normalized, err = unquote(normalized); err != nil {
		return "", err
	}
	normalized = strings.ReplaceAll(normalized, `\`, "/")
	if index := strings.LastIndex(normalized, "/testlogs/"); index >= 0 {
		normalized = normalized[index+len("/testlogs/"):]
	} else if index := strings.LastIndex(normalized, "/bazel-testlogs/"); index >= 0 {
		normalized = normalized[index+len("/bazel-testlogs/"):]
	} else {
		normalized = strings.TrimLeft(normalized, "/")
	}
	for strings.HasPrefix(normalized, "./") {
		normalized = normalized[2:]
	}
	normalized = strings.TrimLeft(normalized, "/")

	if suffix, ok := strings.CutPrefix(artifactName, "test.outputs/"); ok && suffix != "" &&
		strings.HasSuffix(normalized, "/"+suffix) {
		root := strings.TrimRight(normalized[:len(normalized)-len(suffix)], "/")
		if key := CanonicalOutputDirectoryKey(root); strings.HasSuffix(key, "/test.outputs") {
			return strings.TrimLeft(key, "/"), nil
		}
	}

	const marker = "/test.outputs/"
	retryLog := retryLogPattern.FindStringSubmatchIndex(normalized)
	switch {
	case strings.HasSuffix(normalized, "/test.outputs"):
	case strings.HasSuffix(normalized, "/test.log") || strings.HasSuffix(normalized, "/test.xml"):
		normalized = normalized[:strings.LastIndex(normalized, "/")] + "/test.outputs"
	case retryLog != nil:
		normalized = normalized[:retryLog[0]] + "/test_attempts/attempt_" + normalized[retryLog[2]:retryLog[3]] +
			".outputs/test.outputs"
	case strings.HasSuffix(CanonicalOutputDirectoryKey(normalized), "/test.outputs"):
		normalized = CanonicalOutputDirectoryKey(normalized)
	case strings.Contains(normalized, marker):
		normalized = normalized[:strings.LastIndex(normalized, marker)] + "/test.outputs"
	case strings.HasSuffix(normalized, "/outputs.zip"):
		normalized = normalized[:strings.LastIndex(normalized, "/")] + "/test.outputs"
	default:
		retries := retryOutputsPattern.FindAllStringIndex(normalized, -1)
		if len(retries) == 0 {
			return "", nil
		}
		normalized = strings.TrimRight(normalized[:retries[len(retries)-1][1]], "/") + "/test.outputs"
	}
	return strings.TrimLeft(normalized, "/"), nil
}

// remoteOnlyReference is _is_remote_only_bep_reference.
func remoteOnlyReference(pathValue string) bool {
	if pathValue == "" {
		return false
	}
	lowered := strings.ToLower(pathValue)
	if strings.HasPrefix(lowered, "file://") {
		return false
	}
	return remoteSchemePattern.MatchString(lowered) || strings.HasPrefix(lowered, "blobs/") ||
		remoteDigestPattern.MatchString(lowered)
}

// trustedOutputKeyCandidate is _trusted_bep_output_key_candidate.
func trustedOutputKeyCandidate(value string) (bool, error) {
	stripped, err := stripFileURI(value)
	if err != nil {
		return false, err
	}
	normalized := strings.Trim(strings.ReplaceAll(stripped, `\`, "/"), "/")
	if normalized == "" || !strings.Contains(normalized, "/") || remoteOnlyReference(normalized) {
		return false, nil
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "testlogs" || part == "bazel-testlogs" {
			return true, nil
		}
	}
	return false, nil
}

// testOutputsArtifactHint is _bep_test_outputs_artifact_hint.
func testOutputsArtifactHint(pathValue string) (bool, error) {
	if pathValue == "" {
		return false, nil
	}
	stripped, err := stripFileURI(pathValue)
	if err != nil {
		return false, err
	}
	normalized := strings.ToLower(strings.ReplaceAll(stripped, `\`, "/"))
	return normalized == "test.outputs" || normalized == "outputs.zip" || strings.Contains(normalized, "/test.outputs/") ||
		strings.HasSuffix(normalized, "/test.outputs") || strings.HasSuffix(normalized, "/outputs.zip"), nil
}

// pathPrefixNameCandidate is _bep_path_prefix_name_candidate.
func pathPrefixNameCandidate(file any) string {
	object, ok := file.(*jsonObject)
	if !ok {
		return ""
	}
	prefix, isList := coalesced(object, "pathPrefix", "path_prefix", []any{}).([]any)
	name, isName := object.Lookup("name", nil).(string)
	if !isList || !isName || name == "" {
		return ""
	}
	var parts []string
	for _, part := range prefix {
		if text, ok := part.(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(append(parts, name), "/")
}

// stringField is a non-empty string field of an object, or "".
func stringField(file any, key string) string {
	if object, ok := file.(*jsonObject); ok {
		if text, ok := object.Lookup(key, nil).(string); ok {
			return text
		}
	}
	return ""
}

// fileReferenceCandidates is _bep_file_reference_candidates.
func fileReferenceCandidates(file any) []string {
	if text, ok := file.(string); ok {
		return []string{text}
	}
	if _, ok := file.(*jsonObject); !ok {
		return []string{}
	}
	values := []string{}
	for _, key := range []string{"uri", "name", "path"} {
		if value := stringField(file, key); value != "" {
			values = append(values, value)
		}
	}
	if reconstructed := pathPrefixNameCandidate(file); reconstructed != "" {
		values = append(values, reconstructed)
	}
	return values
}

// canonicalOutputKeyCandidates is _bep_canonical_output_key_candidates.
func canonicalOutputKeyCandidates(file any, candidates []string) ([]string, error) {
	var values []string
	add := func(value string) {
		if value != "" && !contains(values, value) {
			values = append(values, value)
		}
	}
	if _, ok := file.(*jsonObject); ok {
		add(pathPrefixNameCandidate(file))
		if path, ok := file.(*jsonObject).Lookup("path", nil).(string); ok {
			add(path)
		}
	}
	for _, candidate := range candidates {
		trusted, err := trustedOutputKeyCandidate(candidate)
		if err != nil {
			return nil, err
		}
		if trusted {
			add(candidate)
		}
	}
	return values, nil
}

// artifactFetchValue is _bep_artifact_fetch_value.
func artifactFetchValue(file any, candidates []string) string {
	if _, ok := file.(*jsonObject); ok {
		for _, key := range []string{"uri", "path"} {
			if value := stringField(file, key); value != "" {
				return value
			}
		}
		if reconstructed := pathPrefixNameCandidate(file); reconstructed != "" {
			return reconstructed
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// stageableCarrierHint is _bep_stageable_artifact_carrier_hint.
func stageableCarrierHint(file any, candidates []string) (bool, error) {
	var values []string
	if _, ok := file.(*jsonObject); ok {
		for _, value := range []string{stringField(file, "name"), stringField(file, "path"), pathPrefixNameCandidate(file)} {
			if value != "" {
				values = append(values, value)
			}
		}
		if uri := stringField(file, "uri"); uri != "" && !remoteOnlyReference(uri) {
			trusted, err := trustedOutputKeyCandidate(uri)
			if err != nil {
				return false, err
			}
			if trusted {
				values = append(values, uri)
			}
		}
	} else {
		values = candidates
	}
	for _, value := range values {
		stripped, err := stripFileURI(value)
		if err != nil {
			return false, err
		}
		normalized := strings.TrimRight(strings.ToLower(strings.ReplaceAll(stripped, `\`, "/")), "/")
		if normalized == "test.outputs" || normalized == "outputs.zip" || strings.HasSuffix(normalized, "/test.outputs") ||
			strings.HasSuffix(normalized, "/outputs.zip") {
			return true, nil
		}
	}
	return false, nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
