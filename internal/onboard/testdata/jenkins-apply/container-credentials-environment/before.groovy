pipeline {
    agent { docker { image 'node:22' } }
    stages {
        stage('Test') {
            environment {
                DD_API_KEY = credentials('datadog-api-key')
            }
            steps {
                sh 'npm ci; npm test'
            }
        }
    }
}
