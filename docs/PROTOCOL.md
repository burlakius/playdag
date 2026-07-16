# playdag agent protocol v1.0

The orchestrator never links against a test framework. It talks to *agents* over
this contract, so an adapter can be written in any language that can parse JSON.

An agent is any program that:

1. receives a **request** as JSON — on stdin, or in the `PLAYDAG_CONTEXT`
   environment variable when the process cannot read stdin (a framework CLI, for
   example);
2. runs exactly one test node;
3. writes a **response** as JSON to stdout, and exits.

Anything an agent prints on stderr is diagnostic output; it is surfaced in the
error message when the process fails, and is otherwise ignored.

## Request

```json
{
  "schema_version": "1.0",
  "test_id": "card.create",
  "script": "tests/card/create.spec.ts",
  "attempt": 1,
  "base_url": "http://localhost:8000",
  "requires": ["session.token"],
  "provides": ["card.id"],
  "context": { "session.token": "eyJhbGci..." },

  "session.token": "eyJhbGci..."
}
```

| field            | meaning                                                     |
| ---------------- | ----------------------------------------------------------- |
| `schema_version` | version of this contract; `"1.0"`                            |
| `test_id`        | node identifier from the suite                               |
| `script`         | script path as declared in the suite                         |
| `attempt`        | 1-based attempt number; `> 1` means this is a retry          |
| `base_url`       | system-under-test entry point; also passed as `BASE_URL`     |
| `requires`       | keys the node declared it needs                              |
| `provides`       | keys the node is expected to produce                         |
| `context`        | resolved values for every key in `requires`                  |

Every `context` entry is **mirrored at the top level** of the same object, as
`session.token` is above. That is what keeps agents written against the
pre-envelope format — which read the flat context map directly — working
unchanged. A context key may therefore not be named after an envelope field
(`schema_version`, `test_id`, `script`, `attempt`, `base_url`, `requires`,
`provides`, `context`); the orchestrator rejects such a suite rather than
silently overwrite a field.

## Response

```json
{
  "schema_version": "1.0",
  "status": "PASSED",
  "provides": { "card.id": 42 },
  "error": "",
  "artifacts": [
    { "type": "trace", "path": "test-results/card-create/trace.zip" }
  ]
}
```

| field            | meaning                                                          |
| ---------------- | ---------------------------------------------------------------- |
| `schema_version` | optional; if present it must be `"1.0"`                          |
| `status`         | `PASSED`, `FAILED`, or `SKIPPED` — required                      |
| `provides`       | values for the keys the node provides                            |
| `error`          | failure detail; shown in the report when the node did not pass    |
| `artifacts`      | files produced for the node: `trace`, `video`, `screenshot`, `log` |

`provides` values may be strings, numbers, or booleans; the orchestrator stores
them as strings. Only the keys declared in the node's contract are meaningful —
they become available to dependent nodes through the context store.

A missing `schema_version` is read as a pre-envelope agent and accepted. Any
other version, a missing or unknown `status`, malformed JSON, or empty output is
a protocol violation: the node is failed with a diagnostic rather than guessed at.

## Minimal agent

```python
import json, sys

req = json.load(sys.stdin)
token = req["context"]["session.token"]      # or req["session.token"]

print(json.dumps({
    "schema_version": "1.0",
    "status": "PASSED",
    "provides": {"card.id": 42},
}))
```
