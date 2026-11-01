// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

// Use the installed Vitest parser, including its option arities, rather than a
// second CLI grammar. parseCLI mutates argv, so every parse gets a fresh array.
export function validationArguments(args, parseCLI, probe = '') {
  const parse = values => parseCLI(['vitest', ...values])
  args = withoutRunOptions(args)
  const parsed = parse(args)
  const marker = '\0ddtest positional filter\0'
  // positional reports whether values[i] is a positional word rather than an
  // option's value. A marker in its place tells them apart, except for a
  // literal true or false: a boolean option takes it as its value
  // (--cache false), but not a marker, which would read as a filter.
  const positional = (values, i) => {
    const value = values[i]
    if (value === 'true' || value === 'false') {
      const count = prefix => parse(prefix).filter.filter(word => word === value).length
      return count(values.slice(0, i + 1)) > count(values.slice(0, i))
    }
    const candidate = [...values]
    candidate[i] = marker
    return parse(candidate).filter.includes(marker)
  }
  const separator = args.indexOf('--')
  const before = separator < 0 ? args : args.slice(0, separator)
  let commandIndex = -1
  let firstPositional = -1
  for (let i = 0; i < before.length; i++) {
    if (before[i].startsWith('-')) continue
    const prefix = before.slice(0, i + 1)
    if (!positional(prefix, i)) continue
    firstPositional = i
    if (!parse(prefix).filter.includes(before[i])) commandIndex = i
    break
  }
  // Older Vitest treats newer command names (notably list in v1) as file
  // filters. Reserve these modes consistently; ./list is an explicit path.
  const first = before[firstPositional]
  const reserved = ['bench', 'watch', 'dev', 'list', 'related'].includes(first)
  const mode = commandIndex < 0 ? reserved ? first : 'run' : before[commandIndex]
  if (mode !== 'run') throw new Error(`Vitest ${mode} mode is not supported by testdrive; select the ordinary test command`)
  if (parsed.options.watch) throw new Error('Vitest watch mode is not supported by testdrive')
  const result = []
  for (let i = 0; i < before.length; i++) {
    const argument = before[i]
    const key = argument.split('=', 1)[0]
    // Validation always adds --no-cache; keep a single cache option.
    const owned = key === '--cache' || key === '--no-cache' || key === '--reporter' || key === '--outputFile' || key.startsWith('--outputFile.') ||
      key === '--coverage.reportsDirectory' || key === '--coverage.thresholds.autoUpdate' ||
      probe && (key === '--shard' || key === '--retry' || key === '--project' || key === '--testNamePattern' || key === '-t' ||
        key.startsWith('--coverage.thresholds') || key === '--no-coverage.thresholds')
    if (owned) {
      if (!argument.includes('=') && i + 1 < before.length && !before[i + 1].startsWith('-') && !positional(before, i + 1)) i++
      continue
    }
    if (probe && i !== commandIndex && !argument.startsWith('-') && positional(before, i)) continue
    result.push(argument)
  }
  if (probe) result.push(probe)
  else if (separator >= 0) result.push(...args.slice(separator))
  return { arguments: result, reporters: parsed.options.reporter, runCommand: commandIndex >= 0 }
}

// Vitest rejects a repeated --run, even in different forms such as --run and
// --run=true. Validation always runs once and adds --run unless the run command
// is explicit, so drop the command's own forms before the first parse.
function withoutRunOptions(args) {
  const result = []
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--') return result.concat(args.slice(i))
    const key = args[i].split('=', 1)[0]
    if (key !== '--run' && key !== '--no-run') {
      result.push(args[i])
      continue
    }
    // The boolean parser takes a following literal true or false as its value.
    if (args[i] === '--run' && (args[i + 1] === 'true' || args[i + 1] === 'false')) i++
  }
  return result
}

// Validation must never let Vitest rewrite a customer's coverage gate. false
// disables all global/per-glob probe thresholds while retaining collection.
export function validationCoverage(directory, probe) {
  return { reportsDirectory: directory, thresholds: probe ? false : { autoUpdate: false } }
}
