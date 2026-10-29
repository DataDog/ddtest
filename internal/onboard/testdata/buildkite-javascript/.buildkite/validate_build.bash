#!/usr/bin/env bash
# From heap/eslint-plugin-heap (MIT), trimmed to its commands: builds and
# checks that dist/ is committed.
npm install
npm run build
git diff --exit-code dist/
