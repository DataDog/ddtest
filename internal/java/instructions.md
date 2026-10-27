# Enable Datadog Test Optimization for Maven on GitHub Actions

Apply this setup to every GitHub Actions job that runs Maven tests. Supported frameworks are JUnit 4 (4.10+), JUnit 5 (5.3+), TestNG (6.4+), Spock (2.0+), Cucumber JVM (5.4.0+), Karate (1.0.0+), ScalaTest (3.0.8+) and Scala MUnit (0.7.28+), with Maven 3.2.1 or newer. See https://docs.datadoghq.com/tests/setup/java/ for current compatibility. A framework declared only in a Maven profile runs only when CI activates that profile.

## 1. Instrument the test job

Add this step after checkout and Java setup, immediately before the first Maven test step:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v3
  with:
    languages: java
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
```

If the organization uses a Datadog site other than US1, replace `datadoghq.com` with that site.

The action instruments the Maven build and the test JVMs it starts. Keep the existing Maven commands, profiles, modules, JVM options, reporters, coverage and matrices unchanged. Add the Datadog action once per test job, not once per test step.

Without the action, download `dd-java-agent.jar`, append `-javaagent:/absolute/path/dd-java-agent.jar` to `MAVEN_OPTS`, and set `DD_CIVISIBILITY_ENABLED=true`, `DD_CIVISIBILITY_AGENTLESS_ENABLED=true`, `DD_API_KEY`, `DD_ENV=ci` and `DD_SERVICE` on the test step. Do not put the agent on the application classpath or attach it twice. ScalaTest's `scalatest-maven-plugin` forks also need the agent in `JAVA_TOOL_OPTIONS`; `MAVEN_OPTS` alone does not instrument that plugin's test JVM.

## 2. Ask a human to connect Datadog

__CONNECT_DATADOG__

ddtest does not apply this setup automatically or run Java tests locally, and `ddtest plan` and `ddtest run` do not support Java: the Java agent performs Test Impact Analysis inside the Maven build.
