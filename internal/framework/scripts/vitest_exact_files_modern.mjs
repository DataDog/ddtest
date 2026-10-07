// Vitest 3+ public specification API. Keep project/pool specifications intact.
export function discoverSpecifications(context) {
  return context.getRelevantTestSpecifications()
}

export function filePath(specification) {
  return specification.moduleId
}

export function runSpecifications(context, specifications) {
  return context.runTestSpecifications(specifications, true)
}
