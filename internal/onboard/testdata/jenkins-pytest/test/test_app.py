from app import greeting


def test_greeting():
    assert greeting("Jenkins") == "Hello, Jenkins!"
