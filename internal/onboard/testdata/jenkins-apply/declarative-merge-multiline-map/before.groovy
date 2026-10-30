pipeline {
  agent any
  options {
    datadog(
      testOptimization: [
        serviceName: "web",
        languages: [
          "RUBY",
        ],
      ]
    )
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
