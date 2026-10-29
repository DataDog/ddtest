// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// buildkiteDockerfile is what ddtest reads from the Dockerfile a Docker
// Compose service is built from: the base image of the stage the build
// produces, which the image rules and the static runtime rules then read,
// and what that stage adds to it.
type buildkiteDockerfile struct {
	// image is the stage's base image, every variable resolved; problem
	// explains why ddtest cannot know it.
	image, problem string
	// workdir is the stage's last WORKDIR, absolute; empty for none.
	// workdirProblem explains one ddtest cannot resolve.
	workdir, workdirProblem string
	// contextCopy is where the stage copies the whole build context
	// (COPY . <destination>), absolute; empty for none.
	contextCopy string
	// bash and downloader report a RUN instruction of the stage installing
	// Bash, and curl or wget.
	bash, downloader bool
}

// buildkiteDockerfileStage is one FROM of a Dockerfile, with what it
// inherits from the stage it is built from.
type buildkiteDockerfileStage struct {
	name string
	buildkiteDockerfile
	// vars are the ARG and ENV values the stage's instructions read; a
	// variable ddtest cannot know is absent.
	vars map[string]string
}

// buildkiteDockerfileVariable matches $NAME, ${NAME}, ${NAME:-x} and
// ${NAME:+x} in Dockerfile instructions.
var buildkiteDockerfileVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?([-+])([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// buildkiteDockerfileExpand resolves the variables of text with vars,
// returning the first one ddtest cannot know.
func buildkiteDockerfileExpand(text string, vars map[string]string) (string, string) {
	unknown := ""
	result := buildkiteDockerfileVariable.ReplaceAllStringFunc(text, func(match string) string {
		parts := buildkiteDockerfileVariable.FindStringSubmatch(match)
		name := parts[1] + parts[4]
		value, known := vars[name]
		switch {
		case !known:
			if unknown == "" {
				unknown = name
			}
			return match
		case parts[2] == "-" && value == "":
			return parts[3]
		case parts[2] == "+" && value != "":
			return parts[3]
		case parts[2] == "+":
			return ""
		}
		return value
	})
	return result, unknown
}

// buildkiteDockerfileHeredoc matches the heredocs an instruction opens
// (<<EOF, <<-EOF, <<"EOF"), whose bodies follow it.
var buildkiteDockerfileHeredoc = regexp.MustCompile(`<<(-?)["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)

// buildkiteDockerfileInstructions splits a Dockerfile into its
// instructions: continuation lines joined, comments dropped, and heredoc
// bodies kept with the instruction that opens them.
func buildkiteDockerfileInstructions(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var instructions []string
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		var instruction strings.Builder
		for {
			line := strings.TrimRight(lines[i], " \t")
			continued := strings.HasSuffix(line, "\\")
			instruction.WriteString(strings.TrimSuffix(line, "\\"))
			if !continued || i+1 >= len(lines) {
				break
			}
			instruction.WriteString(" ")
			i++
			// Comment lines inside a continued instruction are removed.
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
				i++
			}
		}
		text := instruction.String()
		for _, heredoc := range buildkiteDockerfileHeredoc.FindAllStringSubmatch(text, -1) {
			var body []string
			for i+1 < len(lines) {
				i++
				line := lines[i]
				if heredoc[1] == "-" {
					line = strings.TrimLeft(line, "\t")
				}
				if line == heredoc[2] {
					break
				}
				body = append(body, line)
			}
			text += "\n" + strings.Join(body, "\n")
		}
		instructions = append(instructions, strings.TrimSpace(text))
	}
	return instructions
}

// buildkiteDockerfileFlags splits an instruction's arguments into its
// leading --flag=value options and the rest.
func buildkiteDockerfileFlags(arguments string) (map[string]string, []string) {
	flags := map[string]string{}
	words := strings.Fields(arguments)
	for len(words) > 0 && strings.HasPrefix(words[0], "--") {
		name, value, _ := strings.Cut(strings.TrimPrefix(words[0], "--"), "=")
		flags[strings.ToLower(name)] = value
		words = words[1:]
	}
	return flags, words
}

// readBuildkiteDockerfile reads the stage a build produces: target, or the
// last one. overridden lists the build arguments the build passes, whose
// values ddtest does not read: a FROM or WORKDIR reading one is unknown.
func readBuildkiteDockerfile(text, target string, overridden map[string]bool) buildkiteDockerfile {
	global := map[string]string{}
	var stages []buildkiteDockerfileStage
	var current *buildkiteDockerfileStage
	for _, instruction := range buildkiteDockerfileInstructions(text) {
		keyword, arguments, _ := strings.Cut(instruction, " ")
		keyword = strings.ToUpper(keyword)
		arguments = strings.TrimSpace(arguments)
		if current == nil && keyword != "FROM" {
			if keyword == "ARG" {
				for _, declaration := range strings.Fields(arguments) {
					name, value, assigned := strings.Cut(declaration, "=")
					value = strings.Trim(value, `"'`)
					expanded, unknown := buildkiteDockerfileExpand(value, global)
					if !assigned || overridden[name] || unknown != "" {
						delete(global, name)
						continue
					}
					global[name] = expanded
				}
			}
			continue
		}
		switch keyword {
		case "FROM":
			stages = append(stages, buildkiteDockerfileFromStage(arguments, stages, global, overridden))
			current = &stages[len(stages)-1]
		case "ARG":
			for _, declaration := range strings.Fields(arguments) {
				name, value, assigned := strings.Cut(declaration, "=")
				value = strings.Trim(value, `"'`)
				switch {
				case overridden[name]:
					delete(current.vars, name)
				case !assigned:
					// A bare ARG reads the global declaration's value.
					if known, ok := global[name]; ok {
						current.vars[name] = known
					} else {
						delete(current.vars, name)
					}
				default:
					if expanded, unknown := buildkiteDockerfileExpand(value, current.vars); unknown == "" {
						current.vars[name] = expanded
					} else {
						delete(current.vars, name)
					}
				}
			}
		case "ENV":
			buildkiteDockerfileEnv(arguments, current.vars)
		case "WORKDIR":
			directory, unknown := buildkiteDockerfileExpand(strings.Trim(arguments, `"'`), current.vars)
			if unknown != "" {
				current.workdir, current.workdirProblem = "", "its WORKDIR "+arguments+" reads $"+unknown+", which ddtest cannot resolve"
				continue
			}
			if current.workdirProblem != "" && !path.IsAbs(directory) {
				continue
			}
			base := current.workdir
			if base == "" {
				base = "/"
			}
			if !path.IsAbs(directory) {
				directory = path.Join(base, directory)
			}
			current.workdir, current.workdirProblem = path.Clean(directory), ""
		case "RUN":
			bash, downloader := buildkiteDockerfileInstalls(arguments)
			current.bash = current.bash || bash
			current.downloader = current.downloader || downloader
		case "COPY", "ADD":
			flags, words := buildkiteDockerfileFlags(arguments)
			if _, other := flags["from"]; other || len(words) != 2 || path.Clean(words[0]) != "." {
				continue
			}
			destination, unknown := buildkiteDockerfileExpand(words[1], current.vars)
			if unknown != "" || current.workdirProblem != "" && !path.IsAbs(destination) {
				current.contextCopy = ""
				continue
			}
			if !path.IsAbs(destination) {
				base := current.workdir
				if base == "" {
					base = "/"
				}
				destination = path.Join(base, destination)
			}
			current.contextCopy = path.Clean(destination)
		}
	}
	if len(stages) == 0 {
		return buildkiteDockerfile{problem: "which has no FROM instruction ddtest can read"}
	}
	stage := stages[len(stages)-1]
	if target != "" {
		i := slices.IndexFunc(stages, func(stage buildkiteDockerfileStage) bool { return stage.name == strings.ToLower(target) })
		if i < 0 {
			return buildkiteDockerfile{problem: "which has no stage " + target + " for the build's target"}
		}
		stage = stages[i]
	}
	return stage.buildkiteDockerfile
}

// buildkiteDockerfileFromStage starts the stage a FROM instruction opens:
// from an earlier stage it inherits that stage's image and additions, and
// from an image it starts empty. A base reading a build argument without a
// literal default, or one the build overrides, is unknown.
func buildkiteDockerfileFromStage(arguments string, stages []buildkiteDockerfileStage, global map[string]string, overridden map[string]bool) buildkiteDockerfileStage {
	_, words := buildkiteDockerfileFlags(arguments)
	stage := buildkiteDockerfileStage{vars: map[string]string{}}
	if len(words) == 3 && strings.EqualFold(words[1], "as") {
		stage.name = strings.ToLower(words[2])
	}
	if len(words) == 0 {
		stage.problem = "whose FROM names no image"
		return stage
	}
	written := words[0]
	image, unknown := buildkiteDockerfileExpand(written, global)
	switch {
	case unknown != "" && overridden[unknown]:
		stage.problem = "whose base image " + written + " reads the build argument " + unknown + ", which the build overrides with a value ddtest does not read"
		return stage
	case unknown != "":
		stage.problem = "whose base image " + written + " reads the build argument " + unknown + ", which has no literal default ddtest can resolve"
		return stage
	}
	if i := slices.IndexFunc(stages, func(earlier buildkiteDockerfileStage) bool {
		return earlier.name != "" && earlier.name == strings.ToLower(image)
	}); i >= 0 {
		inherited := stages[i]
		inherited.name = stage.name
		inherited.vars = maps.Clone(stages[i].vars)
		return inherited
	}
	if strings.EqualFold(image, "scratch") {
		stage.problem = "which builds FROM scratch, an image without a shell"
		return stage
	}
	stage.image = image
	return stage
}

// buildkiteDockerfileEnv records the values ENV sets: NAME=value pairs, or
// the legacy NAME value form.
func buildkiteDockerfileEnv(arguments string, vars map[string]string) {
	words := strings.Fields(arguments)
	if len(words) > 0 && !strings.Contains(words[0], "=") {
		value, unknown := buildkiteDockerfileExpand(strings.Trim(strings.Join(words[1:], " "), `"'`), vars)
		if unknown == "" {
			vars[words[0]] = value
		} else {
			delete(vars, words[0])
		}
		return
	}
	for _, word := range words {
		name, value, _ := strings.Cut(word, "=")
		value, unknown := buildkiteDockerfileExpand(strings.Trim(value, `"'`), vars)
		if unknown == "" {
			vars[name] = value
		} else {
			delete(vars, name)
		}
	}
}

// buildkiteDockerfilePackageManagers are the package managers whose
// install (apk: add) commands ddtest reads in RUN instructions.
var buildkiteDockerfilePackageManagers = []string{"apt-get", "apt", "apk", "yum", "dnf", "microdnf", "zypper"}

// buildkiteDockerfileSeparators split a RUN instruction's shell commands.
var buildkiteDockerfileSeparators = regexp.MustCompile(`&&|\|\||[;|\n()]`)

// buildkiteDockerfileInstalls reports a RUN instruction installing Bash,
// and curl or wget, with a package manager.
func buildkiteDockerfileInstalls(run string) (bash, downloader bool) {
	// Heredoc bodies keep their own line continuations.
	run = strings.ReplaceAll(strings.ReplaceAll(run, "\\\r\n", " "), "\\\n", " ")
	for _, command := range buildkiteDockerfileSeparators.Split(run, -1) {
		words := strings.Fields(command)
		manager := slices.IndexFunc(words, func(word string) bool { return slices.Contains(buildkiteDockerfilePackageManagers, path.Base(word)) })
		if manager < 0 {
			continue
		}
		install := slices.IndexFunc(words[manager+1:], func(word string) bool { return word == "install" || word == "add" })
		if install < 0 {
			continue
		}
		for _, word := range words[manager+1+install+1:] {
			// apt pins versions with =, apk with = or ~.
			name, _, _ := strings.Cut(strings.Trim(word, `"'`), "=")
			name, _, _ = strings.Cut(name, "~")
			switch name {
			case "bash":
				bash = true
			case "curl", "wget":
				downloader = true
			}
		}
	}
	return bash, downloader
}
