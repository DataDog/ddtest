import { writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'

function ancestorTitles (task) {
  const titles = []
  for (let suite = task.suite; suite; suite = suite.suite) titles.unshift(suite.name)
  return titles
}

// Reporters can mutate task names (for example, to redact credentials). Keep
// Vitest's collected identity separately; never infer it from tracer events.
export default class IdentityReporter {
  names = new Map()

  onInit (ctx) {
    this.ctx = ctx
    this.legacyEmptyTitle = createRequire(resolve('package.json'))('vitest/package.json').version.startsWith('1.')
    const collect = ctx.state.collectFiles
    const names = this.names
    function visit (task) {
      if (task.type === 'test') {
        names.set(task.id, { name: task.name, ancestors: ancestorTitles(task) })
      } else {
        for (const child of task.tasks ?? []) visit(child)
      }
    }
    ctx.state.collectFiles = function (...args) {
      const result = collect.apply(this, args)
      // Vitest 1/2 take files; later versions take project, files.
      for (const files of args.filter(Array.isArray)) files.forEach(file => visit(file))
      return result
    }
  }

  save () {
    const tests = []
    const status = { fail: 'failed', pass: 'passed', skip: 'skipped', todo: 'todo', run: 'pending', only: 'pending', queued: 'pending' }
    const visit = (task, file) => {
      if (task.type === 'test') {
        const original = this.names.get(task.id)
        if (!original) throw new Error('DDTest did not observe the collected Vitest identity')
        const telemetryName = [...original.ancestors.filter(Boolean), original.name].join(' ')
        const ancestors = ancestorTitles(task)
        tests.push({
          file,
          reportedName: (this.legacyEmptyTitle || task.name ? [...ancestors, task.name] : ancestors).join(' '),
          name: (this.legacyEmptyTitle || original.name ? [...original.ancestors, original.name] : original.ancestors).join(' '),
          // dd-trace's encode/tags-processors truncateSpanTestOpt uses JS
          // string units, including a possible split surrogate, then adds '...'.
          telemetryName: telemetryName.length > 5000 ? telemetryName.slice(0, 5000) + '...' : telemetryName,
          status: status[task.result?.state || task.mode] || 'skipped',
        })
      } else {
        for (const child of task.tasks ?? []) visit(child, file)
      }
    }
    for (const file of this.ctx.state.getFiles()) visit(file, file.filepath)
    const output = this.ctx.config.outputFile
    const path = typeof output === 'string' ? output : output?.json
    if (!path) throw new Error('DDTest Vitest identity collection requires a JSON output path')
    writeFileSync(path + '.identities.json', JSON.stringify(tests))
  }

  onFinished () { this.save() }
  onTestRunEnd () { this.save() }
}
