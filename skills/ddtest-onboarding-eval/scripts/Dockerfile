# Build --target offline for fake CLI tests, or --target agents for real trials;
# --target java adds a JDK and Maven for --framework junit4/junit5 trials.
# JDK_IMAGE selects another Temurin JDK for a project that needs one.
ARG JDK_IMAGE=eclipse-temurin:21-jdk@sha256:5d90b26c0784ccf55af55f97cbcf7f3969c66a06299c93bdc72649219942a48f
FROM ${JDK_IMAGE} AS jdk

FROM node:24.19.0-bookworm@sha256:4196d66a565c6f195728d9952f161f4adfe2ad753052a08b7ec7f1c5a6bda42b AS runtime
RUN npm install --global pnpm@12.14.0 && npm cache clean --force

FROM runtime AS offline
COPY fixtures/fake_agent.py /usr/local/lib/ddtest-fake-agent.py
RUN chmod +x /usr/local/lib/ddtest-fake-agent.py && ln -s /usr/local/lib/ddtest-fake-agent.py /usr/local/bin/codex && ln -s /usr/local/lib/ddtest-fake-agent.py /usr/local/bin/claude

FROM runtime AS agents
RUN npm install --global @openai/codex@0.163.0 @anthropic-ai/claude-code@2.1.220 && npm cache clean --force

# Optional browser-ready image. Flags apply only inside this disposable container;
# customer repository configuration and host browser settings remain untouched.
FROM agents AS browsers
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends xvfb xauth chromium chromium-driver && rm -rf /var/lib/apt/lists/*
RUN mv /usr/bin/chromium /usr/bin/chromium.original && printf '#!/bin/sh\nexec /usr/bin/chromium.original --no-sandbox --disable-dev-shm-usage "$@"\n' > /usr/bin/chromium && chmod +x /usr/bin/chromium
RUN printf '#!/bin/sh\nexec /usr/bin/chromium --no-sandbox --disable-dev-shm-usage "$@"\n' > /usr/local/bin/google-chrome && chmod +x /usr/local/bin/google-chrome
# WebdriverIO otherwise downloads Chrome for Testing's driver, which has no
# linux/arm64 build; the distribution driver matches this Chromium and arch.
ENV CHROMEDRIVER_PATH=/usr/bin/chromedriver

# Java trials: the JDK the JDK_IMAGE stage provides and a checksum-verified Maven.
FROM agents AS java
COPY --from=jdk /opt/java/openjdk /opt/java/openjdk
ENV JAVA_HOME=/opt/java/openjdk PATH=/opt/java/openjdk/bin:$PATH
ARG MAVEN_VERSION=3.9.11
ARG MAVEN_SHA512=bcfe4fe305c962ace56ac7b5fc7a08b87d5abd8b7e89027ab251069faebee516b0ded8961445d6d91ec1985dfe30f8153268843c89aa392733d1a3ec956c9978
RUN curl -fsSL -o /tmp/maven.tar.gz "https://archive.apache.org/dist/maven/maven-3/${MAVEN_VERSION}/binaries/apache-maven-${MAVEN_VERSION}-bin.tar.gz" \
    && echo "${MAVEN_SHA512}  /tmp/maven.tar.gz" | sha512sum -c - \
    && tar -xzf /tmp/maven.tar.gz -C /opt && rm /tmp/maven.tar.gz \
    && ln -s "/opt/apache-maven-${MAVEN_VERSION}/bin/mvn" /usr/local/bin/mvn
