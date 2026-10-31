// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
)

// PackageLocation places a JavaScript package that lives below the
// repository root holding its CI configuration. CI checks out the whole
// repository and runs the package's steps in its directory, so onboarding
// reads CI, validates and keeps its reports at Root.
type PackageLocation struct {
	// Root is the repository root that holds the CI configuration.
	Root string
	// Package is the package directory relative to Root, slash-separated.
	// Empty when no package could be selected; Hint then says why.
	Package string
	// Hint names the candidate package directories and the flags that
	// select one when Root has no manifest and no single CI package.
	Hint string
}

// maxPackageCandidates bounds the package manifests read below a root
// without a manifest of its own.
const maxPackageCandidates = 64

// anyCIConfigurationFiles lists, relative to root, the configuration files
// of every supported CI provider. A provider whose files cannot be listed
// contributes none.
func anyCIConfigurationFiles(root string) []string {
	var files []string
	for _, provider := range ciProviders {
		if found, err := provider.configurationFiles(root); err == nil {
			files = append(files, found...)
		}
	}
	return files
}

// LocatePackage reports where onboarding of the current directory belongs
// when it is not simply the current directory:
//
//   - In a JavaScript package below the repository root, when the package
//     has no CI configuration and the root has, Root is the repository root
//     and Package the current directory relative to it.
//   - At a root without any platform manifest, Package is the one package
//     below it that the CI configuration references; otherwise Hint lists
//     the candidates.
//
// directory is the working directory, which platform detection reads. ok is
// false when the current directory is the project, as before.
func LocatePackage(directory string) (location PackageLocation, ok bool) {
	if hint := settings.GetPlatform(); hint != "" && hint != "javascript" {
		return PackageLocation{}, false
	}
	directory = CanonicalDirectory(directory)
	top := gitTopLevel(directory)
	if top != "" && top != directory {
		relative, err := filepath.Rel(top, directory)
		if err != nil || !filepath.IsLocal(relative) {
			return PackageLocation{}, false
		}
		if info, err := os.Stat(filepath.Join(directory, "package.json")); err != nil || info.IsDir() {
			return PackageLocation{}, false
		}
		// Jenkins and Buildkite can read a pipeline from any directory: a
		// package with its own CI configuration is onboarded where it is.
		if len(anyCIConfigurationFiles(directory)) > 0 || len(anyCIConfigurationFiles(top)) == 0 {
			return PackageLocation{}, false
		}
		if detected, err := platform.DetectPlatform(); err != nil || detected.Name() != "javascript" {
			return PackageLocation{}, false
		}
		return PackageLocation{Root: top, Package: filepath.ToSlash(relative)}, true
	}
	if found, err := platform.HasProjectManifest(directory); err != nil || found {
		return PackageLocation{}, false
	}
	candidates := packageCandidates(directory)
	if len(candidates) == 0 {
		return PackageLocation{}, false
	}
	references := ciPackageReferences(directory, candidates)
	var referenced []string
	for _, candidate := range candidates {
		if len(references[candidate]) > 0 {
			referenced = append(referenced, candidate)
		}
	}
	if len(referenced) == 1 {
		return PackageLocation{Root: directory, Package: referenced[0]}, true
	}
	return PackageLocation{Root: directory, Hint: packageCandidatesHint(candidates, references)}, true
}

// packageCandidatesHint names the packages below a root without a manifest,
// those CI references first, and how to onboard one of them.
func packageCandidatesHint(candidates []string, references map[string][]string) string {
	ordered := slices.Clone(candidates)
	slices.SortStableFunc(ordered, func(a, b string) int {
		return boolOrder(len(references[b]) > 0) - boolOrder(len(references[a]) > 0)
	})
	const listed = 8
	var names []string
	for i, candidate := range ordered {
		if i == listed {
			names = append(names, fmt.Sprintf("and %d more", len(ordered)-listed))
			break
		}
		if files := references[candidate]; len(files) > 0 {
			candidate += " (referenced by " + strings.Join(files, ", ") + ")"
		}
		names = append(names, candidate)
	}
	return fmt.Sprintf("the repository root has no project manifest; JavaScript packages below it: %s. Run ddtest onboard in the package directory CI tests (for example cd %s && ddtest onboard), or pass --platform javascript --framework <framework> here", strings.Join(names, ", "), ordered[0])
}

func boolOrder(value bool) int {
	if value {
		return 1
	}
	return 0
}

// packageCandidates lists, slash-separated and sorted, the directories below
// root with a package.json, skipping installed dependencies and files Git
// ignores.
func packageCandidates(root string) []string {
	var manifests []string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "package.json", "*/package.json").Output()
	if err == nil {
		manifests = strings.Split(string(data), "\x00")
	} else {
		// Without Git, read a few levels below the root.
		_ = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			relative, relErr := filepath.Rel(root, file)
			if relErr != nil {
				return nil
			}
			if entry.IsDir() && relative != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || strings.Count(filepath.ToSlash(relative), "/") >= 3) {
				return filepath.SkipDir
			}
			if !entry.IsDir() && entry.Name() == "package.json" {
				manifests = append(manifests, filepath.ToSlash(relative))
			}
			return nil
		})
	}
	var candidates []string
	for _, manifest := range manifests {
		directory := path.Dir(manifest)
		if path.Base(manifest) != "package.json" || directory == "." || slices.Contains(strings.Split(directory, "/"), "node_modules") || slices.Contains(candidates, directory) {
			continue
		}
		candidates = append(candidates, directory)
		if len(candidates) == maxPackageCandidates {
			break
		}
	}
	slices.Sort(candidates)
	return candidates
}

// ciPackageReferences maps each candidate directory to the CI configuration
// files that name it as a path (test/docker-compose.yml) or a working
// directory (cd test, npm --prefix test, working-directory: test, dir('test')).
func ciPackageReferences(root string, candidates []string) map[string][]string {
	references := map[string][]string{}
	for _, file := range anyCIConfigurationFiles(root) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		for _, candidate := range candidates {
			if packageReference(candidate).Match(data) {
				references[candidate] = append(references[candidate], file)
			}
		}
	}
	return references
}

func packageReference(directory string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(directory)
	asPath := `(?:^|[\s"'=:(,\[{])(?:\./)?` + quoted + `/`
	asDirectory := `(?:^|[\s"'(;&|{])(?:cd|pushd|--prefix|-C|--cwd|--dir|--workspace|-w|working[-_]directory\s*:|workdir\s*:|dir\s*\()\s*=?\s*["']?(?:\./)?` + quoted + `/?["']?(?:$|[\s;&|)"',}])`
	// An absolute working directory ends with the package directory:
	// working_directory: ~/project/test, working_dir: /app/test.
	asWorkingDirectory := `(?:working[-_]directory|working_dir|workdir)\s*:\s*["']?[^\s"']*/` + quoted + `/?["']?\s*$`
	return regexp.MustCompile(`(?mi)` + asPath + `|` + asDirectory + `|` + asWorkingDirectory)
}

func gitTopLevel(directory string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return CanonicalDirectory(strings.TrimRight(string(data), "\r\n"))
}

// CanonicalDirectory resolves symlinks in directory, so aliases of one
// checkout compare equal.
func CanonicalDirectory(directory string) string {
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		return resolved
	}
	return filepath.Clean(directory)
}
