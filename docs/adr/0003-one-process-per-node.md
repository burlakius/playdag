# 3. A node is a process

Status: accepted, with a measured cost

## Context

The orchestrator runs each node by starting an agent: `python3 script.py`, or
`npx playwright test --grep @id:<node>`. One node, one process.

## Decision

Keep it. The process boundary is what the protocol is for.

## Consequences

**Isolation is free and total.** A node cannot leak state into another except
through the context store, which is the only channel the design admits. A
crashed agent takes nothing with it, a hung one is killed by its process group,
and what a node shares is exactly what it declared.

**Any language works.** An agent is a program that reads JSON and writes JSON.
That is the whole reason the same core runs Playwright and plain Python scripts.

**It costs a framework startup per node, and that cost is not small.** Measured
on this machine, one `npx playwright test` process takes **≈1.6 s**, of which
the test itself is ≈0.35 s. The five-node example therefore pays ≈7.5 s of
startup, and the measured E2 run is 7.75 s — almost all of it process startup.

That is larger than what sharing a session saves on a fast application, and the
experiment says so plainly: against the ordinary Playwright baseline the
orchestrated suite is **2.4× slower** on the demo, and only breaks even once
signing in costs about **3 s** — the point where the repeated work removed
exceeds the startup added. See `experiments/`.

So this decision, and not the method, is what decides whether the tool is worth
using on a given suite. The method removes work proportional to the number of
tests that no longer repeat an operation; this design adds work proportional to
the number of nodes. Suites whose setup is expensive win; suites of many cheap
tests lose.

**Alternative, not yet taken:** batch several nodes into one framework process —
run a wave of ready nodes as one `npx playwright test` invocation, keeping the
context hand-off between waves. That amortises the startup over a wave and would
move the break-even down by roughly the startup cost, at the price of weaker
isolation within a wave and a scheduler that must reason about groups rather
than nodes. It is the first thing to do if this tool is to pay off on suites of
fast tests, and the experiment is the argument for doing it.
