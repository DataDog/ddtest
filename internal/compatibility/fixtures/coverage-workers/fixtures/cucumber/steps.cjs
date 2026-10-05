const { Given, setDefaultTimeout } = require('@cucumber/cucumber');
const { childCoverage } = require('../gate.cjs');
setDefaultTimeout(120000);
Given('this worker covers its source file', childCoverage);
