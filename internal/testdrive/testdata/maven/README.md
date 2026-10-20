# Maven validation fixtures

Each subdirectory is an independent Maven project with one passing test. JUnit
fixtures also contain a disabled test. Use JDK 17 and Maven 3.9 or a compatible
version; the first execution downloads public Maven dependencies.

For manual QA, copy a fixture to a disposable directory outside the ddtest
checkout, initialize a Git repository there with
`git init -b juan-fernandez/maven-fixture`, then run:

```sh
/path/to/ddtest onboard --framework junit5
/path/to/ddtest testdrive --framework junit5 --check-only --yes
/path/to/ddtest testdrive --framework junit5 --command 'mvn test' --tracer-version 1.67.1 --yes
```

Onboard requires a GitHub Actions workflow; add a normal checkout/setup-java/mvn
test workflow before that command. Replace junit5 with the fixture's directory
name. Cucumber also declares JUnit 4, and Karate also declares JUnit 5; choose
cucumber-java or karate explicitly.

Expect the baseline and reporting-only native results to agree, matching Java
telemetry, and a separate verdict for each controlled feature. The overall
verdict stays INCOMPLETE because this first Maven adapter does not certify CI
runtime/release selection or other profiles. A check-only rerun must mark any
retained execution as historical. Unsupported/custom probe configurations must
remain inconclusive, never counted as successful feature checks.

MUnit does not advertise skipping/disable with agent 1.67.1; expect four passed
features and two unsupported checks. Native ScalaTest uses its Maven plugin and
Karate uses native scenario JSON where Surefire's test-factory XML is empty.

Inspect both files under .testoptimization; do not add them to Git. The original
fixture must contain no target directory or ddtest probe after validation.
Remove the disposable fixture when done.
