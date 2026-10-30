pipeline {
    agent any
    options {
        timeout(time: 1, unit: 'HOURS')
        buildDiscarder(logRotator(numToKeepStr: '10'))
        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
    }
    stages {
        stage('Test') { steps { sh 'npm test' } }
    }
}
