const { Given, Then } = require('@cucumber/cucumber');
const assert = require('assert');
const { add } = require('../../src/calc');
let result;
Given('I add {int} and {int}', (a, b) => { result = add(a, b); });
Then('the result is {int}', (n) => { assert.strictEqual(result, n); });
