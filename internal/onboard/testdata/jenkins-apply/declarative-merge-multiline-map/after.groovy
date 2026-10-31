pipeline {
  agent any
  options {
    datadog(
      testOptimization: [
        serviceName: "web",
        languages: [
          "RUBY",
          "JAVASCRIPT",
        ],
        enabled: true,
      ]
    )
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
