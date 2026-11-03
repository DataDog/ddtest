pipeline {
  agent { docker { image 'node:22' } }
  stages {
    stage('Test') {
      matrix {
        axes {
          axis { name 'SHARD'; values '1', '2' }
        }
        stages {
          stage('Run') {
            steps {
              sh 'npm test'
            }
          }
        }
      }
    }
  }
}
