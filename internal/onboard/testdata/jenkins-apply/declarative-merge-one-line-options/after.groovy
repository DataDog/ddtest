pipeline {
  agent any
  options { timestamps(); datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']]) }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
