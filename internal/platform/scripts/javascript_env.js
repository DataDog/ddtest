const fs = require("fs")
const os = require("os")

const tagsMap = {
  "os.platform": process.platform,
  "os.architecture": process.arch,
  "os.version": os.release(),
  "runtime.name": "node",
  "runtime.version": process.version,
}

const outputFile = process.argv[1]
fs.writeFileSync(outputFile, JSON.stringify(tagsMap))
