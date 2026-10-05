const { test } = require('@playwright/test');
const { childCoverage } = require('../gate.cjs');
// No browser is needed: this case covers Node-side helpers, not page JS.
test('worker collects coverage in a subprocess', childCoverage);
