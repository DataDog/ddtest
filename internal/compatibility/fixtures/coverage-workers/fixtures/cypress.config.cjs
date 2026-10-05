const { defineConfig } = require('cypress');
const coverage = require('@cypress/code-coverage/task');
const { gate } = require('./gate.cjs');
module.exports = defineConfig({
  video: false, screenshotOnRunFailure: false, taskTimeout: 120000,
  e2e: { specPattern: 'cypress/*.cy.cjs', supportFile: 'cypress/support.cjs',
    setupNodeEvents(on, config) {
      coverage(on, config);
      on('task', { gate: async () => { await gate(); return null; } });
      return config;
    },
  },
});
