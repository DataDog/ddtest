package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// commandSet is the --commands input: `ddtest describe` outputs grouped by
// job. A caller that owns its pipeline format describes each test command
// itself; planning then reuses the CI catalog selection without a CI-provider
// parser.
type commandSet struct {
	SchemaVersion int `json:"schema_version"`
	Jobs          []struct {
		Name     string               `json:"name"`
		Commands []commandDescription `json:"commands"`
	} `json:"jobs"`
}

const commandSetSchemaVersion = 1

// loadCommandSet converts a command set into a catalog for the current
// checkout. Each job carries its commands' service and environment as job
// variables, so commands of another worker identity stay runnable unless the
// process environment matches, as for CI catalogs.
func loadCommandSet(path string) (onboard.PythonCICommandCatalog, error) {
	var set commandSet
	f, err := os.Open(path)
	if err != nil {
		return onboard.PythonCICommandCatalog{}, err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return onboard.PythonCICommandCatalog{}, fmt.Errorf("decode command set: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return onboard.PythonCICommandCatalog{}, errors.New("command set must contain one JSON object")
	}
	if set.SchemaVersion != commandSetSchemaVersion {
		return onboard.PythonCICommandCatalog{}, fmt.Errorf("command set schema_version must be %d", commandSetSchemaVersion)
	}
	if len(set.Jobs) == 0 {
		return onboard.PythonCICommandCatalog{}, errors.New("command set has no jobs")
	}
	checkout, err := describeCheckout()
	if err != nil {
		return onboard.PythonCICommandCatalog{}, err
	}
	catalog := onboard.PythonCICommandCatalog{SchemaVersion: 1, CIProvider: "commands", Source: path,
		TestLevel: "suite", RepositoryURL: checkout.RepositoryURL, SHA: checkout.CommitSha}
	jobs := map[string]bool{}
	for _, job := range set.Jobs {
		if strings.TrimSpace(job.Name) == "" || jobs[job.Name] {
			return onboard.PythonCICommandCatalog{}, fmt.Errorf("job names must be unique and nonempty: %q", job.Name)
		}
		jobs[job.Name] = true
		planned := onboard.PythonCIJob{CICommandJob: onboard.CICommandJob{Name: job.Name, Variables: map[string]string{}},
			Commands: []onboard.PythonCICommand{}}
		for _, d := range job.Commands {
			if d.SchemaVersion != 2 {
				return onboard.PythonCICommandCatalog{}, fmt.Errorf("command %q: describe schema_version must be 2", d.Command.ID)
			}
			if d.TIARequest.SHA != checkout.CommitSha {
				return onboard.PythonCICommandCatalog{}, fmt.Errorf("command %q was described at %s, not the checkout %s", d.Command.ID, d.TIARequest.SHA, checkout.CommitSha)
			}
			if !sameCommandRepository(d.TIARequest.RepositoryURL, checkout.RepositoryURL) {
				return onboard.PythonCICommandCatalog{}, fmt.Errorf("command %q describes another repository", d.Command.ID)
			}
			command := onboard.PythonCICommand{
				PythonCatalogCommand: onboard.PythonCatalogCommand{ID: d.Command.ID, Argv: d.Command.Argv, Cwd: d.Command.Cwd, TestRoots: d.Command.TestRoots},
				Configurations:       d.TIARequest.Configurations,
				// describe probes the worker interpreter, so its version is the environment's.
				PythonVersion:      majorMinor(d.TIARequest.Configurations["runtime.version"]),
				CandidateTestFiles: d.CandidateTestFiles,
			}
			if d.TIARequest.TestLevel != "suite" {
				command.Unresolved = "command planning supports suite level only"
			}
			for key, value := range map[string]string{"DD_SERVICE": d.TIARequest.Service, "DD_ENV": d.TIARequest.Env} {
				if previous, set := planned.Variables[key]; set && previous != value {
					planned.Unresolved = append(planned.Unresolved, "commands in one job use different "+key)
				}
				planned.Variables[key] = value
			}
			planned.Commands = append(planned.Commands, command)
		}
		catalog.Jobs = append(catalog.Jobs, planned)
	}
	return catalog, nil
}

func majorMinor(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

func sameCommandRepository(described, checkout string) bool {
	if described == "" || checkout == "" {
		return false
	}
	if described == checkout {
		return true
	}
	normalize := func(value string) string {
		if !strings.Contains(value, "://") {
			if host, path, ok := strings.Cut(value, ":"); ok {
				value = "ssh://" + host + "/" + path
			}
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return ""
		}
		host := strings.ToLower(parsed.Host)
		path := strings.TrimSuffix(strings.TrimRight(parsed.EscapedPath(), "/"), ".git")
		if strings.EqualFold(parsed.Hostname(), "github.com") {
			path = strings.ToLower(path)
		}
		return host + path
	}
	identity := normalize(described)
	return identity != "" && identity == normalize(checkout)
}
