pipeline {
  agent any
  options {
    timeout(time: 30, unit: 'MINUTES')
    datadog(collectLogs: true, tags: ["team:web"], testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
