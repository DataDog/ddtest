// The options directive goes after the later of agent and environment.
pipeline {
  environment {
    CI = 'true'
  }
  agent any // any host
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
