import assert from 'node:assert';
import { Given, Then } from '@cucumber/cucumber';
import { add } from '../../src/calc.js';

let result;
Given('I add {int} and {int}', (a, b) => { result = add(a, b); });
Then('the result is {int}', (n) => { assert.strictEqual(result, n); });
