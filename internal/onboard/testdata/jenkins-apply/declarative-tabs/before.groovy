pipeline {
	agent any
	stages {
		stage('Test') {
			steps {
				sh 'npm test'
			}
		}
	}
}
