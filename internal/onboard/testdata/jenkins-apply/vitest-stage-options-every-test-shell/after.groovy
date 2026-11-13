pipeline {
  agent none
  stages {
    stage('Docker') {
      agent { docker { image 'node:22' } }
      steps { sh 'npm ci' }
    }
    stage('Host') {
      agent { label 'linux' }
      options {
        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
      }
      steps {
        sh 'if [ -n "${DD_TRACE_PATH:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_PATH/register.js"; fi; npx vitest run --dir packages/a'
        sh 'if [ -n "${DD_TRACE_PATH:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_PATH/register.js"; fi; npx vitest run --dir packages/b'
      }
    }
  }
}
