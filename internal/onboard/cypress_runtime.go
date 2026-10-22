package onboard

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
)

// Cypress.run executes in the JavaScript action's runtime, which can differ
// from setup-node used by its build/start commands. Never infer it from a major
// action tag: inspect the exact referenced action metadata.
func CypressActionNode(ctx context.Context, action string) (string, error) {
	repository, ref, found := strings.Cut(action, "@")
	if !found || !strings.EqualFold(repository, "cypress-io/github-action") || ref == "" || len(ref) > 256 {
		return "", fmt.Errorf("cypress action requires an explicit public reference")
	}
	address := "https://raw.githubusercontent.com/cypress-io/github-action/" + url.PathEscape(ref) + "/action.yml"
	data, err := cachedCypressMetadata(ctx, address, func() ([]byte, error) {
		return fetchRuntimeMetadata(ctx, &http.Client{Timeout: 10 * time.Second}, address)
	})
	if err != nil {
		return "", err
	}
	var definition struct {
		Runs struct {
			Using string `yaml:"using"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return "", err
	}
	version := strings.TrimPrefix(definition.Runs.Using, "node")
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(version) {
		return "", fmt.Errorf("cypress action runtime %q is not a Node runtime", definition.Runs.Using)
	}
	return version, nil
}

// These are explicit hypothetical CI contexts, never credentials or values
// inherited from the evaluator. Check trusted pushes/PRs both with and without
// the key, and fork PRs without it. Unknown context fields still fail parsing.
func cypressCIContexts(root string) []map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "git", "-C", root, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return nil
	}
	remote := strings.TrimSpace(string(data))
	remote = strings.TrimPrefix(remote, "https://github.com/")
	remote = strings.TrimPrefix(remote, "git@github.com:")
	owner, repository, found := strings.Cut(strings.TrimSuffix(remote, ".git"), "/")
	if !found || !forkJobOwner.MatchString(owner) || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(repository) {
		return nil
	}
	var contexts []map[string]string
	for _, event := range []string{"push", "pull_request", "fork_pull_request", "workflow_dispatch"} {
		for _, key := range []string{"present", ""} {
			if event == "fork_pull_request" && key != "" {
				continue
			}
			values := map[string]string{
				"github.repository_owner": owner, "github.repository": owner + "/" + repository,
				"github.event_name": event, "github.event.pull_request.head.repo.full_name": "",
				"secrets.DD_API_KEY": key,
			}
			if event == "pull_request" {
				values["github.event.pull_request.head.repo.full_name"] = owner + "/" + repository
			}
			if event == "fork_pull_request" {
				values["github.event_name"] = "pull_request"
				values["github.event.pull_request.head.repo.full_name"] = "ddtest-fork/" + repository
			}
			values["scenario"] = event + " / key absent"
			if key != "" {
				values["scenario"] = event + " / key present"
			}
			contexts = append(contexts, values)
		}
	}
	return contexts
}

func cypressContextJobs(root string, workflow ciWorkflow, job runtimeJob) ([]runtimeJob, error) {
	events := map[string]bool{}
	switch on := workflow.On.(type) {
	case nil:
	case string:
		events[on] = true
	case []any:
		for _, event := range on {
			if value, ok := event.(string); ok {
				events[value] = true
			} else {
				return nil, fmt.Errorf("dynamic Cypress workflow events require review")
			}
		}
	case map[string]any:
		for event := range on {
			events[event] = true
		}
	default:
		return nil, fmt.Errorf("cypress workflow events require review")
	}
	for event := range events {
		if !slices.Contains([]string{"push", "pull_request", "workflow_dispatch"}, event) {
			return nil, fmt.Errorf("cypress CI context requires review for workflow event %s", event)
		}
	}
	contexts := cypressCIContexts(root)
	if len(contexts) == 0 {
		return []runtimeJob{job}, nil
	}
	var jobs []runtimeJob
	for _, values := range contexts {
		if len(events) > 0 && !events[values["github.event_name"]] {
			continue
		}
		child := job
		child.Context = maps.Clone(values)
		child.Steps = slices.Clone(job.Steps)
		jobs = append(jobs, child)
	}
	return jobs, nil
}

func cypressExcludedBootstrap(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) string {
	known := conditionEnvironment(workflow, job, step, row)
	known["DD_TRACE_PACKAGE"], known["DD_TRACE_ESM_IMPORT"] = "", ""
	var options string
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := env["NODE_OPTIONS"]; ok {
			options = value
		}
	}
	known["NODE_OPTIONS"] = ""
	resolved, err := bootstrapOptions(options, row, known)
	if err != nil || strings.Contains(resolved, "dd-trace") || strings.ContainsRune(resolved, 0) || strings.ContainsAny(resolved, "$`") {
		return "Cannot verify that the conditional Datadog preload is absent when installation is skipped."
	}
	words, err := shellquote.Split(resolved)
	if err != nil {
		return "Cannot parse NODE_OPTIONS with installation skipped."
	}
	for i, word := range words {
		if word == "-r" || word == "--require" || word == "--import" {
			if i+1 == len(words) || words[i+1] == "" || strings.HasPrefix(words[i+1], "-") {
				return "Skipped tracer installation leaves a preload flag without its module."
			}
		}
		if word == "--require=" || word == "--import=" {
			return "Skipped tracer installation leaves an empty preload module."
		}
	}
	return ""
}

// Cypress browser image short tags name their Node release. Unknown images
// remain unresolved rather than inheriting the evaluator's runtime.
func cypressContainerNode(container any) string {
	image, _ := container.(string)
	if value, ok := container.(map[string]any); ok {
		image, _ = value["image"].(string)
	}
	if matched := regexp.MustCompile(`^cypress/browsers:([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(image); len(matched) > 0 {
		return matched[1]
	}
	return ""
}

// Local runs use their own tracer. Resolve a guarded CI preload only when both
// installation outcomes produce the same ordinary Node options. Actual CI
// installation and preload are still checked within each synthetic context.
func cypressLocalNodeOptions(value string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) (string, error) {
	var ordinary string
	for index, outcome := range []string{"success", "skipped"} {
		known := conditionEnvironment(workflow, job, step, row)
		known["NODE_OPTIONS"] = ""
		for _, candidate := range job.Steps {
			if strings.HasPrefix(strings.ToLower(candidate.Uses), githubAction+"@") && candidate.ID != "" {
				known["\x00steps."+candidate.ID+".outcome"] = outcome
			}
		}
		resolved, err := resolveNodeOptions(value, row, known)
		if err != nil {
			return "", err
		}
		if index > 0 && resolved != ordinary {
			return "", fmt.Errorf("cypress guarded NODE_OPTIONS changes ordinary runtime options; validate its contexts separately")
		}
		ordinary = resolved
	}
	return ordinary, nil
}
