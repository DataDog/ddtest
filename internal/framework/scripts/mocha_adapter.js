"use strict"

const path = require("path")

const outputMarker = "__DDTEST_MOCHA_FILES__"
const request = JSON.parse(process.argv[1])

const packagePath = require.resolve("mocha/package.json", { paths: [process.cwd()] })
const mochaRoot = path.dirname(packagePath)
const mochaVersion = require(packagePath).version
const majorVersion = Number.parseInt(mochaVersion.split(".")[0], 10)

if (!Number.isInteger(majorVersion) || majorVersion < 8) {
  throw new Error(`ddtest requires Mocha 8 or newer; found ${mochaVersion}`)
}

const optionsPath = path.join(mochaRoot, "lib/cli/options.js")
const optionsModule = require(optionsPath)
const { loadOptions } = optionsModule
const options = loadOptions(request.cliArgs || [])

if (request.mode === "discover") {
  const collectFiles = require(path.join(mochaRoot, "lib/cli/collect-files.js"))
  const collection = collectFiles({
    ignore: options.ignore || [],
    extension: options.extension || [],
    file: options.file || [],
    recursive: Boolean(options.recursive),
    sort: Boolean(options.sort),
    spec: options._ && options._.length ? options._ : ["test"],
  })

  // --file entries are global setup files. Mocha loads them for every run, so
  // they must not be partitioned as independently runnable test files.
  // Mocha 8 and 9 return the file array directly. Mocha 10 and newer
  // return an object that also describes unmatched --file entries.
  const collectedFiles = Array.isArray(collection) ? collection : collection.files
  const setupFiles = new Set((options.file || []).map(file => path.resolve(file)))
  const testFiles = collectedFiles.filter(file => !setupFiles.has(path.resolve(file)))
  process.stdout.write(`${outputMarker}${JSON.stringify(testFiles)}\n`)
} else if (request.mode === "run") {
  const files = request.files || []

  // loadOptions merges configured `spec` values with positional arguments.
  // Replace that merged list so a worker runs only the files assigned by
  // ddtest while retaining every other effective Mocha option.
  options._ = files
  const cliPath = path.join(mochaRoot, "lib/cli/cli.js")
  if (majorVersion === 8) {
    // Mocha 8's CLI does not yet accept pre-parsed options. Replace the
    // loader before requiring the CLI so it uses the effective config with
    // ddtest's selected files instead of merging configured spec again.
    optionsModule.loadOptions = () => options
    const { main } = require(cliPath)
    main(files)
  } else {
    const { main } = require(cliPath)
    main(files, options)
  }
} else {
  throw new Error(`unknown ddtest Mocha adapter mode: ${request.mode}`)
}
