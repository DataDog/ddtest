"use strict"

const fs = require("fs")
const path = require("path")

const outputMarker = "__DDTEST_MOCHA_FILES__"
const requestJSON = process.env.DDTEST_MOCHA_REQUEST
const entrypoint = process.argv[1] || ""

// NODE_OPTIONS is inherited by command wrappers and package managers. Wait
// until the process that actually runs Mocha so those tools can establish the
// intended working directory, module path, and environment first.
if (requestJSON && ["mocha", "mocha.js", "_mocha"].includes(path.basename(entrypoint))) {
  delete process.env.DDTEST_MOCHA_REQUEST
  runAdapter(JSON.parse(requestJSON), entrypoint)
}

function runAdapter(request, mochaEntrypoint) {
  let resolvedEntrypoint = path.resolve(mochaEntrypoint)
  try {
    resolvedEntrypoint = fs.realpathSync(resolvedEntrypoint)
  } catch (_) {
    // Keep the unresolved command path as a module-resolution hint.
  }
  const packagePath = require.resolve("mocha/package.json", {
    paths: [path.dirname(resolvedEntrypoint), process.cwd()],
  })
  const mochaRoot = path.dirname(packagePath)
  const mochaVersion = require(packagePath).version
  const majorVersion = Number.parseInt(mochaVersion.split(".")[0], 10)

  if (!Number.isInteger(majorVersion) || majorVersion < 8) {
    throw new Error(`ddtest requires Mocha 8 or newer; found ${mochaVersion}`)
  }

  const optionsPath = path.join(mochaRoot, "lib/cli/options.js")
  const optionsModule = require(optionsPath)
  const options = optionsModule.loadOptions(request.cliArgs || [])

  if (request.mode === "discover") {
    const collectFiles = require(path.join(mochaRoot, "lib/cli/collect-files.js"))
    const collection = collectFiles({
      ignore: options.ignore || [],
      extension: options.extension || [],
      file: options.file || [],
      recursive: Boolean(options.recursive),
      sort: Boolean(options.sort),
      spec: request.spec && request.spec.length
        ? request.spec
        : (options._ && options._.length ? options._ : ["test"]),
    })

    // --file entries are global setup files. Mocha loads them for every run, so
    // they must not be partitioned as independently runnable test files.
    // Mocha 8 and 9 return the file array directly. Mocha 10 and newer
    // return an object that also describes unmatched --file entries.
    const collectedFiles = Array.isArray(collection) ? collection : collection.files
    const setupFiles = new Set((options.file || []).map(file => path.resolve(file)))
    const testFiles = collectedFiles.filter(file => !setupFiles.has(path.resolve(file)))
    process.stdout.write(`${outputMarker}${JSON.stringify(testFiles)}\n`)
    process.exit(0)
  } else if (request.mode === "run") {
    const files = request.files || []

    // The selected command will load Mocha's CLI after this preload. Replace
    // its option loader so configured specs cannot be merged back in, while
    // retaining all other effective Mocha options on every supported version.
    options._ = files
    optionsModule.loadOptions = () => options
  } else {
    throw new Error(`unknown ddtest Mocha adapter mode: ${request.mode}`)
  }
}
