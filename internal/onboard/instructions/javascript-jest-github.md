# Enable Datadog Test Optimization for Jest on GitHub Actions

Apply this setup to every GitHub Actions job that runs Jest.

## 1. Instrument the test job

Add this step after checkout and dependency installation, immediately before the first test step:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v3
  with:
    languages: js
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
```

If the organization uses a Datadog site other than US1, replace `datadoghq.com` with that site.

GitHub Actions does not let an action change `NODE_OPTIONS`. Add or merge this environment variable on the existing Jest test step:

```yaml
env:
  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}
```

Keep the existing test command and unrelated workflow content unchanged. Add the Datadog action once per test job, not once per test step.

## 2. Try it locally

Run the local, credential-free setup check:

```shell
ddtest testdrive
```

## 3. Ask a human to connect Datadog

The API key must be created and added to GitHub by a human. Ask the human to:

1. Create a [Datadog API key](https://app.datadoghq.com/organization-settings/api-keys).
2. Add it to the GitHub repository as a secret named `DD_API_KEY`.
3. Tell you when the secret is ready without sharing the key itself.

Do not ask the human to paste the API key into chat, and do not try to create or read the secret yourself. After the human confirms it is ready, commit and push the workflow change. The GitHub Actions run verifies the real Datadog backend connection.
