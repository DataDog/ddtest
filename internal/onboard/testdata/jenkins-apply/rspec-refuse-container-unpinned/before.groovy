pipeline {
  agent { docker { image 'ruby:3.3' } }
  stages {
    stage('Test') {
      steps {
        sh 'bundle install'
        sh 'bundle exec rake spec'
      }
    }
  }
}
