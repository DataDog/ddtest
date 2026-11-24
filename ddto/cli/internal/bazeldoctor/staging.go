package bazeldoctor

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// Staging copies BEP test.outputs carriers into owned directories, as the
// Python doctor's _stage_bep_artifacts does. Errors whose Python message
// embeds an exception's text (OSError, zipfile, urllib) are left to the
// Python doctor; the others keep Python's exact text.

const (
	stagingMarker = ".dd-topt-bep-staged"
	maxTreeFiles  = 10000
	maxTreeBytes  = 512 * 1024 * 1024
	httpAttempts  = 3
	// Each retry waits twice as long as the one before.
	httpBackoffFirst = 250 * time.Millisecond
)

// stagedArtifact is StagedBepArtifact.
type stagedArtifact struct {
	label, outputKey, outputDir, stagingRoot, fetchValue string
	downloaded, remoteOnly                               bool
}

// stageError is BepArtifactStageError.
type stageError struct{ message string }

func (e stageError) Error() string { return e.message }

func stageErrorf(parts ...string) error { return stageError{strings.Join(parts, "")} }

// stripFileURI is _strip_file_uri.
func stripFileURI(value string) (string, error) {
	return bazelinspect.StripFileURI(value)
}

// isHTTPReference is _is_http_artifact_reference.
func isHTTPReference(value string) bool {
	if value != "" && value[0] <= ' ' {
		unsupported("artifact reference with leading whitespace")
	}
	if !plainASCII(value) {
		unsupported("non-ASCII artifact reference")
	}
	cleaned := strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	scheme, rest, found := strings.Cut(cleaned, ":")
	if !found || (strings.ToLower(scheme) != "http" && strings.ToLower(scheme) != "https") ||
		!strings.HasPrefix(rest, "//") {
		return false
	}
	netloc := rest[2:]
	if index := strings.IndexAny(netloc, "/?#"); index >= 0 {
		netloc = netloc[:index]
	}
	if strings.ContainsAny(netloc, "[]") {
		unsupported("bracketed host in an HTTP artifact reference")
	}
	return netloc != ""
}

// stagingDir is _artifact_staging_dir.
func (d *doctor) stagingDir() string {
	raw := d.request.Args.ArtifactStagingDir
	if raw == "" {
		raw = ".topt/bep-artifacts"
	}
	path := expandUser(purePath(raw))
	if !filepath.IsAbs(path) {
		path = joinPath(d.workspace, path)
	}
	return resolvePath(path)
}

// selectedArtifactOutputs is _selected_bep_artifact_outputs.
func selectedArtifactOutputs(f *freshness, remoteArtifacts string) map[pair]bool {
	selected := map[pair]bool{}
	for _, ref := range f.references {
		if ref.cached || !ref.stageableCarrier || ref.outputKey == "" {
			continue
		}
		if ref.remoteOnly && remoteArtifacts == "disabled" {
			continue
		}
		selected[pair{ref.label, ref.outputKey}] = true
	}
	return selected
}

// blockedArtifactLabels is _blocked_bep_artifact_labels.
func blockedArtifactLabels(f *freshness, remoteArtifacts string) map[string]bool {
	blocked := map[string]bool{}
	if remoteArtifacts != "download" {
		return blocked
	}
	for _, ref := range f.references {
		if !ref.cached && ref.testOutputsHint && ref.outputKey == "" {
			blocked[ref.label] = true
		}
	}
	return blocked
}

// applyStaged is _apply_staged_bep_artifacts_to_freshness.
func applyStaged(f *freshness, staged []stagedArtifact) {
	pairs := map[pair]bool{}
	for _, item := range staged {
		pairs[pair{item.label, item.outputKey}] = true
	}
	for p := range pairs {
		if f.cached[p] {
			f.ambiguousOutputs[p.key] = true
		}
		f.eligible[p] = true
	}
	if len(pairs) == 0 {
		return
	}
	kept := f.remoteOnly[:0:0]
	for _, remote := range f.remoteOnly {
		if !pairs[pair{remote.label, remote.outputKey}] {
			kept = append(kept, remote)
		}
	}
	f.remoteOnly = kept
}

// mergeStaged is _merge_staged_output_dirs.
func (d *doctor) mergeStaged(dirs []string, staged []stagedArtifact, selected map[pair]bool, blocked map[string]bool,
	targets map[string]string) ([]string, map[string]string) {
	selectedKeys := map[string]bool{}
	for p := range selected {
		selectedKeys[p.key] = true
	}
	merged := []string{}
	mergedTargets := map[string]string{}
	for dir, label := range targets {
		mergedTargets[dir] = label
	}
	for _, dir := range dirs {
		if selectedKeys[d.localOutputKey(dir)] {
			delete(mergedTargets, d.resolved(dir))
			continue
		}
		if label, ok := d.outputDirTargetLabel(dir, mergedTargets); ok && blocked[label] {
			delete(mergedTargets, d.resolved(dir))
			continue
		}
		merged = append(merged, dir)
	}
	for _, item := range staged {
		merged = append(merged, item.outputDir)
		mergedTargets[d.resolved(item.outputDir)] = item.label
	}
	return merged, mergedTargets
}

// outputDirTargetLabel is _output_dir_target_label; false stands for None.
func (d *doctor) outputDirTargetLabel(dir string, targets map[string]string) (string, bool) {
	fallback, found := targets[d.resolved(dir)]
	for _, file := range d.outputFilesOf(dir).metadata {
		if document, isObject := d.loadMetadata(file).(*pyjson.Object); isObject {
			if label := metadataTargetLabel(document, fallback); label != "" || found {
				return label, true
			}
			return "", false
		}
	}
	return fallback, found
}

// stageArtifacts is _stage_bep_artifacts without owned run roots: it removes
// its run directory when it stages nothing or stops.
func (d *doctor) stageArtifacts(f *freshness, base string, selectedLabels map[string]bool) []stagedArtifact {
	remoteArtifacts := d.request.Args.RemoteArtifacts
	runDir := createRunDir(base)
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = os.RemoveAll(runDir)
			panic(recovered)
		}
	}()
	warned := map[string]bool{}
	warnOnce := func(message string) {
		if !warned[message] {
			warned[message] = true
			d.out.warn(message)
		}
	}
	reportError := func(err error) {
		var staging stageError
		if !errors.As(err, &staging) {
			unsupported("staging error: " + err.Error())
		}
		if remoteArtifacts == "required" {
			d.out.fail(staging.message)
		}
		warnOnce(staging.message)
	}
	var candidates []reference
	for _, ref := range f.references {
		if (selectedLabels == nil || selectedLabels[ref.label]) && !f.ambiguousLabels[ref.label] &&
			!f.ambiguousOutputs[ref.outputKey] {
			candidates = append(candidates, ref)
		}
	}
	refs, ambiguous := d.dedupeReferences(candidates, remoteArtifacts)
	for _, p := range ambiguous {
		f.ambiguousOutputs[p.key] = true
	}
	if len(ambiguous) > 0 {
		message := "BEP has multiple distinct stageable carriers for " + ambiguous[0].label + " " + ambiguous[0].key
		if remoteArtifacts == "required" {
			d.out.fail(message)
		}
		warnOnce(message)
	}
	var staged []stagedArtifact
	for _, ref := range refs {
		d.checkInterrupted()
		if selectedLabels != nil && !selectedLabels[ref.label] || ref.cached {
			continue
		}
		display := displayArtifactReference(ref.fetchValue)
		if ref.testOutputsHint && ref.outputKey == "" {
			message := "BEP artifact for " + ref.label + " has no mappable test.outputs key: " + display
			if remoteArtifacts == "required" {
				d.out.fail(message)
			}
			if remoteArtifacts == "download" {
				warnOnce(message)
			}
			continue
		}
		if !ref.stageableCarrier {
			if ref.remoteOnly && !ref.testOutputsHint {
				continue
			}
			if ref.remoteOnly && remoteArtifacts == "required" {
				d.out.fail("BEP artifact for " + ref.label + " is remote-only but not a supported " +
					"test.outputs carrier: " + display)
			}
			if ref.remoteOnly && remoteArtifacts == "download" {
				warnOnce("BEP artifact for " + ref.label + " is remote-only but not stageable: " + display)
			}
			continue
		}
		downloader := d.request.Args.BepArtifactDownloader
		if ref.remoteOnly {
			if remoteArtifacts == "disabled" {
				warnOnce("BEP artifact for " + ref.label + " is remote-only but remote artifact " +
					"download is disabled: " + display)
				continue
			}
			if downloader == "" && !isHTTPReference(ref.fetchValue) {
				message := "BEP artifact for " + ref.label + " is remote-only and no downloader is configured: " + display
				if remoteArtifacts == "required" {
					d.out.fail(message)
				}
				warnOnce(message)
				continue
			}
		}
		tmp, dst := d.prepareDestination(runDir, ref.outputKey)
		var source string
		var err error
		downloaded := false
		switch {
		case ref.remoteOnly && downloader != "":
			source, err = d.runDownloader(ref, runDir, downloader)
			downloaded = source != ""
		case ref.remoteOnly:
			source, err = d.downloadHTTP(ref, runDir)
			downloaded = source != ""
		default:
			resolved, ok := d.resolveLocalArtifact(ref.fetchValue)
			switch {
			case ok:
				source = resolved
			case ref.fetchValue == "":
				err = stageErrorf("BEP artifact reference is empty")
			default:
				err = stageErrorf("BEP artifact is not available locally: ", ref.fetchValue)
			}
		}
		if err != nil {
			_ = os.RemoveAll(tmp)
			reportError(err)
			continue
		}
		if source == "" {
			_ = os.RemoveAll(tmp)
			if remoteArtifacts == "required" {
				d.out.fail("BEP artifact for " + ref.label + " could not be materialized as a local test.outputs carrier: " +
					display)
			}
			if remoteArtifacts == "download" {
				warnOnce("BEP artifact for " + ref.label + " could not be materialized and will be skipped: " + display)
			}
			continue
		}
		err = materialize(ref, source, downloaded, tmp)
		if err == nil {
			err = commitDestination(tmp, dst)
		}
		if err != nil {
			_ = os.RemoveAll(tmp)
			reportError(err)
			continue
		}
		staged = append(staged, stagedArtifact{label: ref.label, outputKey: ref.outputKey, outputDir: dst,
			stagingRoot: runDir, downloaded: downloaded, remoteOnly: ref.remoteOnly, fetchValue: ref.fetchValue})
	}
	if len(staged) == 0 {
		_ = os.RemoveAll(runDir)
	}
	sort.SliceStable(staged, func(a, b int) bool { return staged[a].outputDir < staged[b].outputDir })
	return staged
}

// materialize copies or extracts one carrier into its temporary destination.
func materialize(ref reference, source string, downloaded bool, tmp string) error {
	switch {
	case downloaded:
		if !isFile(source) || pathName(source) != "outputs.zip" {
			return stageErrorf("BEP downloader for ", ref.label, " did not produce an outputs.zip archive: ", source)
		}
		return extractOutputsZip(source, tmp)
	case isDir(source):
		return copyTreeContents(source, tmp)
	case pathName(source) == "outputs.zip":
		return extractOutputsZip(source, tmp)
	}
	return stageErrorf("BEP artifact for ", ref.label, " is not a supported test.outputs carrier: ", source)
}

// dedupeReferences is _dedupe_stageable_artifact_references.
func (d *doctor) dedupeReferences(refs []reference, remoteArtifacts string) ([]reference, []pair) {
	type carrierKey struct {
		remote bool
		value  string
	}
	var selected []reference
	groups := map[pair][]reference{}
	var order []pair
	for _, ref := range refs {
		if ref.cached || !ref.stageableCarrier || ref.outputKey == "" || ref.remoteOnly && remoteArtifacts == "disabled" {
			selected = append(selected, ref)
			continue
		}
		key := pair{ref.label, ref.outputKey}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], ref)
	}
	var ambiguous []pair
	for _, key := range order {
		carriers := map[carrierKey][]reference{}
		var first carrierKey
		for i, ref := range groups[key] {
			var carrier carrierKey
			if ref.remoteOnly {
				carrier = carrierKey{true, ref.fetchValue}
			} else if resolved, ok := d.resolveLocalArtifact(ref.fetchValue); ok {
				carrier = carrierKey{false, resolved}
			} else {
				stripped, err := stripFileURI(ref.fetchValue)
				if err != nil {
					unsupported("artifact reference " + ref.fetchValue)
				}
				carrier = carrierKey{false, strings.ReplaceAll(stripped, `\`, "/")}
			}
			if i == 0 {
				first = carrier
			}
			carriers[carrier] = append(carriers[carrier], ref)
		}
		if len(carriers) > 1 {
			ambiguous = append(ambiguous, key)
			continue
		}
		group := carriers[first]
		best := group[0]
		for _, ref := range group[1:] {
			if ref.fetchValue < best.fetchValue {
				best = ref
			}
		}
		selected = append(selected, best)
	}
	sort.SliceStable(selected, func(a, b int) bool {
		x, y := selected[a], selected[b]
		if x.label != y.label {
			return x.label < y.label
		}
		if x.outputKey != y.outputKey {
			return x.outputKey < y.outputKey
		}
		return x.fetchValue < y.fetchValue
	})
	sort.Slice(ambiguous, func(a, b int) bool { return lessPair(ambiguous[a], ambiguous[b]) })
	return selected, ambiguous
}

func randomHex() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		unsupported("random: " + err.Error())
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return hex.EncodeToString(value[:])
}

// createRunDir is _create_staging_run_dir.
func createRunDir(base string) string {
	if err := os.MkdirAll(base, 0o777); err != nil {
		unsupported("staging directory: " + err.Error())
	}
	run := joinPath(base, "__runs", randomHex())
	if err := os.MkdirAll(pathParent(run), 0o777); err != nil {
		unsupported("staging run directory: " + err.Error())
	}
	if err := os.Mkdir(run, 0o777); err != nil {
		unsupported("staging run directory: " + err.Error())
	}
	return run
}

// prepareDestination is _prepare_staging_destination.
func (d *doctor) prepareDestination(runDir, outputKey string) (string, string) {
	unsafe := outputKey == "" || strings.HasPrefix(outputKey, "/")
	for _, part := range strings.Split(outputKey, "/") {
		unsafe = unsafe || part == ".."
	}
	if unsafe {
		d.out.fail("unsafe BEP output key for staging: " + pyformat.String(outputKey))
	}
	dst := joinPath(runDir, outputKey)
	root, parent := resolvePath(runDir), resolvePath(pathParent(dst))
	if root != parent && !isBelow(parent, root) {
		d.out.fail("unsafe BEP staging destination outside staging dir: " + dst)
	}
	if exists(dst) {
		if !exists(joinPath(dst, stagingMarker)) {
			d.out.fail("refusing to replace non-owned BEP staging directory: " + dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			unsupported("remove " + dst + ": " + err.Error())
		}
	}
	tmp := joinPath(pathParent(dst), "."+pathName(dst)+".tmp-"+randomHex())
	if err := os.MkdirAll(pathParent(tmp), 0o777); err != nil {
		unsupported("staging destination: " + err.Error())
	}
	if err := os.Mkdir(tmp, 0o777); err != nil {
		unsupported("staging destination: " + err.Error())
	}
	return tmp, dst
}

// commitDestination is _commit_staging_destination.
func commitDestination(tmp, dst string) error {
	if err := os.WriteFile(joinPath(tmp, stagingMarker), []byte("owned by dd-test-optimization BEP staging\n"),
		0o666); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// copyTreeContents is _copy_tree_contents: os.walk visits each directory in
// the order the file system lists it, so a refused symlink is the same one.
func copyTreeContents(src, dst string) error {
	files, bytes := 0, int64(0)
	var walk func(directory, relative string) error
	walk = func(directory, relative string) error {
		handle, err := os.Open(directory)
		if err != nil {
			// os.walk skips a directory it cannot list.
			return nil
		}
		entries, err := handle.ReadDir(-1)
		_ = handle.Close()
		if err != nil {
			return nil
		}
		var dirs, names []string
		for _, entry := range entries {
			if isDirEntry(joinPath(directory, entry.Name()), entry) {
				dirs = append(dirs, entry.Name())
			} else {
				names = append(names, entry.Name())
			}
		}
		for _, name := range dirs {
			source := joinPath(directory, name)
			if isSymlink(source) {
				return stageErrorf("refusing to stage symlink directory from BEP artifact source: ", source)
			}
			if err := os.MkdirAll(joinPath(dst, relative, name), 0o777); err != nil {
				return err
			}
		}
		for _, name := range names {
			files++
			if files > maxTreeFiles {
				return stageErrorf("BEP test.outputs tree has too many files: ", src)
			}
			source := joinPath(directory, name)
			if isSymlink(source) {
				return stageErrorf("refusing to stage symlink from BEP artifact source: ", source)
			}
			info, err := os.Stat(source)
			if err != nil {
				unsupported("stat " + source + ": " + err.Error())
			}
			if !info.Mode().IsRegular() {
				unsupported("special file " + source)
			}
			bytes += info.Size()
			if bytes > maxTreeBytes {
				return stageErrorf("BEP test.outputs tree is too large: ", src)
			}
			target := joinPath(dst, relative, name)
			if err := os.MkdirAll(pathParent(target), 0o777); err != nil {
				return err
			}
			if err := copyFile(source, target, info); err != nil {
				return err
			}
		}
		for _, name := range dirs {
			if err := walk(joinPath(directory, name), joinPath(relative, name)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(src, ".")
}

// isDirEntry is DirEntry.is_dir(), which follows symlinks and treats an
// error as false.
func isDirEntry(path string, entry fs.DirEntry) bool {
	if entry.Type()&fs.ModeSymlink == 0 {
		return entry.IsDir()
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// copyFile is shutil.copy2: the content, permission bits and modification
// time. The access time is set to the modification time; nothing reads it.
func copyFile(source, target string, info fs.FileInfo) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(target, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}

var windowsDriveName = regexp.MustCompile(`^[A-Za-z]:/`)

// extractOutputsZip is _extract_outputs_zip.
func extractOutputsZip(zipPath, dst string) error {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		unsupported("outputs.zip " + zipPath + ": " + err.Error())
	}
	defer func() { _ = archive.Close() }()
	type planned struct {
		member *zip.File
		target string
		dir    bool
	}
	var plan []planned
	bytes := uint64(0)
	root := resolvePath(dst)
	for _, member := range archive.File {
		name := member.Name
		if strings.ContainsRune(name, 0) || !plainASCII(name) && (member.Flags&0x800 == 0 || !utf8.ValidString(name)) {
			// zipfile truncates at NUL and decodes legacy names as cp437.
			unsupported("outputs.zip member name")
		}
		unsafeName := stageErrorf("unsafe path in BEP outputs.zip ", zipPath, ": ", pyformat.String(name))
		normalized := strings.TrimRight(strings.ReplaceAll(name, `\`, "/"), "/")
		if normalized == "" || strings.HasPrefix(normalized, "/") || windowsDriveName.MatchString(normalized) {
			return unsafeName
		}
		for _, part := range strings.Split(normalized, "/") {
			if part == "" || part == "." || part == ".." {
				return unsafeName
			}
		}
		target := joinPath(dst, normalized)
		if resolved := resolvePath(target); resolved != root && !isBelow(resolved, root) {
			return unsafeName
		}
		if len(plan)+1 > maxTreeFiles {
			return stageErrorf("BEP outputs.zip has too many entries: ", zipPath)
		}
		dir := strings.HasSuffix(name, "/")
		if !dir {
			bytes += member.UncompressedSize64
			if bytes > maxTreeBytes {
				return stageErrorf("BEP outputs.zip is too large after decompression: ", zipPath)
			}
		}
		plan = append(plan, planned{member, target, dir})
	}
	for _, item := range plan {
		if item.dir {
			if err := os.MkdirAll(item.target, 0o777); err != nil {
				unsupported("outputs.zip directory: " + err.Error())
			}
			continue
		}
		if item.member.Flags&0x1 != 0 || item.member.Method != zip.Store && item.member.Method != zip.Deflate {
			// zipfile reports encryption and other methods in its own words.
			unsupported("outputs.zip member method")
		}
		if err := os.MkdirAll(pathParent(item.target), 0o777); err != nil {
			unsupported("outputs.zip directory: " + err.Error())
		}
		if err := extractMember(item.member, item.target); err != nil {
			unsupported("outputs.zip member: " + err.Error())
		}
	}
	return nil
}

func extractMember(member *zip.File, target string) error {
	in, err := member.Open()
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

var unsafeFragment = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeDownloadFragment is _safe_download_fragment.
func safeDownloadFragment(value string) string {
	fragment := strings.Trim(unsafeFragment.ReplaceAllString(value, "_"), "._-")
	if fragment == "" {
		return "artifact"
	}
	return fragment
}

func downloadDestination(ref reference, runDir string) string {
	return joinPath(runDir, "__downloads", safeDownloadFragment(ref.label), safeDownloadFragment(ref.outputKey),
		"outputs.zip")
}

// replaceableFile removes a previous download; a directory there is an
// error with Python's text.
func replaceableFile(path, kind string) error {
	if !exists(path) {
		return nil
	}
	if !isFile(path) {
		return stageErrorf("refusing to replace non-file BEP ", kind, " destination: ", path)
	}
	if err := os.Remove(path); err != nil {
		unsupported("remove " + path + ": " + err.Error())
	}
	return nil
}

// runDownloader is _download_remote_artifact; "" stands for None.
func (d *doctor) runDownloader(ref reference, runDir, downloader string) (string, error) {
	dst := downloadDestination(ref, runDir)
	if err := os.MkdirAll(pathParent(dst), 0o777); err != nil {
		unsupported("download directory: " + err.Error())
	}
	if err := replaceableFile(dst, "downloader"); err != nil {
		return "", err
	}
	timeout := d.request.Args.BepArtifactDownloaderTimeout
	command := exec.Command(downloader, "--uri", ref.fetchValue, "--name", ref.name, "--output", dst)
	command.Stdin = os.Stdin
	command.WaitDelay = time.Second
	if err := command.Start(); err != nil {
		unsupported("downloader could not start: " + err.Error())
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	timer := time.NewTimer(time.Duration(timeout * float64(time.Second)))
	defer timer.Stop()
	var err error
	select {
	case err = <-done:
	case <-d.ctx.Done():
		_ = command.Process.Kill()
		<-done //nolint:errcheck // The killed downloader exits with an error.
		panic(interrupted{})
	case <-timer.C:
		_ = command.Process.Kill()
		<-done //nolint:errcheck // The killed downloader exits with an error.
		d.out.warn("BEP artifact downloader timed out for " + ref.label + " after " + formatG(timeout) + "s")
		return "", nil
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			unsupported("downloader: " + err.Error())
		}
		code := exit.ExitCode()
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			code = -int(status.Signal())
		}
		d.out.warn("BEP artifact downloader failed for " + ref.label + " with exit code " + strconv.Itoa(code))
		return "", nil
	}
	if !exists(dst) {
		return "", nil
	}
	if !isFile(dst) {
		return "", stageErrorf("BEP artifact downloader did not produce a file at ", dst)
	}
	return dst, nil
}

// formatG is Python's format(value, "g").
func formatG(value float64) string {
	return strconv.FormatFloat(value, 'g', 6, 64)
}

// downloadHTTP is _download_http_artifact. A failed download warns with
// urllib's error text, so it is left to the Python doctor.
func (d *doctor) downloadHTTP(ref reference, runDir string) (string, error) {
	dst := downloadDestination(ref, runDir)
	tmp := joinPath(pathParent(dst), "outputs.zip.tmp")
	if err := os.MkdirAll(pathParent(dst), 0o777); err != nil {
		unsupported("download directory: " + err.Error())
	}
	for _, path := range []string{dst, tmp} {
		if err := replaceableFile(path, "HTTP download"); err != nil {
			return "", err
		}
	}
	display := displayArtifactReference(ref.fetchValue)
	timeout := time.Duration(d.request.Args.BepArtifactDownloaderTimeout * float64(time.Second))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Each download has its own transport, so nothing reuses its idle
	// connections.
	defer transport.CloseIdleConnections()
	// urllib neither asks for compressed bodies nor decodes them.
	transport.DisableCompression = true
	// urllib's timeout bounds each socket operation.
	transport.DialContext = (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = timeout
	transport.ResponseHeaderTimeout = timeout
	client := &http.Client{Transport: transport}
	for attempt := 0; attempt < httpAttempts; attempt++ {
		_ = os.Remove(tmp)
		d.checkInterrupted()
		total, retry, err := fetchHTTP(d.ctx, client, ref.fetchValue, tmp, timeout, display)
		if err == nil {
			if err := os.Rename(tmp, dst); err != nil {
				unsupported("rename " + tmp + ": " + err.Error())
			}
			d.out.info("BEP HTTP artifact downloaded for " + ref.label + " output " + ref.outputKey + ": " + display +
				" (" + strconv.FormatInt(total, 10) + " bytes)")
			return dst, nil
		}
		_ = os.Remove(tmp)
		var staging stageError
		if errors.As(err, &staging) {
			return "", err
		}
		if !retry || attempt+1 >= httpAttempts {
			break
		}
		select {
		case <-d.ctx.Done():
		case <-time.After(httpBackoffFirst << attempt):
		}
	}
	d.checkInterrupted()
	unsupported("HTTP artifact download failed")
	return "", nil
}

// fetchHTTP makes one download attempt. It reports whether urllib would
// retry the failure.
func fetchHTTP(parent context.Context, client *http.Client, url, tmp string, timeout time.Duration,
	display string) (int64, bool, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	request.Header.Set("User-Agent", "datadog-rules-test-optimization")
	response, err := client.Do(request)
	if err != nil {
		return 0, true, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		code := response.StatusCode
		return 0, code == 408 || code == 429 || code >= 500 && code <= 599, errors.New(response.Status)
	}
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		unsupported("HTTP download file: " + err.Error())
	}
	defer func() { _ = out.Close() }()
	expected := int64(-1)
	if length := response.Header.Get("Content-Length"); length != "" {
		value, err := strconv.ParseInt(length, 10, 64)
		if err != nil || value < 0 {
			return 0, false, errors.New("invalid Content-Length")
		}
		if value > maxTreeBytes {
			return 0, false, stageErrorf("BEP HTTP artifact is too large: ", display)
		}
		expected = value
	}
	total := int64(0)
	buffer := make([]byte, 1024*1024)
	idle := time.AfterFunc(timeout, cancel)
	defer idle.Stop()
	for {
		idle.Reset(timeout)
		count, err := response.Body.Read(buffer)
		if count > 0 {
			total += int64(count)
			if total > maxTreeBytes {
				return 0, false, stageErrorf("BEP HTTP artifact is too large: ", display)
			}
			if _, err := out.Write(buffer[:count]); err != nil {
				unsupported("HTTP download write: " + err.Error())
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, true, err
		}
	}
	if expected >= 0 && total < expected {
		return 0, true, errors.New("incomplete read")
	}
	if err := out.Close(); err != nil {
		unsupported("HTTP download write: " + err.Error())
	}
	return total, false, nil
}

// cleanupStagedRuns is _cleanup_staged_bep_run_roots.
func (d *doctor) cleanupStagedRuns(staged []stagedArtifact, base string) {
	runsRoot := resolvePath(joinPath(base, "__runs"))
	unique := map[string]bool{}
	for _, item := range staged {
		unique[resolvePath(item.stagingRoot)] = true
	}
	roots := make([]string, 0, len(unique))
	for root := range unique {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(a, b int) bool { return lessPath(roots[a], roots[b]) })
	for _, root := range roots {
		if resolvePath(pathParent(root)) != runsRoot {
			d.out.fail("refusing to clean BEP staging root outside run directory: " + root)
		}
		owned := false
		for _, item := range staged {
			if resolvePath(item.stagingRoot) != root {
				continue
			}
			outputDir := resolvePath(item.outputDir)
			if outputDir != root && !isBelow(outputDir, root) {
				d.out.fail("refusing to clean BEP output outside staging root: " + outputDir)
			}
			if exists(joinPath(outputDir, stagingMarker)) {
				owned = true
			}
		}
		if owned {
			_ = os.RemoveAll(root)
		}
	}
}
