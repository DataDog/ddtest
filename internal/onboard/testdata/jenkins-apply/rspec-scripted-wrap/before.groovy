#!groovy
// From deanwilson/puppet-multitemplate (Apache-2.0), Jenkinsfile, unchanged;
// the repository is a plain RSpec project instead of rspec-puppet.

node {
    stage('Checkout') {
        checkout scm
    }

    stage('Build') {
        sh 'bundle install'
    }

    stage('Test') {
        sh 'bundle exec rake spec'
    }
}
