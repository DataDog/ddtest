# Detection fixtures

These are offline configuration snapshots from the OSS repositories used to
validate onboarding. JavaScript manifests retain the original name, scripts,
dependencies and devDependencies; unrelated package metadata is omitted.
Python and Ruby configuration files are copied unchanged. No dependencies are
installed and no scripts, Python files or Gemfiles are executed by these tests.

| Fixture | Source file | Commit |
| --- | --- | --- |
| class-validator | [typestack/class-validator](https://github.com/typestack/class-validator/blob/2e1a5c27dbd65b80e27fe96b49bd6e6641fa3603/package.json) | `2e1a5c27dbd65b80e27fe96b49bd6e6641fa3603` |
| compare-versions | [omichelsen/compare-versions](https://github.com/omichelsen/compare-versions/blob/98e81116ef4197b42dca8c3fde8d1e8166e2a81f/package.json) | `98e81116ef4197b42dca8c3fde8d1e8166e2a81f` |
| destr | [unjs/destr](https://github.com/unjs/destr/blob/541b6f9aeada9fc30de9c5a7e086dbfc1c6fcdc7/package.json) | `541b6f9aeada9fc30de9c5a7e086dbfc1c6fcdc7` |
| ttvc | [dropbox/ttvc](https://github.com/dropbox/ttvc/blob/239481cdecb841f63f0b3f3e13be52275333b18d/package.json) | `239481cdecb841f63f0b3f3e13be52275333b18d` |
| cloudevents | [cloudevents/sdk-javascript](https://github.com/cloudevents/sdk-javascript/blob/bf5d53f2862248d72d9869cf5173f51b32583faf/package.json) | `bf5d53f2862248d72d9869cf5173f51b32583faf` |
| cypress-example-kitchensink | [cypress-io/cypress-example-kitchensink](https://github.com/cypress-io/cypress-example-kitchensink/blob/ddaaa92080b68d71d7a1797b4ed20ada18ed2a2a/package.json) | `ddaaa92080b68d71d7a1797b4ed20ada18ed2a2a` |
| itsdangerous | [pallets/itsdangerous](https://github.com/pallets/itsdangerous/blob/672971d66a2ef9f85151e53283113f33d642dabd/pyproject.toml) | `672971d66a2ef9f85151e53283113f33d642dabd` |
| concurrent-ruby | [ruby-concurrency/concurrent-ruby](https://github.com/ruby-concurrency/concurrent-ruby/blob/e674fb2688206bb7cb66dee108e1d08184413afb/Gemfile) | `e674fb2688206bb7cb66dee108e1d08184413afb` |
| i18n | [ruby-i18n/i18n](https://github.com/ruby-i18n/i18n/blob/547917dd8d41fab781a81880f22687fc4eac5d85/Gemfile) | `547917dd8d41fab781a81880f22687fc4eac5d85` |

The snapshots deliberately include coverage wrappers (compare-versions), a
composite default test script (destr), mixed Jest/Playwright suites behind
npm-run-all (ttvc), mixed Mocha/Cucumber with lifecycle hooks (CloudEvents),
a background web server (Cypress), dependency groups (itsdangerous), and a
Gemfile that reads local source files and environment variables (concurrent-ruby).

`edge-cases.json` contains synthetic adversarial regressions, not OSS snapshots.
It covers explicit framework selection, shell composition, misleading names,
script-only detection, malformed manifests, standalone pytest markers and
polyglot roots. All are materialized into temporary directories with spaces.

Additional Python snapshots (copied unchanged):

| Fixture | Source files | Commit |
| --- | --- | --- |
| requests-legacy | [setup.cfg](https://github.com/psf/requests/blob/147c8511ddbfa5e8f71bbf5c18ede0c4ceb3bba4/setup.cfg), [tox.ini](https://github.com/psf/requests/blob/147c8511ddbfa5e8f71bbf5c18ede0c4ceb3bba4/tox.ini) | `147c8511ddbfa5e8f71bbf5c18ede0c4ceb3bba4` |
| django | [setup.cfg](https://github.com/django/django/blob/879e5d587b84e6fc961829611999431778eb9f6a/setup.cfg), [tox.ini](https://github.com/django/django/blob/879e5d587b84e6fc961829611999431778eb9f6a/tox.ini) | `879e5d587b84e6fc961829611999431778eb9f6a` |
| flask-legacy | [setup.cfg](https://github.com/pallets/flask/blob/47af817c8fe01045c641b97f8fb784c7ad864eee/setup.cfg) | `47af817c8fe01045c641b97f8fb784c7ad864eee` |
| requests | [pyproject.toml](https://github.com/psf/requests/blob/611c6162cbc4ac2020a2f91c7cfa4f3abf9bbb60/pyproject.toml) | `611c6162cbc4ac2020a2f91c7cfa4f3abf9bbb60` |
| virtualenv | [pyproject.toml](https://github.com/pypa/virtualenv/blob/4f09d426aba3e07981d5003fe6daee77c1160ff8/pyproject.toml) | `4f09d426aba3e07981d5003fe6daee77c1160ff8` |

Django uses its own test runner: its packaging and tox configuration prove
Python, but must not imply pytest. Requests’ legacy setup.cfg configures flake8;
pytest evidence comes from tox commands. Flask provides `[tool:pytest]`, while
modern Requests and virtualenv exercise dependency groups and both pytest TOML
configuration forms. Tests also inspect each snapshot in isolation.
