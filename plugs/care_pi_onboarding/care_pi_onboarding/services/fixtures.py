import json
from functools import cache
from pathlib import Path

FIXTURES_DIR = Path(__file__).resolve().parent.parent / "fixtures"


@cache
def load(name: str):
    with (FIXTURES_DIR / name).open(encoding="utf-8") as f:
        return json.load(f)


def locale(code: str) -> dict:
    path = FIXTURES_DIR / "locales" / f"{code}.json"
    if not code.isalnum() or not path.is_file():
        available = sorted(p.stem for p in (FIXTURES_DIR / "locales").glob("*.json"))
        msg = f"The locale {code!r} has no fixture. The locales are: {', '.join(available)}."
        raise ValueError(msg)
    return load(f"locales/{code}.json")


def states(loc: dict) -> dict[str, dict]:
    """The states of a locale by their lower-case name and by their lower-case aliases."""
    index = {}
    for s in loc.get("states", []):
        for name in [*s.get("aliases", []), s["name"]]:
            index[name.lower()] = s
    return index
