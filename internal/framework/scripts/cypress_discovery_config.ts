__DDTEST_CONFIG_IMPORT__

import fs from "node:fs"
import path from "node:path"
import { createRequire } from "node:module"

const discoveryMarker = "__DDTEST_CYPRESS_CONFIG__"
const discoveryConfigPath = __DDTEST_DISCOVERY_CONFIG_PATH__
const config: any = originalConfig || {}
// Resolve minimatch from Cypress's config-process entrypoint so discovery uses
// the matcher and export shape bundled with the selected Cypress version.
const cypressRequire = createRequire(process.argv[1])
const minimatchModule = cypressRequire("minimatch")
const minimatch = typeof minimatchModule === "function"
  ? minimatchModule
  : minimatchModule.minimatch
if (typeof minimatch !== "function") {
  throw new Error("Cypress did not provide a compatible minimatch implementation")
}
const minimatchOptions = { dot: true, matchBase: true }

function patterns(value: unknown): string[] {
  if (typeof value === "string") {
    return [value]
  }
  if (Array.isArray(value)) {
    return value.filter((pattern): pattern is string => typeof pattern === "string")
  }
  return []
}

function matchesAny(file: string, filePatterns: string[]): boolean {
  return filePatterns.some((pattern) => minimatch(file, pattern, minimatchOptions))
}

function discoverSpecFiles(
  effectiveConfig: Record<string, unknown>,
  testingType: "e2e" | "component",
): string[] {
  const projectRoot = String(effectiveConfig.projectRoot || process.cwd())
  const includePatterns = patterns(effectiveConfig.specPattern)
  const excludePatterns = [
    ...patterns(effectiveConfig.excludeSpecPattern),
    ...patterns(effectiveConfig.additionalIgnorePattern),
  ]
  if (testingType === "component") {
    const e2ePatterns = patterns(config.e2e?.specPattern)
    excludePatterns.push(...(e2ePatterns.length > 0
      ? e2ePatterns
      : ["cypress/e2e/**/*.cy.{js,jsx,ts,tsx}"]))
  }

  const files: string[] = []
  function walk(directory: string) {
    let entries: fs.Dirent[]
    try {
      entries = fs.readdirSync(directory, { withFileTypes: true })
    } catch {
      return
    }
    for (const entry of entries) {
      const absolute = path.join(directory, entry.name)
      if (entry.isDirectory()) {
        if (entry.name !== "node_modules" && entry.name !== ".git") {
          walk(absolute)
        }
        continue
      }
      if (path.resolve(absolute) === path.resolve(discoveryConfigPath)) {
        continue
      }
      const relative = path.relative(projectRoot, absolute).split(path.sep).join("/")
      if (matchesAny(relative, includePatterns) && !matchesAny(relative, excludePatterns)) {
        files.push(relative)
      }
    }
  }
  walk(projectRoot)
  return files.sort()
}

function testingTypeConfig(testingType: "e2e" | "component") {
  const originalTestingTypeConfig = config[testingType] || {}
  const originalSetupNodeEvents = originalTestingTypeConfig.setupNodeEvents

  return {
    ...originalTestingTypeConfig,
    async setupNodeEvents(on: unknown, resolvedConfig: Record<string, unknown>) {
      let returnedConfig: unknown
      if (typeof originalSetupNodeEvents === "function") {
        returnedConfig = await originalSetupNodeEvents(on, resolvedConfig)
      }

      const effectiveConfig = returnedConfig && typeof returnedConfig === "object"
        ? { ...resolvedConfig, ...returnedConfig }
        : resolvedConfig
      process.stdout.write(`${discoveryMarker}${JSON.stringify({
        projectRoot: effectiveConfig.projectRoot,
        testingType,
        specFiles: discoverSpecFiles(effectiveConfig, testingType),
      })}\n`)

      return returnedConfig
    },
  }
}

export default {
  ...config,
  e2e: testingTypeConfig("e2e"),
  component: testingTypeConfig("component"),
}
