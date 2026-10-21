// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

// Use the installed Vitest parser, including its option arities, rather than a
// second CLI grammar. parseCLI mutates argv, so every parse gets a fresh array.
export function validationArguments(args, parseCLI, probe = '') {
  const parse = values => parseCLI(['vitest', ...values])
  const parsed = parse(args)
  const marker = '\0ddtest positional filter\0'
  const separator = args.indexOf('--')
  const before = separator < 0 ? args : args.slice(0, separator)
  let commandIndex = -1
  for (let i = 0; i < before.length; i++) {
    if (before[i].startsWith('-')) continue
    const prefix = before.slice(0, i + 1)
    const candidate = [...prefix]
    candidate[i] = marker
    if (!parse(candidate).filter.includes(marker)) continue
    if (!parse(prefix).filter.includes(before[i])) commandIndex = i
    break
  }
  const mode = commandIndex < 0 ? 'run' : before[commandIndex]
  if (mode !== 'run') throw new Error(`Vitest ${mode} mode is not supported by testdrive; select the ordinary test command`)
  if (parsed.options.watch) throw new Error('Vitest watch mode is not supported by testdrive')
  const result = []
  for (let i = 0; i < before.length; i++) {
    const argument = before[i]
    const key = argument.split('=', 1)[0]
    const owned = key === '--reporter' || key === '--outputFile' || key.startsWith('--outputFile.') ||
      key === '--coverage.reportsDirectory' || key === '--coverage.thresholds.autoUpdate' ||
      probe && (key === '--shard' || key === '--retry' || key === '--project' || key === '--testNamePattern' || key === '-t' ||
        key.startsWith('--coverage.thresholds') || key === '--no-coverage.thresholds')
    if (owned) {
      if (!argument.includes('=') && i + 1 < before.length && !before[i + 1].startsWith('-')) {
        const candidate = [...before]
        candidate[i + 1] = marker
        if (!parse(candidate).filter.includes(marker)) i++
      }
      continue
    }
    if (probe && i !== commandIndex && !argument.startsWith('-')) {
      const candidate = [...before]
      candidate[i] = marker
      if (parse(candidate).filter.includes(marker)) continue
    }
    result.push(argument)
  }
  if (probe) result.push(probe)
  else if (separator >= 0) result.push(...args.slice(separator))
  return { arguments: result, reporters: parsed.options.reporter }
}

// Validation must never let Vitest rewrite a customer's coverage gate. false
// disables all global/per-glob probe thresholds while retaining collection.
export function validationCoverage(directory, probe) {
  return { reportsDirectory: directory, thresholds: probe ? false : { autoUpdate: false } }
}
