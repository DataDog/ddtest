import { test, expect } from 'vitest';
import alpha from '../src/alpha.cjs';
import gateModule from '../gate.cjs';
test('alpha worker', async () => { expect(alpha()).toBe(11); await gateModule.gate(); });
