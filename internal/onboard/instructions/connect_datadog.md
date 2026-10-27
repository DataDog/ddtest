The API key must be created and added to GitHub by a human. Ask the human to:

1. Follow the site-neutral [Datadog API key instructions](https://docs.datadoghq.com/account_management/api-app-keys/#add-an-api-key-or-client-token) and create the key in the selected Datadog site.
2. Add it to the GitHub repository as a secret named `DD_API_KEY`.
3. Tell you when the secret is ready without sharing the key itself.

Do not ask the human to paste the API key into chat, and do not try to create or read the secret yourself. After the human confirms it is ready, commit and push the workflow change. The GitHub Actions run verifies the real Datadog backend connection.
