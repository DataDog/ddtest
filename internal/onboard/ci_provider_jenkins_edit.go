// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// onboard --apply enables the Datadog Jenkins plugin in the Jenkinsfiles
// whose jobs run the tests: the only language input is the plugin's language
// token (and the service name the language reads from its project). In a
// declarative pipeline it adds datadog(testOptimization: …) to the top-level
// options directive, creating the directive after agent or environment; in a
// scripted pipeline it wraps each outermost node block that runs the tests.
// An existing datadog(…) call is merged instead: enabled: true, the language
// added to languages, every other argument kept. Every edit is placed with
// the parser's byte spans, keeps the file's indentation and line endings,
// and leaves every other byte unchanged; the result is read again and must
// be a complete setup for every target job, or nothing is written.

// jenkinsServiceName is a service name the edit writes as a literal.
var jenkinsServiceName = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@._/-]*$`)

// repositoryName is the name of the repository at root: its origin
// remote's last path segment, otherwise the checkout directory's name.
func repositoryName(root string) string {
	command := exec.Command("git", "config", "--get", "remote.origin.url")
	command.Dir = root
	if output, err := command.Output(); err == nil {
		url := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(string(output)), "/"), ".git")
		if name := url[strings.LastIndexAny(url, "/:")+1:]; jenkinsServiceName.MatchString(name) {
			return name
		}
	}
	return filepath.Base(root)
}

// jenkinsEdit replaces source[start:end] with text.
type jenkinsEdit struct {
	start, end int
	text       string
}

// jenkinsEditor plans the edits of one Jenkinsfile.
type jenkinsEditor struct {
	file, source, newline string
	tree                  *groovyBlock
	// multiline are the string literals that span lines, whose content
	// re-indenting must not change.
	multiline []groovySpan
	token     string
	service   string
	edits     []jenkinsEdit
	merged    map[*groovyCall]bool
	wrapped   map[*groovyCall]bool
}

// jenkinsShTarget is the sh call that runs a target job's first test
// command, with the commands that must precede the test in its script.
type jenkinsShTarget struct {
	job   string
	line  int
	after []string
}

// planSetupEdits enables the Datadog Jenkins plugin for every target job, or
// explains why it cannot do so safely. Jobs that run in containers, which
// the plugin does not instrument, are left as they are.
func (j jenkins) planSetupEdits(root string, workflows []ciWorkflow, targets []ciSetupTarget, request ciSetupRequest) ([]setupEdit, []string, error) {
	service := request.Service
	if !jenkinsServiceName.MatchString(service) {
		service = repositoryName(root)
	}
	if !jenkinsServiceName.MatchString(service) {
		return nil, nil, &SetupGap{"Jenkins", "ddtest could not derive a service name for serviceName from the project or the checkout directory (" + strconv.Quote(service) + "); add the Datadog Jenkins plugin setup by hand with a serviceName of your choice, as onboard --instructions shows, then validate with testdrive --all."}
	}
	token := jenkinsPluginToken(request.Language)
	byFile := map[string][]jenkinsShTarget{}
	jobs := map[string][]string{}
	containers := []string{}
	for _, target := range targets {
		job, step, ok := gitLabTarget(workflows, target)
		if !ok {
			return nil, nil, &SetupGap{target.Workflow + " / " + target.Job, "The test step is not a Jenkinsfile sh step ddtest can edit."}
		}
		if job.SetupUnsupported != "" {
			containers = append(containers, target.Workflow+" / "+target.Job)
			continue
		}
		line, err := strconv.Atoi(step.Source[strings.LastIndex(step.Source, ":")+1:])
		if err != nil || !strings.HasPrefix(step.Source, target.Workflow+":") {
			return nil, nil, &SetupGap{target.Workflow + " / " + target.Job, "The test step's position in the Jenkinsfile could not be located."}
		}
		after := request.After
		if target.After != nil {
			after = target.After
		}
		byFile[target.Workflow] = append(byFile[target.Workflow], jenkinsShTarget{job: target.Job, line: line, after: after})
		jobs[target.Workflow] = append(jobs[target.Workflow], target.Job)
	}
	if len(byFile) == 0 {
		return nil, nil, &SetupGap{"Jenkins", "Every job that runs the tests runs in a container (" + strings.Join(containers, ", ") + "), which the Datadog Jenkins plugin does not instrument; ddtest does not enable it for them. The user's options are to run those tests on a host agent, or to set up Test Optimization for them by hand, outside ddtest, which ddtest cannot validate."}
	}
	var edits []setupEdit
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		edit, err := readSetupEdit(root, file)
		if err != nil {
			return nil, nil, err
		}
		source := string(edit.before)
		after, err := planJenkinsfileSetup(file, source, token, service, byFile[file])
		if err != nil {
			return nil, nil, err
		}
		if err := checkJenkinsfileSetup(root, j, workflows, file, after, jobs[file], request); err != nil {
			return nil, nil, err
		}
		if after == source {
			continue
		}
		edit.after = []byte(after)
		edits = append(edits, edit)
	}
	return edits, nil, nil
}

// checkJenkinsfileSetup reads the edited Jenkinsfile again: it must keep
// every job, and set up every target job completely.
func checkJenkinsfileSetup(root string, provider ciProvider, workflows []ciWorkflow, file, source string, jobs []string, request ciSetupRequest) error {
	gap := func(reason string) error {
		return &SetupGap{file, "The planned edit would not leave a complete Datadog Jenkins plugin setup, so ddtest wrote nothing: " + reason + " Fix it, or add the setup by hand as onboard --instructions shows, then validate with testdrive --all."}
	}
	edited, err := readJenkinsfile(root, file, source)
	if err != nil {
		return gap("the edited Jenkinsfile cannot be read (" + err.Error() + ").")
	}
	fillStatedRuntimes([]ciWorkflow{edited})
	for _, workflow := range workflows {
		if workflow.Path == file && !slices.Equal(slices.Sorted(maps.Keys(workflow.Jobs)), slices.Sorted(maps.Keys(edited.Jobs))) {
			return gap("the edited Jenkinsfile reads as other jobs than the original.")
		}
	}
	// The plugin's variables reach every step in the option's scope, so a
	// job there that runs steps in a container refuses the edit.
	var containers []string
	for _, name := range slices.Sorted(maps.Keys(edited.Jobs)) {
		job := edited.Jobs[name]
		if job.SetupUnsupported != "" && !disabledCondition(job.If) && len(job.Steps) > 1 && job.Steps[0].Setup != nil {
			containers = append(containers, file+" / "+name)
		}
	}
	if len(containers) > 0 {
		return &SetupGap{file, "ddtest does not enable the Datadog Jenkins plugin here: its option would also apply to " + strings.Join(containers, ", ") + ", which run steps in a container. The plugin sets its tracer variables in every step of its scope, container steps included, but installs the tracer only on the build agent, so processes in those containers, tests or not, can fail to load it. The user's options are to run those stages on a host agent, or to keep them out of the option's scope (in a scripted pipeline, a node block of their own outside the wrap), then rerun onboard --apply; or to set up Test Optimization for them by hand, outside ddtest, which ddtest cannot validate. Do not move stages without the user's approval."}
	}
	language := discoveryLanguageName(request.Framework)
	for _, name := range jobs {
		if problem := providerJobSetupProblem(root, provider, edited, name, language, request.Framework, request.Site); problem != "" {
			return gap(file + " / " + name + ": " + problem)
		}
	}
	return nil
}

// planJenkinsfileSetup returns source with the plugin enabled for targets.
func planJenkinsfileSetup(file, source, token, service string, targets []jenkinsShTarget) (string, error) {
	tree, err := parseGroovy(source)
	if err != nil {
		return "", &SetupGap{file, "The Jenkinsfile cannot be parsed: " + err.Error()}
	}
	tokens, _ := lexGroovy(source)
	e := &jenkinsEditor{file: file, source: source, newline: "\n", tree: tree, token: token, service: service, merged: map[*groovyCall]bool{}, wrapped: map[*groovyCall]bool{}}
	if strings.Contains(source, "\r\n") {
		e.newline = "\r\n"
	}
	for _, token := range tokens {
		if token.kind == groovyStringToken && strings.Contains(token.text, "\n") {
			e.multiline = append(e.multiline, groovySpan{token.start, token.end})
		}
	}
	if err := e.plan(targets); err != nil {
		return "", err
	}
	return e.apply()
}

// gap refuses the edit at node, pointing at instructions.
func (e *jenkinsEditor) gap(node groovyNode, reason string) error {
	location := e.file
	if node != nil {
		start, _ := node.span()
		location += ":" + strconv.Itoa(groovyLine(e.source, start))
	}
	return &SetupGap{location, reason + " Add the Datadog Jenkins plugin setup by hand as onboard --instructions shows, then validate with testdrive --all."}
}

// groovyVisit calls visit for node and every node inside it, with the
// nodes that enclose it, outermost first.
func groovyVisit(node groovyNode, ancestors []groovyNode, visit func(node groovyNode, ancestors []groovyNode)) {
	if node == nil {
		return
	}
	visit(node, ancestors)
	inner := append(slices.Clone(ancestors), node)
	children := []groovyNode{}
	switch node := node.(type) {
	case *groovyBlock:
		children = node.Statements
	case *groovyCall:
		if node.Receiver != nil {
			children = append(children, node.Receiver)
		}
		for _, arg := range node.Args {
			children = append(children, arg.Value)
		}
		if node.Closure != nil {
			children = append(children, node.Closure)
		}
	case *groovyClosure:
		children = append(children, node.Body)
	case *groovyControl:
		children = append(children, node.Condition)
		for _, block := range append(append(slices.Clone(node.Blocks), node.Catches...), node.Finally) {
			if block != nil {
				children = append(children, block)
			}
		}
	case *groovyAssign:
		children = append(children, node.Target, node.Value)
	case *groovyFunction:
		children = append(children, node.Body)
	case *groovyReturn:
		children = append(children, node.Value)
	case *groovyBinary:
		children = append(children, node.Left, node.Right)
	case *groovyList:
		children = node.Items
	case *groovyMap:
		for _, entry := range node.Entries {
			children = append(children, entry.Value)
		}
	}
	for _, child := range children {
		groovyVisit(child, inner, visit)
	}
}

// jenkinsStep reports a call of the Pipeline step name.
func jenkinsStep(node groovyNode, name string) (*groovyCall, bool) {
	call, ok := node.(*groovyCall)
	return call, ok && call.Receiver == nil && call.Name == name
}

func (e *jenkinsEditor) plan(targets []jenkinsShTarget) error {
	var pipeline *groovyCall
	// A scripted pipeline's node blocks inside functions stay unwrapped.
	var functions []error
	for _, statement := range e.tree.Statements {
		switch node := statement.(type) {
		case *groovyAnnotation:
			if node.Name == "Library" {
				return e.gap(node, "The Jenkinsfile loads a shared library (@Library), which may declare the pipeline, its options or its node blocks itself; ddtest does not edit pipelines that use shared libraries.")
			}
		case *groovyCall:
			if node.Receiver == nil && node.Name == "library" {
				return e.gap(node, "The Jenkinsfile loads a shared library (library …), which may declare the pipeline, its options or its node blocks itself; ddtest does not edit pipelines that use shared libraries.")
			}
			if node.Receiver == nil && node.Name == "pipeline" && node.Closure != nil {
				if pipeline != nil {
					return e.gap(node, "The Jenkinsfile declares several pipeline blocks; ddtest cannot tell which one Jenkins runs.")
				}
				pipeline = node
			}
		case *groovyAssign:
			if _, ok := jenkinsStep(node.Value, "library"); ok {
				return e.gap(node, "The Jenkinsfile loads a shared library (library …), which may declare the pipeline, its options or its node blocks itself; ddtest does not edit pipelines that use shared libraries.")
			}
		case *groovyFunction:
			problem := ""
			groovyVisit(node.Body, nil, func(inner groovyNode, _ []groovyNode) {
				if call, ok := jenkinsStep(inner, "node"); ok && problem == "" && call.Closure != nil && jenkinsRunsCommands(call.Closure.Body) {
					problem = "The Groovy function " + node.Name + "() runs a node block with shell steps, which may run the tests; ddtest wraps only node blocks the script runs directly, so it would leave that one uninstrumented."
				}
			})
			if problem != "" {
				functions = append(functions, e.gap(node, problem))
			}
		}
	}
	if pipeline == nil && len(functions) > 0 {
		return functions[0]
	}
	if pipeline != nil {
		if libraries, ok := jenkinsDirectives(pipeline.Closure.Body)["libraries"]; ok {
			return e.gap(libraries, "The pipeline loads shared libraries (libraries directive), which may declare its options themselves; ddtest does not edit pipelines that use shared libraries.")
		}
	}
	// The sh calls of each target line, with what encloses them.
	type located struct {
		call      *groovyCall
		ancestors []groovyNode
	}
	byLine := map[int][]located{}
	groovyVisit(e.tree, nil, func(node groovyNode, ancestors []groovyNode) {
		if call, ok := jenkinsStep(node, "sh"); ok {
			line := groovyLine(e.source, call.Start)
			byLine[line] = append(byLine[line], located{call, ancestors})
		}
	})
	options := false
	esm := map[*groovyCall][]string{}
	for _, target := range targets {
		calls := byLine[target.line]
		if len(calls) == 0 {
			return e.gap(nil, "The sh step of "+target.job+" at line "+strconv.Itoa(target.line)+" could not be located.")
		}
		if len(target.after) > 0 {
			if len(calls) > 1 {
				return &EditThenApplyGap{SetupGap{e.file + ":" + strconv.Itoa(target.line), "Several sh steps share this line, so ddtest cannot tell which one runs the tests. Add these commands inside the sh script that runs the tests, before the test command, then rerun onboard --apply, which enables the Datadog Jenkins plugin:\n" + strings.Join(target.after, "\n")}}
			}
			esm[calls[0].call] = target.after
		}
		for _, current := range calls {
			var datadogs []*groovyCall
			var node *groovyCall
			inPipeline := false
			for _, ancestor := range current.ancestors {
				if call, ok := jenkinsStep(ancestor, "datadog"); ok && call.Closure != nil {
					datadogs = append(datadogs, call)
				}
				if call, ok := jenkinsStep(ancestor, "node"); ok && node == nil {
					node = call
				}
				if ancestor == pipeline {
					inPipeline = true
				}
			}
			switch {
			case len(datadogs) > 1:
				return e.gap(datadogs[1], "Several datadog(…) calls enclose the tests of "+target.job+"; ddtest cannot tell which arguments the plugin applies.")
			case len(datadogs) == 1:
				if err := e.merge(datadogs[0]); err != nil {
					return err
				}
			case pipeline != nil && !inPipeline:
				return e.gap(current.call, "The tests of "+target.job+" run outside the declarative pipeline block, where its options do not apply.")
			case pipeline != nil:
				options = true
			case node == nil:
				return e.gap(current.call, "The tests of "+target.job+" run outside a node block ddtest can wrap.")
			default:
				if err := e.wrap(node); err != nil {
					return err
				}
			}
		}
	}
	if options {
		if err := e.options(pipeline); err != nil {
			return err
		}
	}
	for _, call := range slices.SortedFunc(maps.Keys(esm), func(a, b *groovyCall) int { return a.Start - b.Start }) {
		if err := e.prepend(call, esm[call]); err != nil {
			return err
		}
	}
	return nil
}

// apply makes the planned edits, which must not overlap.
func (e *jenkinsEditor) apply() (string, error) {
	slices.SortFunc(e.edits, func(a, b jenkinsEdit) int { return b.start - a.start })
	text := e.source
	end := len(text) + 1
	for _, edit := range e.edits {
		if edit.end > end || edit.start > edit.end {
			return "", &SetupGap{e.file, "The planned edits overlap; add the Datadog Jenkins plugin setup by hand as onboard --instructions shows."}
		}
		text = text[:edit.start] + edit.text + text[edit.end:]
		end = edit.start
	}
	return text, nil
}

func (e *jenkinsEditor) add(start, end int, text string) {
	e.edits = append(e.edits, jenkinsEdit{start, end, text})
}

// optionsMap is the testOptimization argument ddtest writes.
func (e *jenkinsEditor) optionsMap() string {
	return "[enabled: true, serviceName: " + groovyQuote(e.service, "'") + ", languages: [" + groovyQuote(e.token, "'") + "]]"
}

// datadogCall is the datadog(…) call ddtest writes.
func (e *jenkinsEditor) datadogCall() string {
	return "datadog(testOptimization: " + e.optionsMap() + ")"
}

// groovyQuote writes text as a Groovy string with quote, ' or ".
func groovyQuote(text, quote string) string {
	if quote == "'" {
		return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(text) + "'"
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(text) + `"`
}

// lineStart is the offset where the line holding offset starts.
func (e *jenkinsEditor) lineStart(offset int) int {
	return strings.LastIndex(e.source[:offset], "\n") + 1
}

// lineEnd is the offset of the line break after offset (before a \r\n), or
// the end of the file.
func (e *jenkinsEditor) lineEnd(offset int) int {
	end := strings.Index(e.source[offset:], "\n")
	if end < 0 {
		return len(e.source)
	}
	end += offset
	if end > offset && e.source[end-1] == '\r' {
		end--
	}
	return end
}

// indent is the leading whitespace of the line holding offset.
func (e *jenkinsEditor) indent(offset int) string {
	line := e.source[e.lineStart(offset):]
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// startsLine reports whether only whitespace precedes offset on its line.
func (e *jenkinsEditor) startsLine(offset int) bool {
	return strings.TrimLeft(e.source[e.lineStart(offset):offset], " \t") == ""
}

// endsLine reports whether only whitespace or a line comment follows offset
// on its line.
func (e *jenkinsEditor) endsLine(offset int) bool {
	rest := strings.TrimLeft(e.source[offset:e.lineEnd(offset)], " \t")
	return rest == "" || strings.HasPrefix(rest, "//")
}

func (e *jenkinsEditor) sameLine(a, b int) bool {
	return groovyLine(e.source, a) == groovyLine(e.source, b)
}

// unit is the file's indentation step: inner's indentation beyond outer's
// when it is deeper, otherwise the smallest indentation of the file's code
// lines (a tab when they indent with tabs), or four spaces.
func (e *jenkinsEditor) unit(outer, inner string) string {
	if strings.HasPrefix(inner, outer) && len(inner) > len(outer) {
		return inner[len(outer):]
	}
	unit := ""
	offset := 0
	for _, line := range strings.SplitAfter(e.source, "\n") {
		content := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimLeft(content, " \t")
		indent := content[:len(content)-len(trimmed)]
		code := trimmed != "" && !strings.HasPrefix(trimmed, "*") && !e.inString(offset)
		offset += len(line)
		switch {
		case !code || indent == "":
		case indent[0] == '\t':
			return "\t"
		case len(indent) >= 2 && (unit == "" || len(indent) < len(unit)):
			unit = indent
		}
	}
	if unit == "" || len(unit) > 8 {
		return "    "
	}
	return unit
}

// inString reports an offset inside a multi-line string literal.
func (e *jenkinsEditor) inString(offset int) bool {
	return slices.ContainsFunc(e.multiline, func(span groovySpan) bool { return offset > span.Start && offset < span.End })
}

// options enables the plugin in the declarative pipeline's top-level options
// directive, creating it after agent or environment.
func (e *jenkinsEditor) options(pipeline *groovyCall) error {
	body := pipeline.Closure.Body
	var options, anchor groovyNode
	for _, statement := range body.Statements {
		name := ""
		switch node := statement.(type) {
		case *groovyCall:
			if node.Receiver == nil {
				name = node.Name
			}
		case *groovyName:
			if len(node.Path) == 1 {
				name = node.Path[0]
			}
		}
		switch name {
		case "options":
			if options != nil {
				return e.gap(statement, "The pipeline has several options directives.")
			}
			options = statement
		case "agent", "environment":
			anchor = statement
		}
	}
	if options != nil {
		call, ok := options.(*groovyCall)
		if !ok || call.Closure == nil || len(call.Args) > 0 {
			return e.gap(options, "The pipeline's options directive is not a block ddtest can read.")
		}
		return e.optionsBlock(call)
	}
	if len(body.Statements) == 0 || e.sameLine(pipeline.Closure.Start, pipeline.Closure.End-1) {
		return e.gap(pipeline, "The pipeline block is written on one line, where ddtest cannot place an options directive.")
	}
	first, _ := body.Statements[0].span()
	if anchor != nil {
		first, _ = anchor.span()
	}
	sibling := e.indent(first)
	block := sibling + "options {" + e.newline + sibling + e.unit(e.indent(pipeline.Start), sibling) + e.datadogCall() + e.newline + sibling + "}"
	if anchor == nil {
		if !e.startsLine(first) {
			return e.gap(pipeline, "The pipeline's first directive does not start its line, where ddtest cannot place an options directive.")
		}
		e.add(e.lineStart(first), e.lineStart(first), block+e.newline)
		return nil
	}
	if _, end := anchor.span(); !e.endsLine(end) {
		return e.gap(anchor, "Another statement follows the pipeline's agent or environment directive on its line, where ddtest cannot place an options directive.")
	} else {
		position := e.lineEnd(end)
		e.add(position, position, e.newline+block)
	}
	return nil
}

// optionsBlock enables the plugin in an existing options directive: it
// merges into its datadog(…) call, or adds one.
func (e *jenkinsEditor) optionsBlock(options *groovyCall) error {
	var datadogs []*groovyCall
	for _, statement := range options.Closure.Body.Statements {
		if call, ok := jenkinsStep(statement, "datadog"); ok {
			datadogs = append(datadogs, call)
		}
	}
	if len(datadogs) > 0 {
		for _, call := range datadogs[1:] {
			if groovyText(e.source, call) != groovyText(e.source, datadogs[0]) {
				return e.gap(call, "The options directive has several datadog(…) calls with different arguments; ddtest cannot tell which ones the plugin applies.")
			}
		}
		for _, call := range datadogs {
			if err := e.merge(call); err != nil {
				return err
			}
		}
		return nil
	}
	closure := options.Closure
	sibling := e.indent(options.Start)
	statements := closure.Body.Statements
	if len(statements) == 0 {
		if e.sameLine(closure.Start, closure.End-1) {
			e.add(closure.Start, closure.End, "{"+e.newline+sibling+e.unit(e.indent(options.Start), "")+e.datadogCall()+e.newline+sibling+"}")
			return nil
		}
		position := e.lineStart(closure.End - 1)
		if !e.startsLine(closure.End - 1) {
			position = closure.End - 1
			e.add(position, position, e.newline+sibling+e.unit(sibling, "")+e.datadogCall()+e.newline+sibling)
			return nil
		}
		e.add(position, position, sibling+e.unit(sibling, "")+e.datadogCall()+e.newline)
		return nil
	}
	_, end := statements[len(statements)-1].span()
	if e.endsLine(end) && !e.sameLine(closure.Start, end) {
		start, _ := statements[len(statements)-1].span()
		position := e.lineEnd(end)
		e.add(position, position, e.newline+e.indent(start)+e.datadogCall())
		return nil
	}
	// Options written on one line take another statement after a ;.
	e.add(end, end, "; "+e.datadogCall())
	return nil
}

// wrap encloses a scripted node block in datadog(…) { … }, indenting its
// lines one step; lines inside multi-line strings keep their bytes.
func (e *jenkinsEditor) wrap(node *groovyCall) error {
	if e.wrapped[node] {
		return nil
	}
	e.wrapped[node] = true
	if node.Closure == nil {
		return e.gap(node, "The node step has no block ddtest can wrap.")
	}
	if !e.startsLine(node.Start) || !e.endsLine(node.End) {
		return e.gap(node, "The node block shares its first or last line with other statements, where ddtest cannot wrap it.")
	}
	outer := e.indent(node.Start)
	inner := ""
	if statements := node.Closure.Body.Statements; len(statements) > 0 {
		if start, _ := statements[0].span(); !e.sameLine(node.Start, start) {
			inner = e.indent(start)
		}
	}
	unit := e.unit(outer, inner)
	start, end := e.lineStart(node.Start), e.lineEnd(node.End)
	var text strings.Builder
	text.WriteString(outer + "datadog(testOptimization: " + e.optionsMap() + ") {" + e.newline)
	for offset := start; ; {
		lineEnd := min(e.lineEnd(offset), end)
		line := e.source[offset:lineEnd]
		if strings.TrimSpace(line) != "" && !e.inString(offset) {
			text.WriteString(unit)
		}
		text.WriteString(line)
		if lineEnd >= end {
			break
		}
		next := strings.Index(e.source[lineEnd:], "\n") + lineEnd + 1
		text.WriteString(e.source[lineEnd:next])
		offset = next
	}
	text.WriteString(e.newline + outer + "}")
	e.add(start, end, text.String())
	return nil
}

// merge enables the plugin in an existing datadog(…) call: it adds
// testOptimization, or sets enabled: true and adds the language to
// languages in it (or in its legacy testVisibility), keeping every other
// argument. Arguments it would change must be literal.
func (e *jenkinsEditor) merge(call *groovyCall) error {
	if e.merged[call] {
		return nil
	}
	e.merged[call] = true
	key, value, ok := jenkinsTestOptimization(call)
	if !ok {
		argument := "testOptimization: " + e.optionsMap()
		switch {
		case len(call.Args) > 0:
			last := call.Args[len(call.Args)-1]
			lastStart, lastEnd := last.Value.span()
			if last.Name != "" {
				lastStart = last.NameStart
			}
			e.append(call.NameStart, lastStart, lastEnd, argument)
		case call.Parens:
			open := strings.Index(e.source[call.NameStart:], "(") + call.NameStart
			e.add(open+1, open+1, argument)
		default:
			position := call.NameStart + len(call.Name)
			e.add(position, position, "("+argument+")")
		}
		return nil
	}
	options, literal := value.(*groovyMap)
	if !literal {
		return e.gap(call, "The datadog("+key+": …) argument is computed, not a literal map, so ddtest cannot merge the setup into it.")
	}
	if len(options.Entries) == 0 {
		e.add(options.Start, options.End, e.optionsMap())
		return nil
	}
	fields := map[string]groovyArg{}
	for _, entry := range options.Entries {
		if entry.Name == "\x00" {
			return e.gap(call, "The datadog("+key+": …) map has a computed key, so ddtest cannot merge the setup into it.")
		}
		fields[entry.Name] = entry
	}
	var missing []string
	if enabled, ok := fields["enabled"]; !ok {
		missing = append(missing, "enabled: true")
	} else if literal, ok := enabled.Value.(*groovyLiteral); !ok || literal.Text != "true" && literal.Text != "false" {
		return e.gap(call, "The datadog("+key+": …) map sets enabled to the computed value "+groovyText(e.source, enabled.Value)+", so ddtest cannot set it to true.")
	} else if literal.Text == "false" {
		e.add(literal.Start, literal.End, "true")
	}
	if service, ok := fields["serviceName"]; !ok {
		missing = append(missing, "serviceName: "+groovyQuote(e.service, "'"))
	} else if text, ok := service.Value.(*groovyString); ok {
		if value, literal := text.Value.literal(); literal && strings.TrimSpace(value) == "" {
			e.add(text.Start, text.End, groovyQuote(e.service, "'"))
		}
	}
	if languages, ok := fields["languages"]; !ok {
		missing = append(missing, "languages: ["+groovyQuote(e.token, "'")+"]")
	} else if err := e.language(call, key, languages.Value); err != nil {
		return err
	}
	if len(missing) > 0 {
		last := options.Entries[len(options.Entries)-1]
		_, lastEnd := last.Value.span()
		e.append(options.Start, last.NameStart, lastEnd, strings.Join(missing, ", "))
	}
	return nil
}

// language adds the plugin's language token to a literal languages list.
func (e *jenkinsEditor) language(call *groovyCall, key string, value groovyNode) error {
	list, ok := value.(*groovyList)
	if !ok {
		return e.gap(call, "The datadog("+key+": …) map sets languages to the computed value "+groovyText(e.source, value)+", so ddtest cannot add "+e.token+" to it.")
	}
	quote := "'"
	for _, item := range list.Items {
		text, ok := item.(*groovyString)
		value, literal := "", false
		if ok {
			value, literal = text.Value.literal()
		}
		if !literal {
			return e.gap(call, "The datadog("+key+": …) languages list has the computed item "+groovyText(e.source, item)+", so ddtest cannot add "+e.token+" to it.")
		}
		if strings.EqualFold(strings.TrimSpace(value), e.token) {
			return nil
		}
		if text.Value.quote == `"` {
			quote = `"`
		}
	}
	if len(list.Items) == 0 {
		e.add(list.Start, list.End, "["+groovyQuote(e.token, "'")+"]")
		return nil
	}
	lastStart, lastEnd := list.Items[len(list.Items)-1].span()
	e.append(list.Start, lastStart, lastEnd, groovyQuote(e.token, quote))
	return nil
}

// append adds item after the last of a comma-separated sequence opened at
// open: on its own line, with the last item's indentation, when the items
// start on lines of their own; after a trailing comma, keeping one after it.
func (e *jenkinsEditor) append(open, lastStart, lastEnd int, item string) {
	rest := e.source[lastEnd:]
	trimmed := strings.TrimLeft(rest, " \t\r\n")
	trailing := strings.HasPrefix(trimmed, ",")
	multiline := !e.sameLine(open, lastStart) && e.startsLine(lastStart)
	switch {
	case trailing && multiline:
		position := lastEnd + len(rest) - len(trimmed) + 1
		e.add(position, position, e.newline+e.indent(lastStart)+item+",")
	case trailing:
		position := lastEnd + len(rest) - len(trimmed) + 1
		e.add(position, position, " "+item+",")
	case multiline:
		e.add(lastEnd, lastEnd, ","+e.newline+e.indent(lastStart)+item)
	default:
		e.add(lastEnd, lastEnd, ", "+item)
	}
}

// prepend adds commands to the start of an sh step's script, which must be
// a single-quoted literal: they then run in the test's shell, before it.
func (e *jenkinsEditor) prepend(call *groovyCall, commands []string) error {
	location := e.file + ":" + strconv.Itoa(groovyLine(e.source, call.Start))
	manual := func(reason string) error {
		return &EditThenApplyGap{SetupGap{location, reason + " Add these commands inside the sh script that runs the tests, before the test command, then rerun onboard --apply, which enables the Datadog Jenkins plugin:\n" + strings.Join(commands, "\n")}}
	}
	positional, named := jenkinsArgs(call)
	node := firstNode(positional)
	if value, ok := named["script"]; ok {
		node = value
	}
	script, ok := node.(*groovyString)
	if !ok || script.Value.quote != "'" && script.Value.quote != "'''" {
		return manual("The sh script that runs the tests is not a single-quoted string literal, which ddtest edits only.")
	}
	text, _ := script.Value.literal()
	if !slices.ContainsFunc(commands, func(command string) bool { return !strings.Contains(text, command) }) {
		return nil
	}
	if first, _, _ := strings.Cut(strings.TrimLeft(text, " \t\r\n"), "\n"); strings.HasPrefix(first, "#!") {
		return manual("The sh script that runs the tests starts with a shebang, which ddtest does not edit.")
	}
	if slices.ContainsFunc(commands, func(command string) bool { return strings.ContainsAny(command, "'\\\n") }) {
		return manual("These commands cannot be written into a single-quoted string as they are.")
	}
	content := script.Start + len(script.Value.quote)
	body := e.source[content : script.End-len(script.Value.quote)]
	if script.Value.quote == "'''" && strings.TrimLeft(body, " \t\r\n") != body && strings.Contains(body[:len(body)-len(strings.TrimLeft(body, " \t\r\n"))], "\n") {
		// The script starts on the next line: one line per command, with its
		// first line's indentation.
		leading := body[:len(body)-len(strings.TrimLeft(body, " \t\r\n"))]
		position := content + strings.LastIndex(leading, "\n") + 1
		indent := e.indent(content + len(leading))
		var lines strings.Builder
		for _, command := range commands {
			lines.WriteString(indent + command + e.newline)
		}
		e.add(position, position, lines.String())
		return nil
	}
	e.add(content, content, strings.Join(commands, "; ")+"; ")
	return nil
}
