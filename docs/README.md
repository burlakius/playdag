# Documentation

- [PROTOCOL.md](PROTOCOL.md) - the contract between the orchestrator and an
  agent. Read this to write an adapter in any language.

## Decisions

The three questions this project gets asked, with the answers and what they
cost:

- [1. Order comes from data contracts, not `depends_on`](adr/0001-data-contracts-not-depends-on.md)
- [2. The orchestrator is Go; only the adapter is TypeScript](adr/0002-go-for-the-orchestrator.md)
- [3. A node is a process](adr/0003-one-process-per-node.md) - including the
  measured cost of that choice, which is the main limitation of the tool.
