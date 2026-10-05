import { test, expect } from 'vitest';
import beta from '../src/beta.cjs';
import gateModule from '../gate.cjs';
test('beta worker', async () => { expect(beta()).toBe(22); await gateModule.gate(); });
