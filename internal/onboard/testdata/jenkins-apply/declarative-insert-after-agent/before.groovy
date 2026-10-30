pipeline {
    agent { label 'sl7'}
    stages {
        stage('Unit Tests') {
            steps {
                sh 'npm test'
            }
        }
    }
}
