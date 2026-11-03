node('linux') {
    stage('Test') {
        sh '''
            npm ci
            npx vitest run
        '''
    }
}
