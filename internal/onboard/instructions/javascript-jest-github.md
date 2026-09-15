# Enable Datadog Test Optimization for Jest on GitHub Actions

Apply this setup to every GitHub Actions job that runs Jest.

## 1. Add the API key secret

Create a [Datadog API key](https://app.datadoghq.com/organization-settings/api-keys), then add it to the GitHub repository as a secret named `DD_API_KEY`.

If an agent cannot verify the secret, it should continue with the workflow edit. The first CI run will verify the credential.

## 2. Instrument the test job

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

## 3. Try it

Run the local, credential-free setup check:

```shell
ddtest testdrive
```

Then commit and push the workflow change. The GitHub Actions run verifies the real Datadog API key and backend connection.
