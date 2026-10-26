# Build --target offline for fake CLI tests, or --target agents for real trials.
FROM node:24.19.0-bookworm@sha256:4196d66a565c6f195728d9952f161f4adfe2ad753052a08b7ec7f1c5a6bda42b AS runtime
RUN npm install --global pnpm@12.14.0 && npm cache clean --force

FROM runtime AS offline
COPY fixtures/fake_agent.py /usr/local/lib/ddtest-fake-agent.py
RUN chmod +x /usr/local/lib/ddtest-fake-agent.py && ln -s /usr/local/lib/ddtest-fake-agent.py /usr/local/bin/codex && ln -s /usr/local/lib/ddtest-fake-agent.py /usr/local/bin/claude

FROM runtime AS agents
RUN npm install --global @openai/codex@0.163.0 @anthropic-ai/claude-code@2.1.220 && npm cache clean --force

# Java trials use image-owned toolchains; the container runner keeps HOME and
# Maven's default dependency/wrapper caches inside its owned trial directory.
FROM maven:3.9-eclipse-temurin-17@sha256:1a352420f7aba21f5ad08df31bab55f74c013fb491f1ae8ab1dd7ff9ed698584 AS maven-toolchain
FROM eclipse-temurin:8-jdk-jammy@sha256:185d4ef919b98cd195690c33f3fd93bd49c66c70f0b662e54566c0dbaebea6ce AS jdk8
FROM eclipse-temurin:17-jdk-jammy@sha256:73ed442231ec66cfe48cae0b263ce619a75ab96d162ab325f38fc0a269ee3aab AS jdk17

FROM agents AS maven-runtime
COPY --from=maven-toolchain /usr/share/maven /usr/share/maven
RUN ln -s /usr/share/maven/bin/mvn /usr/local/bin/mvn

FROM maven-runtime AS java8
COPY --from=jdk8 /opt/java/openjdk /opt/java/openjdk
RUN for tool in java javac javadoc jar; do ln -s /opt/java/openjdk/bin/$tool /usr/local/bin/$tool; done

FROM maven-runtime AS java17
COPY --from=jdk17 /opt/java/openjdk /opt/java/openjdk
RUN for tool in java javac javadoc jar; do ln -s /opt/java/openjdk/bin/$tool /usr/local/bin/$tool; done

# Optional browser-ready image. Flags apply only inside this disposable container;
# customer repository configuration and host browser settings remain untouched.
FROM agents AS browsers
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends xvfb xauth chromium chromium-driver && rm -rf /var/lib/apt/lists/*
RUN mv /usr/bin/chromium /usr/bin/chromium.original && printf '#!/bin/sh\nexec /usr/bin/chromium.original --no-sandbox --disable-dev-shm-usage "$@"\n' > /usr/bin/chromium && chmod +x /usr/bin/chromium
RUN printf '#!/bin/sh\nexec /usr/bin/chromium --no-sandbox --disable-dev-shm-usage "$@"\n' > /usr/local/bin/google-chrome && chmod +x /usr/local/bin/google-chrome
# WebdriverIO otherwise downloads Chrome for Testing's driver, which has no
# linux/arm64 build; the distribution driver matches this Chromium and arch.
ENV CHROMEDRIVER_PATH=/usr/bin/chromedriver
