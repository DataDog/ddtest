const net = require('node:net')
const fs = require('node:fs')

function recordFailure(reason, code = 'ownership_connection_failed') {
  fs.appendFileSync(process.env.DDTEST_PROCESS_ERRORS, JSON.stringify({ code, reason }) + '\n')
}

// A failed initial connection must not kill customer code. It invalidates the
// comparison instead. Once connected, a lost lease means the owner disconnected.
module.exports = function ownProcess(stop, keepAlive = false, ready = () => {}) {
  const socket = net.connect(process.env.DDTEST_PROCESS_OWNER)
  let connected = false
  let stopping = false
  let pending = ''
  const terminate = () => {
    if (stopping) return
    stopping = true
    stop('SIGTERM')
    setTimeout(() => stop('SIGKILL'), 1000).ref()
  }
  socket.on('connect', () => { connected = true; ready() })
  socket.setEncoding('utf8')
  socket.on('data', data => {
    pending += data
    if (pending.split('\n').includes('stop')) terminate()
  })
  socket.on('end', () => { if (connected) terminate() })
  socket.on('close', () => { if (connected) terminate() })
  socket.on('error', error => recordFailure('owner connection: ' + error.code))
  if (!keepAlive) socket.unref()
  return socket
}
module.exports.recordFailure = recordFailure
