from pybuildkite import pipeline_url


def test_pipeline_url():
    assert pipeline_url("acme", "app") == "https://buildkite.com/acme/app"
