# Build --target offline for fake CLI tests, or --target agents for real trials;
# --target java adds a JDK and Maven for --framework junit4/junit5 trials.
# JDK_IMAGE selects another Temurin JDK for a project that needs one.
# --target python and --target ruby add an official CPython or Ruby build for
# pytest and rspec/minitest trials; PYTHON_IMAGE with PYTHON_VERSION, or
# RUBY_IMAGE, select another version.
ARG JDK_IMAGE=eclipse-temurin:21-jdk@sha256:5d90b26c0784ccf55af55f97cbcf7f3969c66a06299c93bdc72649219942a48f
ARG PYTHON_IMAGE=python:3.12-bookworm@sha256:a5bab0ff300f91231c2e68bf73a8a8d60762ac39ce1c50eaf6803e4c38a9062b
ARG RUBY_IMAGE=ruby:3.4-bookworm@sha256:8853c6f24866429483fce3a869e61a6a13916beb2720468c6d95c78f41c0f564
FROM ${JDK_IMAGE} AS jdk
FROM ${PYTHON_IMAGE} AS cpython
FROM ${RUBY_IMAGE} AS cruby

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
# Trial commands keep JAVA_HOME but run with a fixed PATH, so the JDK's tools
# are linked into it.
ENV JAVA_HOME=/opt/java/openjdk
RUN ln -s /opt/java/openjdk/bin/* /usr/local/bin/
ARG MAVEN_VERSION=3.9.11
ARG MAVEN_SHA512=bcfe4fe305c962ace56ac7b5fc7a08b87d5abd8b7e89027ab251069faebee516b0ded8961445d6d91ec1985dfe30f8153268843c89aa392733d1a3ec956c9978
RUN curl -fsSL -o /tmp/maven.tar.gz "https://archive.apache.org/dist/maven/maven-3/${MAVEN_VERSION}/binaries/apache-maven-${MAVEN_VERSION}-bin.tar.gz" \
    && echo "${MAVEN_SHA512}  /tmp/maven.tar.gz" | sha512sum -c - \
    && tar -xzf /tmp/maven.tar.gz -C /opt && rm /tmp/maven.tar.gz \
    && ln -s "/opt/apache-maven-${MAVEN_VERSION}/bin/mvn" /usr/local/bin/mvn

# pytest trials: the official CPython build (same Debian bookworm base) with
# venv and pip; pass --python /usr/local/bin/python3. PYTHON_VERSION names the
# PYTHON_IMAGE's major.minor.
FROM agents AS python
ARG PYTHON_VERSION=3.12
COPY --from=cpython /usr/local/bin/python${PYTHON_VERSION} /usr/local/bin/
COPY --from=cpython /usr/local/lib/python${PYTHON_VERSION} /usr/local/lib/python${PYTHON_VERSION}
COPY --from=cpython /usr/local/include/python${PYTHON_VERSION} /usr/local/include/python${PYTHON_VERSION}
COPY --from=cpython /usr/local/lib/libpython${PYTHON_VERSION}.so.1.0 /usr/local/lib/
RUN ln -s "libpython${PYTHON_VERSION}.so.1.0" "/usr/local/lib/libpython${PYTHON_VERSION}.so" \
    && ln -s "python${PYTHON_VERSION}" /usr/local/bin/python3 && ln -s python3 /usr/local/bin/python && ldconfig \
    && python3 -c 'import ssl, sqlite3, ctypes, lzma, bz2, zlib, venv, ensurepip' \
    && python3 -m venv /tmp/venv && /tmp/venv/bin/python -m pip --version && rm -rf /tmp/venv

# rspec/minitest trials: the official Ruby build with its default gems and Bundler.
FROM agents AS ruby
COPY --from=cruby /usr/local/bin/ruby /usr/local/bin/gem /usr/local/bin/bundle /usr/local/bin/bundler \
    /usr/local/bin/rake /usr/local/bin/erb /usr/local/bin/irb /usr/local/bin/rdoc /usr/local/bin/ri /usr/local/bin/
COPY --from=cruby /usr/local/lib/ruby /usr/local/lib/ruby
COPY --from=cruby /usr/local/include /usr/local/include
COPY --from=cruby /usr/local/lib/ /tmp/rubylib/
RUN cp -a /tmp/rubylib/libruby.so* /usr/local/lib/ && rm -rf /tmp/rubylib && ldconfig \
    && ruby -ropenssl -ryaml -rzlib -e 'puts RUBY_VERSION' && bundle --version
