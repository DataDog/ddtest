from project import full_name


def test_full_name():
    assert full_name("Ada", "Lovelace") == "Ada Lovelace"


def test_missing_last_name():
    assert full_name("Ada", "") == "Ada"
