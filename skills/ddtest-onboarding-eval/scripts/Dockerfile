# Build --target offline for fake CLI tests, or --target agents for real trials.
FROM node:24.19.0-bookworm@sha256:4196d66a565c6f195728d9952f161f4adfe2ad753052a08b7ec7f1c5a6bda42b AS runtime
RUN npm install --global pnpm@12.14.0 && npm cache clean --force

FROM runtime AS offline
COPY fixtures/fake_agent.py /usr/local/lib/ddtest-fake-agent.py
RUN chmod +x /usr/local/lib/ddtest-fake-agent.py && ln -s /usr/local/lib/ddtest-fake-agent.py /usr/local/bin/codex && ln -s /usr/local/lib/ddtest-fake-agent.py /usr/local/bin/claude

FROM runtime AS agents
RUN npm install --global @openai/codex@0.163.0 @anthropic-ai/claude-code@2.1.220 && npm cache clean --force
