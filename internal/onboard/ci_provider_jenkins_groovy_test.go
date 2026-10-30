// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// groovyStatements parses source and returns its top-level statements.
func groovyStatements(t *testing.T, source string) []groovyNode {
	t.Helper()
	tree, err := parseGroovy(source)
	require.NoError(t, err)
	return tree.Statements
}

func TestGroovyLexerStrings(t *testing.T) {
	for _, tc := range []struct {
		name, source, literal string
		interpolated          []string
	}{
		{name: "single quotes", source: `'npm test'`, literal: "npm test"},
		{name: "escapes", source: `'it\'s \\ ok'`, literal: `it's \ ok`},
		{name: "double quotes", source: `"npm test"`, literal: "npm test"},
		{name: "triple single quotes keep $", source: "'''\nnpm test $HOME\n'''", literal: "\nnpm test $HOME\n"},
		{name: "triple double quotes", source: "\"\"\"\nnpm test\n\"\"\"", literal: "\nnpm test\n"},
		{name: "line continuation in a string", source: "\"a \\\nb\"", literal: "a b"},
		{name: "escaped dollar", source: `"cost \$5"`, literal: "cost $5"},
		{name: "a lone dollar", source: `"a $ b"`, literal: "a $ b"},
		{name: "braced interpolation", source: `"run ${env.TARGET} now"`, interpolated: []string{"env.TARGET"}},
		{name: "nested braces and strings", source: `"${ sh(script: 'echo }', returnStdout: true).trim() }"`, interpolated: []string{` sh(script: 'echo }', returnStdout: true).trim() `}},
		{name: "name interpolation", source: `"$X.y."`, interpolated: []string{"X.y"}},
		{name: "slashy", source: `x = /a\/b/`},
		{name: "dollar slashy", source: `x = $/a/b/$`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := lexGroovy(tc.source)
			require.NoError(t, err)
			var str *groovyStr
			for _, token := range tokens {
				if token.kind == groovyStringToken {
					str = token.str
				}
			}
			require.NotNil(t, str)
			if tc.literal != "" {
				value, ok := str.literal()
				require.True(t, ok)
				require.Equal(t, tc.literal, value)
			}
			var expressions []string
			for _, part := range str.parts {
				if part.interpolated && str.quote != "/" && str.quote != "$/" {
					expressions = append(expressions, part.expr)
				}
			}
			require.Equal(t, tc.interpolated, expressions)
		})
	}
}

func TestGroovyLexerSkipsCommentsAndKeepsSpans(t *testing.T) {
	source := "#!groovy\n// a comment with 'quotes\n/* block\n  with \"quotes */ sh 'x' // trailing\n"
	tokens, err := lexGroovy(source)
	require.NoError(t, err)
	var kinds []string
	for _, token := range tokens {
		if token.kind == groovyIdent || token.kind == groovyStringToken {
			kinds = append(kinds, source[token.start:token.end])
		}
	}
	require.Equal(t, []string{"sh", "'x'"}, kinds)
}

func TestGroovyLexerErrorsAndLimits(t *testing.T) {
	for source, message := range map[string]string{
		"sh 'unterminated":               "line 1: unterminated string",
		"sh \"\"\"never closed":          "unterminated string",
		"/* open":                        "unterminated comment",
		"x = \"${ open\"":                "unterminated",
		strings.Repeat(" ", 3<<20):       "larger than",
		strings.Repeat("a ", 100001):     "more than 100000 tokens",
		"sh 'a'\nsh 'b\nsh 'c'":          "line 2: unterminated string",
		"x = $/never closed":             "unterminated dollar-slashy string",
		"pipeline { /regex never closed": "unterminated slashy string",
	} {
		_, err := parseGroovy(source)
		require.ErrorContains(t, err, message, source)
	}
}

func TestGroovyParserCalls(t *testing.T) {
	statements := groovyStatements(t, `pipeline {
  agent { label 'sl7' }
  stages {
    stage('Test')
    {
      steps {
        sh 'npm ci'
        sh script: 'npm test', label: 'tests'
        junit testResults: 'out.xml', allowEmptyResults: true
        checkout scm
        def out = sh(returnStdout: true, script: 'git rev-parse HEAD').trim()
        docker.image('node:22')
          .inside('-u root') { sh 'npm test' }
      }
    }
  }
}`)
	require.Len(t, statements, 1)
	pipeline := statements[0].(*groovyCall)
	require.Equal(t, "pipeline", pipeline.Name)
	stages := pipeline.Closure.Body.Statements[1].(*groovyCall)
	stage := stages.Closure.Body.Statements[0].(*groovyCall)
	require.Equal(t, "stage", stage.Name)
	require.NotNil(t, stage.Closure, "an Allman-style closure belongs to its call")
	steps := stage.Closure.Body.Statements[0].(*groovyCall).Closure.Body.Statements
	require.Len(t, steps, 6)
	sh := steps[0].(*groovyCall)
	require.Equal(t, "sh", sh.Name)
	require.False(t, sh.Parens)
	named := steps[1].(*groovyCall)
	require.Equal(t, "script", named.Args[0].Name)
	require.Equal(t, "label", named.Args[1].Name)
	require.Equal(t, "checkout", steps[3].(*groovyCall).Name)
	assign := steps[4].(*groovyAssign)
	require.True(t, assign.Declared)
	trim := assign.Value.(*groovyCall)
	require.Equal(t, "trim", trim.Name)
	require.Equal(t, "sh", trim.Receiver.(*groovyCall).Name)
	inside := steps[5].(*groovyCall)
	require.Equal(t, "inside", inside.Name, "a chain continues on the next line")
	require.Equal(t, "docker.image", inside.Receiver.(*groovyCall).Name)
	require.NotNil(t, inside.Closure)
}

func TestGroovyParserKeepsByteSpans(t *testing.T) {
	source := "node {\n  sh '''\n    npm test\n  '''\n}\n"
	statements := groovyStatements(t, source)
	sh := statements[0].(*groovyCall).Closure.Body.Statements[0].(*groovyCall)
	require.Equal(t, "sh '''\n    npm test\n  '''", groovyText(source, sh))
	require.Equal(t, "sh", source[sh.NameStart:sh.NameStart+2])
	require.Equal(t, "'''\n    npm test\n  '''", groovyText(source, sh.Args[0].Value))
	require.Equal(t, `'''`, sh.Args[0].Value.(*groovyString).Value.quote)
}

func TestGroovyParserCollectionsAndClosures(t *testing.T) {
	statements := groovyStatements(t, `datadog(testOptimization: [enabled: true, languages: ['JAVASCRIPT', "RUBY"], additionalVariables: [:], empty: []])
parallel(a: { sh 'x' }, b: { item -> sh "y" })
versions.each { ver ->
  sh "test ${ver}"
}`)
	datadog := statements[0].(*groovyCall)
	options := datadog.Args[0].Value.(*groovyMap)
	require.Equal(t, "testOptimization", datadog.Args[0].Name)
	require.Equal(t, []string{"enabled", "languages", "additionalVariables", "empty"}, []string{options.Entries[0].Name, options.Entries[1].Name, options.Entries[2].Name, options.Entries[3].Name})
	require.Len(t, options.Entries[1].Value.(*groovyList).Items, 2)
	require.IsType(t, &groovyMap{}, options.Entries[2].Value)
	require.IsType(t, &groovyList{}, options.Entries[3].Value)
	parallel := statements[1].(*groovyCall)
	require.Equal(t, []string{"item"}, parallel.Args[1].Value.(*groovyClosure).Params)
	each := statements[2].(*groovyCall)
	require.Equal(t, "versions.each", each.Name)
	require.Equal(t, []string{"ver"}, each.Closure.Params)
}

func TestGroovyParserControlFlowAndDeclarations(t *testing.T) {
	statements := groovyStatements(t, `@Library(['common@v2', 'extra']) _
import groovy.json.JsonSlurper
def versions = [3.3, 3.4]
final String NAME = 'x'
def build(String target) {
  sh "make ${target}"
}
if (env.BRANCH_NAME == 'main') {
  sh 'deploy'
} else if (x) sh 'other'
else {
  sh 'none'
}
try {
  sh 'npm test'
} catch (err) {
  sh 'notify'
} finally {
  junit 'out.xml'
}
for (v in versions) { sh "echo $v" }
switch (x) {
  case 'a':
    sh 'a'
    break
  default:
    sh 'b'
}
throw new Exception('x')
counter++
x ? a : b
`)
	require.IsType(t, &groovyAnnotation{}, statements[0])
	require.Equal(t, "Library", statements[0].(*groovyAnnotation).Name)
	require.IsType(t, &groovyAssign{}, statements[1])
	require.Equal(t, "NAME", statements[2].(*groovyAssign).Target.(*groovyName).Path[0])
	require.Equal(t, "build", statements[3].(*groovyFunction).Name)
	ifStatement := statements[4].(*groovyControl)
	require.Equal(t, "if", ifStatement.Kind)
	require.Len(t, ifStatement.Blocks, 2)
	nested := ifStatement.Blocks[1].Statements[0].(*groovyControl)
	require.Len(t, nested.Blocks, 2, "else if chains keep the last else")
	try := statements[5].(*groovyControl)
	require.Len(t, try.Catches, 1)
	require.NotNil(t, try.Finally)
	require.Equal(t, "for", statements[6].(*groovyControl).Kind)
	require.Equal(t, "switch", statements[7].(*groovyControl).Kind)
	require.Equal(t, "throw", statements[8].(*groovyAssert).Kind, "a throw's value runs")
	require.IsType(t, &groovyBinary{}, statements[9], "counter++")
	ternary := statements[10].(*groovyTernary)
	require.Equal(t, []string{"x"}, ternary.Condition.(*groovyName).Path)
	require.Equal(t, []string{"a"}, ternary.Then.(*groovyName).Path)
	require.Equal(t, []string{"b"}, ternary.Else.(*groovyName).Path)
	require.Len(t, statements, 11, "imports run nothing")
}

func TestGroovyParserRecoversWithUnknownNodes(t *testing.T) {
	statements := groovyStatements(t, "node {\n  sh 'a' ; ) garbage (\n  sh 'b'\n}\n} stray\n")
	body := statements[0].(*groovyCall).Closure.Body.Statements
	require.Equal(t, "sh", body[0].(*groovyCall).Name)
	require.IsType(t, &groovyUnknown{}, body[1])
	require.Equal(t, "sh", body[2].(*groovyCall).Name)
	require.IsType(t, &groovyUnknown{}, statements[1], "a stray brace")
}

// Real Jenkinsfile idioms parse into readable nodes.
func TestGroovyParserCorpus(t *testing.T) {
	for name, source := range map[string]string{
		"interpolated map keys":             "def tests = [\n  \"Unit - ${env.STAGE_NAME}\": {\n    withCredentials([string(credentialsId: 'x', variable: 'Y')]) { sh 'a' }\n  },\n]\n",
		"environment with GString commands": "pipeline {\n  environment {\n    RECIPIENTS = \"${ \\\n      sh(returnStdout: true, script: 'echo $X').trim() \\\n    }\"\n    PATH = \"${WORKSPACE}/node_modules/.bin:${env.PATH}\"\n  }\n}\n",
		"stripMargin":                       "x = '''a\n  |b'''.stripMargin()\n",
		"legacy stage":                      "node {\n  stage 'Build'\n  sh 'make'\n}\n",
		"properties":                        "properties([parameters([string(name: 'X', defaultValue: 'y')])])\n",
		"step class":                        "step([$class: 'WsCleanup'])\n",
		"closure in argument":               "timeout(time: 10, unit: 'MINUTES') {\n  retry(2) { sh 'npm test' }\n}\n",
		"method on string":                  "sh \"echo ${'a'.toUpperCase()}\"\n",
		"semicolons":                        "node { sh 'a'; sh 'b' }\n",
		"regex match":                       "if (env.BRANCH_NAME ==~ /release-.*/) { sh 'x' }\n",
	} {
		t.Run(name, func(t *testing.T) {
			tree, err := parseGroovy(source)
			require.NoError(t, err)
			require.NotEmpty(t, tree.Statements)
			_, err = readJenkinsfile(t.TempDir(), "Jenkinsfile", source)
			require.NoError(t, err)
		})
	}
}

// The lexer and parser never panic or loop, whatever the input.
func FuzzParseGroovy(f *testing.F) {
	for _, seed := range []string{
		"pipeline { agent any\n stages { stage('t') { steps { sh 'npm test' } } } }",
		"node('x') { stage('a') { sh \"\"\"\n${env.X}\n\"\"\" } }",
		"def f(a) { [a: { -> }, (b): c] }\n@Library('x') _\nx = /re/ + $/re/$",
		"if (a) b else { c }; try { } catch (e) { } finally { }",
		"\"${\"${'}'}\"}\"",
		"switch (x) { case 1: case 'a': break\n default: sh 'b' }\nfor (int i = 0; i < 2; i++) {}\nfor (a in b) {}\nfor (;;) {}\nenv['A'] = env[x]",
		"assert sh(script: 'npm test', returnStatus: true) == 0 : \"m ${x}\"\nthrow new Exception(sh('x'))\ndef r = a ? sh('b') : c ?: d",
		"echo \"${sh(script: \"x ${y ? 'a' : sh('z')}\", returnStdout: true)} ${a; b} ${\"${\"${q}\"}\"}\"\ndef p = /${sh('x')}/",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if tree, err := parseGroovy(source); err == nil {
			require.NotNil(t, tree)
			_, _ = readJenkinsfile(t.TempDir(), "Jenkinsfile", source)
		}
	})
}

// Loop headers, switch labels, jumps and indexes keep what the interpreter
// needs.
func TestGroovyParserControlDetails(t *testing.T) {
	source := `for (def suite in ['unit', 'e2e']) { sh 'a' }
for (int i = 0; i < n; i++) sh 'b'
for (String name : names) { }
for (a, b in pairs) { }
switch (kind) {
  case 'unit':
  case 'integration': sh 'u'
    break
  default:
    sh 'd'
}
env['SUITE'] = 'x'
`
	statements := groovyStatements(t, source)
	forIn := statements[0].(*groovyControl)
	require.Equal(t, []string{"suite"}, forIn.Variables)
	require.IsType(t, &groovyList{}, forIn.Condition)
	require.Nil(t, forIn.Init)
	classic := statements[1].(*groovyControl)
	require.Equal(t, []string{"i", "i"}, classic.Variables)
	require.IsType(t, &groovyAssign{}, classic.Init)
	require.IsType(t, &groovyBinary{}, classic.Condition)
	require.Equal(t, "++", classic.Update.(*groovyBinary).Op)
	require.Len(t, classic.Blocks[0].Statements, 1)
	require.Equal(t, []string{"name"}, statements[2].(*groovyControl).Variables)
	unreadable := statements[3].(*groovyControl)
	require.IsType(t, &groovyUnknown{}, unreadable.Init)
	require.Empty(t, unreadable.Variables)
	switchStatement := statements[4].(*groovyControl)
	require.Len(t, switchStatement.Blocks, 3)
	require.Len(t, switchStatement.Labels, 3)
	require.Equal(t, "'unit'", groovyText(source, switchStatement.Labels[0]))
	require.Nil(t, switchStatement.Labels[2], "default")
	require.Empty(t, switchStatement.Blocks[0].Statements, "an empty case falls through")
	require.Len(t, switchStatement.Blocks[1].Statements, 2)
	require.Equal(t, "break", switchStatement.Blocks[1].Statements[1].(*groovyJump).Kind)
	index := statements[5].(*groovyAssign).Target.(*groovyIndex)
	require.Equal(t, []string{"env"}, index.Receiver.(*groovyName).Path)
	require.IsType(t, &groovyString{}, index.Index)
}
