__DDTEST_CONFIG_IMPORT__

const discoveryMarker = "__DDTEST_CYPRESS_CONFIG__"
const config: any = originalConfig || {}

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
      const otherTestingType = testingType === "component" ? "e2e" : "component"
      const otherTestingTypeConfig = config[otherTestingType] || {}

      process.stdout.write(`${discoveryMarker}${JSON.stringify({
        projectRoot: effectiveConfig.projectRoot,
        testingType,
        specPattern: effectiveConfig.specPattern,
        excludeSpecPattern: effectiveConfig.excludeSpecPattern,
        otherTestingTypeSpecPattern: otherTestingTypeConfig.specPattern,
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
