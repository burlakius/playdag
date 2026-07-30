# 1. Order comes from data contracts, not from `depends_on`

Status: accepted

## Context

The suite has to run in an order that works: a test that needs a session cannot
run before the test that establishes one. Something must express that.

The obvious way is to let a test name its predecessors - `depends_on:
["auth.login"]` - which is what most pipeline tools do and what a reviewer
expects to see.

## Decision

A node declares the data it **requires** and **provides**. The edges are derived:
whoever provides `session.token` runs before whoever requires it.

## Consequences

**A dependency cannot be wrong about itself.** `depends_on` is a claim about
order that nothing checks: a test that stops needing the session keeps the edge
until someone notices, and a test that starts needing one may forget to add it
and pass for weeks on leftover state. `requires` is not a claim about order - it
is what the test reads, and the test cannot run without it. The order is a
consequence, and it cannot drift from the code.

**The values travel with the edge.** An order-only dependency says when to run;
it does not carry the token from one test to the next, so each test still
establishes its own session and the repeated work stays. Because our edge exists
*because of* a value, the value is there to hand over. Removing the repeated
work is the point of the whole thing, and `depends_on` cannot express it.

**Broken contracts are found before the run.** Two tests providing the same key,
a key nobody provides, a cycle: all of it is decidable from the declarations, so
`orchestrator validate` and `extract` reject it against the file and line that
declared it. With `depends_on`, a name that no longer exists is just a name.

**The cost:** the contract is more to write than a list of names, and a node
that must run after another without exchanging anything - a cleanup, an audit -
has no key to hang the edge on. Such a node must publish a marker key
(`card.archived`) to be ordered at all, which is honest but wordy.

**Alternative considered:** support both, with `depends_on` as an escape hatch.
Rejected: two ways to express order means the graph is the union of a checked
one and an unchecked one, and the unchecked one wins every argument about what
actually ran. If ordering without data exchange proves common, a marker key is
the same thing with the checking left on.
