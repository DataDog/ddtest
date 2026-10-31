node('docker') {
    stage('Checkout') {
        checkout scm
    }
    stage('Test') {
        docker.image('node:22').inside {
            sh 'npm ci'
            sh 'npm test'
        }
    }
}
