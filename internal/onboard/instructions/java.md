Onboard this Maven repository to Datadog Test Optimization:

1. Inspect the existing Maven commands, reactor modules, profiles, Surefire/Failsafe
   configuration and GitHub Actions Java matrix. Supported frameworks are JUnit 4
   (>=4.10), JUnit 5 (>=5.3), TestNG (>=6.4), Spock (>=2.0), Cucumber JVM
   (>=5.4.0), Karate (>=1.0.0), ScalaTest (>=3.0.8) and Scala MUnit (>=0.7.28).
   Profile dependencies are detection candidates; confirm they actually execute.
   See https://docs.datadoghq.com/tests/setup/java/ for current compatibility.
2. Add `datadog/test-visibility-github-action@v3` after checkout and Java setup,
   before the existing Maven test step. Set `languages: java`, `api_key` from a
   repository secret, and the correct Datadog `site`. Preserve the original test
   commands, profiles, modules, JVM options, reporters, coverage and CI matrices.
   The action instruments the Maven build and its test JVMs; Java framework
   selection in ddtest does not filter tests from a mixed-framework Maven command.
3. Keep Maven >=3.2.1 and the project's supported JDK. For manual setup, download
   dd-java-agent.jar, append `-javaagent:/absolute/path/dd-java-agent.jar` to
   MAVEN_OPTS, and set DD_CIVISIBILITY_ENABLED=true, DD_ENV=ci and DD_SERVICE.
   Agentless CI also needs DD_CIVISIBILITY_AGENTLESS_ENABLED=true and DD_API_KEY.
   Do not put the agent on the application classpath or attach it twice.
   ScalaTest's native Maven plugin forks also need the agent through inherited
   JAVA_TOOL_OPTIONS; MAVEN_OPTS alone does not instrument that plugin's test JVM.
4. Inspect locally with `ddtest testdrive --framework __FRAMEWORK__ --check-only --yes`.
   Then run `ddtest testdrive --framework __FRAMEWORK__ --command './mvnw verify' --tracer-version <release> --yes`
   using the actual CI command (use `mvn` when there is no wrapper). Without
   --command, testdrive uses ./mvnw test or mvn test. Prepare project dependencies
   and external services first. Maven can populate its normal local dependency cache.
5. Review baseline/reporting-only native XML (Karate: scenario JSON), outcomes and telemetry in
   .testoptimization/testdrive.json and report.html. The local intake needs no
   Datadog API key. Missing native XML results, excluded tests, missing telemetry,
   unsupported probes or unverified CI remain incomplete. A local command does
   not certify other profiles, integration-test commands, CI Java versions or
   real backend processing. Rerun after final setup edits.

This Maven-first path supports onboard/testdrive. Use Maven's native command for
production optimization; ddtest plan/run, Gradle, Bazel and SBT are outside this path.
Keep both reports after cleanup, exclude .testoptimization from Git/packages,
and remove only scratch files that you created.
