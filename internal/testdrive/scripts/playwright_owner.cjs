const net = require('node:net')

// The process signals itself while it is alive. No recorded PID is ever used
// after exit, including when ddtest is killed before ordinary cleanup runs.
module.exports = function ownProcess(stop, keepAlive = false, ready = () => {}) {
  const socket = net.connect(process.env.DDTEST_PLAYWRIGHT_OWNER)
  let stopping = false
  const terminate = () => {
    if (stopping) return
    stopping = true
    stop('SIGTERM')
    setTimeout(() => stop('SIGKILL'), 1000).ref()
  }
  socket.on('connect', ready)
  socket.on('data', terminate)
  socket.on('end', terminate)
  socket.on('close', terminate)
  socket.on('error', terminate)
  if (!keepAlive) socket.unref()
  return socket
}
