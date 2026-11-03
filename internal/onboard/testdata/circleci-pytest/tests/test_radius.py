from einsteinpy import schwarzschild_radius


def test_sun():
    assert round(schwarzschild_radius(1.989e30)) == 2954
