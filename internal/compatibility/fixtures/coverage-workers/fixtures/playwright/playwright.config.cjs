module.exports = { testDir: '.', testMatch: 'worker.spec.cjs', timeout: 120000, workers: 1,
  reporter: 'line', outputDir: process.env.REPRO_TEST_OUTPUT };
