// Vitest 3+ public specification API. Keep project/pool specifications intact.
export function initialize(context) {
  // Vitest 4 renamed init to standalone; both initialize without running tests.
  return context.standalone ? context.standalone() : context.init()
}

export function discoverSpecifications(context) {
  return context.getRelevantTestSpecifications()
}

export function filePath(specification) {
  return specification.moduleId
}

export function runSpecifications(context, specifications) {
  return context.runTestSpecifications(specifications, true)
}
