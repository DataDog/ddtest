datadog(collectLogs: true) { node { stage('Test') { sh 'npm test' } } }; datadog(collectLogs: true) { node { stage('Deploy') { sh 'echo deploying' } } }
