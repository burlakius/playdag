"""pytest plugin that turns marked tests into playdag nodes: a marker carries
the node's contract, a fixture hands the test its context, and a report hook
tells the orchestrator what happened. Registered as a pytest11 entry point, so
installing the package is enough for `pytest.mark.playdag` to be recognised.
"""

from __future__ import annotations

import json
import os

import pytest

from . import protocol

NOT_RUN = "playdag: no test matched the requested node"

_PROVIDE_STASH_KEY = pytest.StashKey[dict]()

_STATUS_OF = {
    "passed": protocol.STATUS_PASSED,
    "failed": protocol.STATUS_FAILED,
    "skipped": protocol.STATUS_SKIPPED,
}


class PlaydagContext:
    """The context a test reads from and writes to. Values an earlier node
    produced arrive through `get`; values this node promised are handed back
    through `provide`, which is how a login test passes its session to
    everything after it.
    """

    def __init__(self, test_id: str | None, attempt: int, context: dict[str, str], item: pytest.Item):
        self.test_id = test_id
        self.attempt = attempt
        self._context = context
        self._item = item

    def get(self, key: str) -> str:
        try:
            return self._context[key]
        except KeyError:
            known = ", ".join(sorted(self._context)) or "nothing"
            raise KeyError(
                f'playdag: required key "{key}" is not in the context (available: {known}). '
                "Declare it in the test's requires and make sure a node provides it."
            ) from None

    def try_get(self, key: str) -> str | None:
        return self._context.get(key)

    def provide(self, key: str, value: object) -> None:
        self._item.stash.setdefault(_PROVIDE_STASH_KEY, {})[key] = str(value)


class Collector:
    """Tracks what the orchestrator asked for and what each node reported, so
    pytest_sessionfinish has a single place to read the outcome from.
    """

    def __init__(self) -> None:
        self.requested = protocol.read_envelope()
        self.results: dict[str, protocol.Response] = {}
        if self.requested:
            for req in self.requested.tests:
                self.results[req.test_id] = protocol.Response(error=NOT_RUN)

    def fallback_id(self) -> str | None:
        # A test with no marker id can only be matched positionally, and only
        # when a single node was asked for.
        if self.requested and not self.requested.batch and len(self.requested.tests) == 1:
            return self.requested.tests[0].test_id
        return None

    def envelope_json(self) -> dict | None:
        if self.requested is None:
            return None
        if self.requested.batch:
            return {
                "schema_version": protocol.SCHEMA_VERSION,
                "results": {k: v.to_json() for k, v in self.results.items()},
            }
        if not self.requested.tests:
            return protocol.Response(error=NOT_RUN).to_json()
        only = self.requested.tests[0].test_id
        return self.results.get(only, protocol.Response(error=NOT_RUN)).to_json()


def pytest_addoption(parser: pytest.Parser) -> None:
    group = parser.getgroup("playdag")
    group.addoption(
        "--playdag-collect",
        action="store",
        default=None,
        metavar="PATH",
        help="write playdag node metadata as JSON to PATH instead of running the suite",
    )


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line(
        "markers", "playdag(**kwargs): declare this test as a playdag node (id, requires, provides, ...)"
    )
    config._playdag_collector = Collector()  # type: ignore[attr-defined]


def pytest_collection_finish(session: pytest.Session) -> None:
    path = session.config.getoption("--playdag-collect")
    if not path:
        return

    nodes = []
    for item in session.items:
        marker = item.get_closest_marker("playdag")
        if marker is None or not marker.kwargs.get("id"):
            continue
        file_path, lineno, _ = item.location
        node = {"nodeid": item.nodeid, "file": file_path, "line": (lineno or 0) + 1}
        node.update({k: v for k, v in marker.kwargs.items() if v is not None})
        nodes.append(node)

    with open(path, "w", encoding="utf-8") as f:
        json.dump({"nodes": nodes}, f)


@pytest.fixture
def playdag(request: pytest.FixtureRequest) -> PlaydagContext:
    marker = request.node.get_closest_marker("playdag")
    node_id = marker.kwargs.get("id") if marker else None
    req = protocol.request_for(node_id)
    context = req.context if req else {}
    attempt = req.attempt if req else 1
    return PlaydagContext(req.test_id if req else node_id, attempt, context, request.node)


@pytest.hookimpl(wrapper=True)
def pytest_runtest_makereport(item: pytest.Item, call: pytest.CallInfo):
    report = yield

    collector: Collector = item.config._playdag_collector  # type: ignore[attr-defined]
    if collector.requested is None:
        return report

    # Only setup (when it fails or is skipped) and call decide the outcome;
    # teardown never overturns a result the test already reported.
    ready = report.when == "call" or (report.when == "setup" and report.outcome != "passed")
    if ready:
        marker = item.get_closest_marker("playdag")
        node_id = (marker.kwargs.get("id") if marker else None) or collector.fallback_id()
        if node_id is not None:
            collector.results[node_id] = protocol.Response(
                status=_STATUS_OF.get(report.outcome, protocol.STATUS_FAILED),
                provides=dict(item.stash.get(_PROVIDE_STASH_KEY, {})),
                error=str(report.longrepr) if report.longrepr else "",
            )
    return report


def pytest_sessionfinish(session: pytest.Session) -> None:
    collector: Collector = session.config._playdag_collector  # type: ignore[attr-defined]
    payload = collector.envelope_json()
    if payload is None:
        return

    text = json.dumps(payload)
    path = os.environ.get(protocol.RESULT_ENV)
    if not path:
        print(text)
        return
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
