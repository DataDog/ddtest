pipeline {
    agent { label 'sl7'}
    options {
        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
    }
    stages {
        stage('Unit Tests') {
            steps {
                sh 'npm test'
            }
        }
    }
}
