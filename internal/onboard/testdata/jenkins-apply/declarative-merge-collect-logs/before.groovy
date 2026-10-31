pipeline {
  agent any
  options {
    timeout(time: 30, unit: 'MINUTES')
    datadog(collectLogs: true, tags: ["team:web"])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
