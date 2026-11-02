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
              withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {
                sh '''
                  if [ "${DD_API_KEY:+set}" = set ]; then
                    export DD_TRACER_FOLDER="$PWD/.datadog-ci-container" DD_SITE="datadoghq.com" DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="jenkins"
                    if mkdir -p "$DD_TRACER_FOLDER" &&
                      { curl -fsSL -o "$DD_TRACER_FOLDER/install_test_visibility.sh" https://install.datadoghq.com/scripts/install_test_visibility_v15.sh || wget -qO "$DD_TRACER_FOLDER/install_test_visibility.sh" https://install.datadoghq.com/scripts/install_test_visibility_v15.sh; } &&
                      echo "e64a4a45e0b171f4294b9f949f7fc99946192f952a2d463c61fad3cb8b956a58  $DD_TRACER_FOLDER/install_test_visibility.sh" | { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1 &&
                      DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES="js" DD_SET_TRACER_VERSION_JS="6.22.0" bash "$DD_TRACER_FOLDER/install_test_visibility.sh" >"$DD_TRACER_FOLDER/test-optimization.env"; then
                      while IFS='=' read -r dd_name dd_value; do
                        case "$dd_name" in '' | [0-9]* | *[!A-Za-z0-9_]*) ;; *) export "$dd_name=$dd_value" ;; esac
                      done <"$DD_TRACER_FOLDER/test-optimization.env"
                    else
                      echo "Datadog Test Optimization could not be installed; the tests run without it." >&2
                    fi
                  else
                    echo "DD_API_KEY is not set; Datadog Test Optimization is disabled for this step." >&2
                  fi
                  npm test
                '''
              }
            }
          }
        }
      }
    }
  }
}
