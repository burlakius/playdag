# 2. The orchestrator is Go; only the adapter is TypeScript

Status: accepted

## Context

The reference case is Playwright, which is Node. The obvious question is why the
orchestrator is not Node too - one language, one toolchain, and the adapter
would need no protocol at all because it could simply be imported.

## Decision

The core is Go. TypeScript is used only for `adapters/playwright`, which must
live inside the Playwright process because that is where the fixture and the
reporter run.

## Consequences

**The core cannot quietly become Playwright-specific.** A Node orchestrator that
can `import { test } from '@playwright/test'` eventually will: the shortest fix
to any problem is to reach for the framework's own types, and after a few of
those the tool only orchestrates Playwright. A process boundary and a JSON
contract make that impossible rather than merely discouraged, which is what the
work claims - that the method is not tied to one framework. The Python agents in
`examples/process` are the evidence, and they cost nothing to support because
the core cannot tell them apart from a Playwright run.

**Concurrency is the problem the language is for.** The core is a scheduler:
fan-out, cancellation, timeouts, a shared store under concurrent writes.
Goroutines and contexts express that directly, and `go test -race` checks it -
the race detector has caught real defects here. Node's single-threaded model
would put this in callbacks around worker processes, which is the same design
with more ceremony.

**Deployment is a file.** CI gets a static binary with no runtime to install, no
`node_modules` to restore, and no version of Node to agree on. The orchestrator
is then independent of whatever the tests happen to need.

**The cost is real:** two toolchains, two test commands, and a protocol to keep
in step across a boundary that a shared import would have made free. Anyone
writing an adapter pays that price too - they must speak JSON rather than call a
function.

**Alternative considered:** Node core with adapters as plugins. Rejected on the
first consequence: what stops the core from importing the framework is not
discipline, it is not being able to.
