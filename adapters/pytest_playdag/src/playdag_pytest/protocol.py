"""The playdag wire contract, restated in Python. This module is the only part
of the adapter the orchestrator constrains; everything else here is pytest's
business. See docs/PROTOCOL.md for the specification.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from typing import Any

SCHEMA_VERSION = "1.1"

# Versions this adapter accepts from the orchestrator.
SUPPORTED_VERSIONS = {"1.0", SCHEMA_VERSION}

# Environment variable carrying the encoded request.
CONTEXT_ENV = "PLAYDAG_CONTEXT"

# Environment variable naming the file the response is written to.
RESULT_ENV = "PLAYDAG_RESULT"

STATUS_PASSED = "PASSED"
STATUS_FAILED = "FAILED"
STATUS_SKIPPED = "SKIPPED"


@dataclass
class Request:
    schema_version: str
    test_id: str
    script: str = ""
    attempt: int = 1
    base_url: str = ""
    requires: list[str] = field(default_factory=list)
    provides: list[str] = field(default_factory=list)
    context: dict[str, str] = field(default_factory=dict)


@dataclass
class Response:
    status: str = STATUS_FAILED
    provides: dict[str, str] = field(default_factory=dict)
    error: str = ""
    schema_version: str = SCHEMA_VERSION

    def to_json(self) -> dict[str, Any]:
        out: dict[str, Any] = {"schema_version": self.schema_version, "status": self.status}
        if self.provides:
            out["provides"] = self.provides
        if self.error:
            out["error"] = self.error
        return out


@dataclass
class Envelope:
    # True when the orchestrator sent several nodes at once.
    batch: bool
    tests: list[Request]


def read_envelope(env: dict[str, str] | None = None) -> Envelope | None:
    """Reads what the orchestrator passed in: one node, a batch of them, or
    nothing at all when the tests are run directly by pytest. That last case is
    what lets a suite still be run and debugged with plain `pytest`.
    """
    env = env if env is not None else os.environ
    raw = env.get(CONTEXT_ENV)
    if not raw:
        return None

    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError as e:
        raise ValueError(f"playdag: {CONTEXT_ENV} is not valid JSON: {e}") from e

    version = parsed.get("schema_version", "")
    if version not in SUPPORTED_VERSIONS:
        raise ValueError(
            f"playdag: unsupported schema_version {version!r} (want one of {sorted(SUPPORTED_VERSIONS)})"
        )

    tests = parsed.get("tests")
    if isinstance(tests, list):
        return Envelope(batch=True, tests=[_request_from(t) for t in tests])
    return Envelope(batch=False, tests=[_request_from(parsed)])


def _request_from(d: dict[str, Any]) -> Request:
    return Request(
        schema_version=d.get("schema_version", SCHEMA_VERSION),
        test_id=d.get("test_id", ""),
        script=d.get("script", ""),
        attempt=d.get("attempt", 1),
        base_url=d.get("base_url", ""),
        requires=list(d.get("requires") or []),
        provides=list(d.get("provides") or []),
        context=dict(d.get("context") or {}),
    )


def request_for(test_id: str | None, env: dict[str, str] | None = None) -> Request | None:
    """The request for one test, matched by node id. A batch process runs
    several tests and each must see only its own context.
    """
    envelope = read_envelope(env)
    if envelope is None:
        return None
    if not envelope.batch:
        return envelope.tests[0] if envelope.tests else None
    return next((t for t in envelope.tests if t.test_id == test_id), None)
