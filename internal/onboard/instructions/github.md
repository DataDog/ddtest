# Enable Datadog Test Optimization for __FRAMEWORK__ on GitHub Actions

Apply this setup to every GitHub Actions job that runs __FRAMEWORK__.

[[if eq . "javascript"]]__AUTOMATIC_SETUP__

[[end -]]
## 1. Instrument the test job

Add this step after checkout and dependency installation, immediately before the first test step:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v3
  with:
    languages: __LANGUAGE__
    __TRACER_SETTING__
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
```

If the organization uses a Datadog site other than US1, replace `datadoghq.com` with that site.

__BOOTSTRAP__

Keep the existing test command and unrelated workflow content unchanged. Add the Datadog action once per test job, not once per test step.

## 2. Try it locally

Run the local, credential-free setup check:

```shell
[[if eq . "javascript"]]ddtest testdrive --agent --yes[[else]]ddtest testdrive[[end]]
```

After it finishes, post every `Open report:` link printed by `ddtest testdrive` to the user so they can open the local Test Optimization report. Do not only summarize the result.

## 3. Ask a human to connect Datadog

__CONNECT_DATADOG__
