import { defineConfig } from 'vitest/config';
export default defineConfig({ test: {
  include: ['vitest/*.test.mjs'], testTimeout: 120000,
  maxWorkers: 1, minWorkers: 1,
  coverage: { enabled: true, provider: 'v8', include: ['src/*.cjs'],
    reporter: ['json'], reportsDirectory: process.env.REPRO_REPORT },
} });
