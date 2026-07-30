import json

import pytest


def test_collect_writes_manifest_for_marked_tests(pytester: pytest.Pytester) -> None:
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="auth.login", provides=["session.token"], criticality="BLOCKER", priority=10)
        def test_login():
            pass

        def test_plain():
            pass
        """
    )
    manifest = pytester.path / "manifest.json"

    result = pytester.runpytest("-p", "no:playwright", "--collect-only", f"--playdag-collect={manifest}")
    result.assert_outcomes()

    data = json.loads(manifest.read_text())
    ids = {n["id"] for n in data["nodes"]}
    assert ids == {"auth.login"}

    node = data["nodes"][0]
    assert node["nodeid"].endswith("test_mod.py::test_login")
    assert node["provides"] == ["session.token"]
    assert node["criticality"] == "BLOCKER"
    assert node["priority"] == 10
    assert node["line"] >= 1


def test_fixture_provides_and_reports_result(pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch) -> None:
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="auth.login", provides=["session.token"])
        def test_login(playdag):
            playdag.provide("session.token", "tok123")
        """
    )
    result_path = pytester.path / "result.json"
    monkeypatch.setenv(
        "PLAYDAG_CONTEXT",
        json.dumps({"schema_version": "1.1", "test_id": "auth.login", "attempt": 1, "context": {}}),
    )
    monkeypatch.setenv("PLAYDAG_RESULT", str(result_path))

    result = pytester.runpytest("-p", "no:playwright", "test_mod.py::test_login")
    result.assert_outcomes(passed=1)

    payload = json.loads(result_path.read_text())
    assert payload["status"] == "PASSED"
    assert payload["provides"] == {"session.token": "tok123"}


def test_fixture_get_reads_context(pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch) -> None:
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="card.create", requires=["session.token"])
        def test_create(playdag):
            assert playdag.get("session.token") == "tok123"
        """
    )
    result_path = pytester.path / "result.json"
    monkeypatch.setenv(
        "PLAYDAG_CONTEXT",
        json.dumps(
            {
                "schema_version": "1.1",
                "test_id": "card.create",
                "attempt": 1,
                "context": {"session.token": "tok123"},
            }
        ),
    )
    monkeypatch.setenv("PLAYDAG_RESULT", str(result_path))

    result = pytester.runpytest("-p", "no:playwright", "test_mod.py::test_create")
    result.assert_outcomes(passed=1)


def test_missing_required_key_fails_with_a_clear_message(
    pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch
) -> None:
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="card.create", requires=["session.token"])
        def test_create(playdag):
            playdag.get("session.token")
        """
    )
    result_path = pytester.path / "result.json"
    monkeypatch.setenv(
        "PLAYDAG_CONTEXT",
        json.dumps({"schema_version": "1.1", "test_id": "card.create", "attempt": 1, "context": {}}),
    )
    monkeypatch.setenv("PLAYDAG_RESULT", str(result_path))

    result = pytester.runpytest("-p", "no:playwright", "test_mod.py::test_create")
    result.assert_outcomes(failed=1)

    payload = json.loads(result_path.read_text())
    assert payload["status"] == "FAILED"
    assert "session.token" in payload["error"]


def test_batch_reports_every_requested_id(pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch) -> None:
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="a")
        def test_a(playdag):
            pass

        @pytest.mark.playdag(id="b")
        def test_b(playdag):
            raise AssertionError("boom")
        """
    )
    result_path = pytester.path / "result.json"
    monkeypatch.setenv(
        "PLAYDAG_CONTEXT",
        json.dumps(
            {
                "schema_version": "1.1",
                "tests": [
                    {"schema_version": "1.1", "test_id": "a", "attempt": 1, "context": {}},
                    {"schema_version": "1.1", "test_id": "b", "attempt": 1, "context": {}},
                ],
            }
        ),
    )
    monkeypatch.setenv("PLAYDAG_RESULT", str(result_path))

    result = pytester.runpytest("-p", "no:playwright", "test_mod.py::test_a", "test_mod.py::test_b")
    result.assert_outcomes(passed=1, failed=1)

    payload = json.loads(result_path.read_text())
    assert payload["results"]["a"]["status"] == "PASSED"
    assert payload["results"]["b"]["status"] == "FAILED"
    assert "boom" in payload["results"]["b"]["error"]


def test_batch_node_never_run_stays_reported_as_not_run(
    pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch
) -> None:
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="a")
        def test_a(playdag):
            pass
        """
    )
    result_path = pytester.path / "result.json"
    monkeypatch.setenv(
        "PLAYDAG_CONTEXT",
        json.dumps(
            {
                "schema_version": "1.1",
                "tests": [
                    {"schema_version": "1.1", "test_id": "a", "attempt": 1, "context": {}},
                    {"schema_version": "1.1", "test_id": "b", "attempt": 1, "context": {}},
                ],
            }
        ),
    )
    monkeypatch.setenv("PLAYDAG_RESULT", str(result_path))

    # Only "a" is actually selected - "b" is asked for but an agent that never
    # reaches it must not make the node quietly vanish from the report.
    result = pytester.runpytest("-p", "no:playwright", "test_mod.py::test_a")
    result.assert_outcomes(passed=1)

    payload = json.loads(result_path.read_text())
    assert payload["results"]["a"]["status"] == "PASSED"
    assert payload["results"]["b"]["status"] == "FAILED"
    assert "no test matched" in payload["results"]["b"]["error"]


def test_runs_without_the_orchestrator(pytester: pytest.Pytester, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("PLAYDAG_CONTEXT", raising=False)
    pytester.makepyfile(
        test_mod="""
        import pytest

        @pytest.mark.playdag(id="a")
        def test_a(playdag):
            assert playdag.try_get("nothing") is None
        """
    )

    result = pytester.runpytest("-p", "no:playwright", "test_mod.py::test_a")
    result.assert_outcomes(passed=1)
