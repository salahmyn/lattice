"""No-op Lattice tags, so the fixture runs without the lattice package."""


def module_feature(*_):
    return None


def enforces_invariant(*_):
    return lambda fn: fn


def verifies(*_):
    return lambda fn: fn
