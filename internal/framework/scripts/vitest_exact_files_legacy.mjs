// Vitest 1.6-2 compatibility only: these APIs predate the public specification
// API. Vitest 1 returns workspace tuples; Vitest 2 returns specification objects.
export async function discoverSpecifications(context) {
  return context.filterTestsBySource(await context.globTestFiles())
}

export function filePath(specification) {
  return specification.moduleId ?? specification[1]
}

export function runSpecifications(context, specifications) {
  return context.runFiles(specifications, true)
}
