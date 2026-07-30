# Experiments

What is being measured: whether orchestrating a test suite by its data
contracts is faster than running the same scenarios the ordinary way, by how
much, and whether the difference is real rather than noise.

The subject is the [Playwright example](../examples/playwright/README.md) and the
[demo application](../examples/demo-app/server.js) - self-contained, so anyone
reading the results can reproduce them without access to a private application.

Two suites describe the same five scenarios:

- `tests/baseline.spec.ts` - every test independent, every test signing in for
  itself. This is how Playwright is normally used, and it is the prototype the
  method is compared against.
- `tests/board.spec.ts` - the same scenarios declaring what they need and
  produce, run by the orchestrator.

## Configurations

| Id | What runs | What it isolates |
|----|-----------|------------------|
| **E1** | `npx playwright test`, 1 worker | the baseline |
| **E1p** | `npx playwright test`, 4 workers | the baseline given the same parallelism |
| **E2** | playdag, 1 agent | context sharing alone |
| **E3** | playdag, 4 agents | context sharing plus dynamic scheduling |
| **E4** | playdag, 4 agents, `-fail-fast`, login broken | what early termination saves |
| **E4b** | baseline, 4 workers, login broken | what the same failure costs without it |
| **sN** | playdag, N ∈ {1,2,4,8} agents | the scaling curve, against Amdahl's law |

E1p exists because comparing a parallel run against a sequential baseline would
credit the method for parallelism anyone can have. E1p gives the baseline the
same four workers, so E1p -> E3 isolates what the orchestration itself is worth.

## Running

```bash
npm install
npx playwright install chromium
pip install scipy numpy matplotlib

# the configurations above
./experiments/run.sh ../experiments/results 11
python3 experiments/analyze.py ../experiments/results

# where the method starts to pay
./experiments/sensitivity.sh ../experiments/sensitivity 6
python3 experiments/sensitivity.py ../experiments/sensitivity
```

`run.sh` writes one JSON per run; `analyze.py` turns them into `results.md`,
`results.json` and figures. Measurements go outside this repository by default:
they are evidence for the write-up, not source code.

## Sensitivity: the question behind the numbers

Whether orchestration wins is not a property of the method alone. Sharing a
session saves what establishing it costs, once for every test that no longer
does it; running each node in its own framework process costs a fixed startup,
every node. The balance turns on one number - how expensive the repeated
operation is.

The demo application signs a user in in about ten milliseconds, which no real
application does. `sensitivity.sh` therefore varies that cost directly
(`DEMO_LOGIN_MS`) and measures both suites at each value, so the point where the
curves cross is measured rather than argued. It is reported in
`sensitivity.md`, and it is the number that says which suites this method is
for.

## Method

- **11 runs per configuration**, the first dropped as a warm-up: the first run
  pays for a cold browser and a cold cache, which measures the machine rather
  than the method.
- **A fresh application per run.** Every run starts a new server, so no run
  inherits state - or a session - from the one before it.
- **Wall clock is measured outside the tool**, the same way for both suites, so
  neither is credited for its own bookkeeping.
- **Failing runs are still timed.** A failure costs CI time too; in E4 that cost
  is the entire subject.
- **Welch's t-test**, which does not assume the two configurations have the same
  variance - they do not, since the baseline repeats work that the orchestrated
  run does once.
- **Cohen's d and 95% confidence intervals** alongside the p-value: with enough
  runs almost any difference becomes significant, and the question is how large
  it is and how sure we can be of its size.
- **The serial fraction** is estimated from the measured speedups (Karp-Flatt)
  rather than assumed, and the Amdahl curve drawn from it is the honest ceiling
  on what more agents could buy for this suite.

## Reading the results

`results.md` holds the tables; the figures are:

- `fig1-configurations.png` - what each configuration costs, with 95% intervals;
- `fig2-scaling.png` - measured speedup against the linear ideal and Amdahl;
- `fig3-spread.png` - the spread of every run, which is what CV% summarises.
