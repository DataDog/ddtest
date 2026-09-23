# Jest suite-skipping integration projects

`jest_projects.json` records the file lists returned by native Jest at these
immutable revisions. Only paths and provenance are stored; upstream test source
is fetched separately and is never rewritten by the integration test.

| Project | Revision | Layout | Suites |
| --- | --- | --- | ---: |
| [Day.js v1.11.13](https://github.com/iamkun/dayjs/tree/93c8fd0f807b8a8252f4cd65083bb1d6a49b90e7) | `93c8fd0` | `test/**/*.test.js`, Babel, package.json Jest configuration | 91 |
| [Immer v10.1.1](https://github.com/immerjs/immer/tree/e2d222bd4fb26abded04075c936290715e9ee335) | `e2d222b` | `__tests__`, JavaScript/TypeScript, ts-jest preset, snapshots | 20 |
| [Redux v4.2.1](https://github.com/reduxjs/redux/tree/f4b3ab9ac5370c5520d08383c5fc88e1e1c64587) | `f4b3ab9` | `test/**/*.spec.{js,ts}`, Babel, nested suites, build artifacts | 8 |

`TestJestProjectSuiteSkipping` runs offline in `make test`, using the captured
layouts with the real Jest filesystem adapter and planner. It checks no skips,
alternating skips, all skips, and the unskippable marker override.

`TestJestOpenSourceProjectIntegration` compares config-aware discovery with native
Jest without customer glob overrides, runs all original suites with no skips, executes only assigned suites with
alternating skips, and verifies that all-skipped plans never start Jest. Backend skip responses are simulated for deterministic cases. It
checks Jest's JSON execution report against the planner's file assignments.
Static analysis is required for Day.js and Redux. Immer's dynamic `ts-jest`
preset currently uses an explicit native fallback; the test checks that this
fallback is reported and preserves the native file list. `.github/workflows/ci.yml` runs each
pinned project, using its original lockfile and configuration.

To reproduce locally, clone one of the pinned revisions into a temporary
checkout. Use Node 20, install with `npm ci --ignore-scripts --legacy-peer-deps`
for Day.js/Redux, or `npx --yes yarn@1.22.22 install --frozen-lockfile
--ignore-scripts --non-interactive` for Immer. Run `npm run build` for Redux.
From the DDTest repository:

```sh
DDTEST_JEST_PROJECT=redux \
DDTEST_JEST_PROJECT_ROOT=/absolute/path/to/redux \
  go test -v ./internal/planner -run '^TestJestOpenSourceProjectIntegration$' -count=1
```

The integration test writes DDTest plan artifacts into the temporary upstream
checkout. Remove the checkout afterward. To refresh a fixture, pin its new
revision in both JSON and CI, regenerate the sorted relative paths from
`node_modules/.bin/jest --listTests --json --runInBand --coverage=false`, and run
both tests. Do not silently update a file list to hide a discovery mismatch.
