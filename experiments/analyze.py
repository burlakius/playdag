#!/usr/bin/env python3
"""Turns the run files written by run.sh into the tables and figures the
write-up needs.

    pip install scipy numpy matplotlib
    python3 experiments/analyze.py <results-dir> [-o <output-dir>]

Reads <results-dir>/<config>_run_<n>.json, drops the warm-up run of each
configuration, and reports for every comparison a Welch's t-test (the samples
are not assumed to share a variance), Cohen's d for the size of the effect, and
a 95% confidence interval for the difference — a p-value alone says an effect
exists, not whether it is worth anything.
"""

import argparse
import glob
import json
import math
import os
import statistics
import sys
from dataclasses import dataclass, field

try:
    import numpy as np
    from scipy import stats
except ImportError:
    sys.exit("this needs scipy and numpy: pip install scipy numpy matplotlib")

# WARMUP is dropped from every configuration: the first run pays for a cold
# browser, a cold filesystem cache and a JIT that has never seen this code, and
# measuring that would be measuring the machine rather than the method.
WARMUP = 1

# Labels used in tables and figures.
CONFIG_LABELS = {
    "e1": "E1 baseline, 1 worker",
    "e1p": "E1p baseline, 4 workers",
    "e2": "E2 playdag, 1 worker",
    "e3": "E3 playdag, 4 workers",
    "e4": "E4 playdag, fail-fast, login broken",
    "e4b": "E4b baseline, login broken",
}


@dataclass
class Sample:
    """One configuration's measured runs, warm-up already dropped."""

    config: str
    seconds: list[float] = field(default_factory=list)
    worker_seconds: list[float] = field(default_factory=list)
    utilization: list[float] = field(default_factory=list)
    passed: list[int] = field(default_factory=list)
    total: list[int] = field(default_factory=list)
    workers: int | None = None

    @property
    def n(self) -> int:
        return len(self.seconds)

    @property
    def mean(self) -> float:
        return statistics.mean(self.seconds)

    @property
    def stdev(self) -> float:
        return statistics.stdev(self.seconds) if self.n > 1 else 0.0

    @property
    def cv(self) -> float:
        """Coefficient of variation: how repeatable the configuration is."""
        return 100 * self.stdev / self.mean if self.mean else 0.0

    @property
    def mean_worker_seconds(self) -> float:
        return statistics.mean(self.worker_seconds) if self.worker_seconds else float("nan")


def load(results_dir: str) -> dict[str, Sample]:
    samples: dict[str, Sample] = {}

    for path in sorted(glob.glob(os.path.join(results_dir, "*_run_*.json"))):
        with open(path) as f:
            rec = json.load(f)

        if rec["run"] <= WARMUP:
            continue

        s = samples.setdefault(rec["config"], Sample(config=rec["config"]))
        s.seconds.append(rec["wall_clock_seconds"])
        if rec.get("worker_seconds") is not None:
            s.worker_seconds.append(rec["worker_seconds"])
        if rec.get("utilization") is not None:
            s.utilization.append(rec["utilization"])
        if rec.get("passed") is not None:
            s.passed.append(rec["passed"])
        if rec.get("total_tests") is not None:
            s.total.append(rec["total_tests"])
        if rec.get("workers") is not None:
            s.workers = rec["workers"]

    return samples


def cohens_d(a: list[float], b: list[float]) -> float:
    """Effect size on the pooled standard deviation. It answers the question a
    p-value does not: how large is the difference, in units of the noise."""
    na, nb = len(a), len(b)
    if na < 2 or nb < 2:
        return float("nan")
    va, vb = statistics.variance(a), statistics.variance(b)
    pooled = math.sqrt(((na - 1) * va + (nb - 1) * vb) / (na + nb - 2))
    if pooled == 0:
        return float("inf") if statistics.mean(a) != statistics.mean(b) else 0.0
    return (statistics.mean(a) - statistics.mean(b)) / pooled


def magnitude(d: float) -> str:
    d = abs(d)
    if math.isnan(d):
        return "n/a"
    if d < 0.2:
        return "negligible"
    if d < 0.5:
        return "small"
    if d < 0.8:
        return "medium"
    return "large"


def welch_ci(a: list[float], b: list[float], confidence: float = 0.95) -> tuple[float, float]:
    """95% confidence interval for the difference of means, Welch style: the
    range the true saving plausibly lies in, which is what a reader should be
    told rather than a single number."""
    na, nb = len(a), len(b)
    ma, mb = statistics.mean(a), statistics.mean(b)
    va, vb = statistics.variance(a), statistics.variance(b)

    se = math.sqrt(va / na + vb / nb)
    if se == 0:
        return (ma - mb, ma - mb)

    # Welch–Satterthwaite degrees of freedom.
    df = (va / na + vb / nb) ** 2 / ((va / na) ** 2 / (na - 1) + (vb / nb) ** 2 / (nb - 1))
    t = stats.t.ppf(1 - (1 - confidence) / 2, df)
    diff = ma - mb
    return (diff - t * se, diff + t * se)


@dataclass
class Comparison:
    name: str
    question: str
    a: Sample
    b: Sample

    def result(self) -> dict:
        t, p = stats.ttest_ind(self.a.seconds, self.b.seconds, equal_var=False)
        low, high = welch_ci(self.a.seconds, self.b.seconds)
        d = cohens_d(self.a.seconds, self.b.seconds)
        return {
            "comparison": self.name,
            "question": self.question,
            "a": self.a.config,
            "b": self.b.config,
            "a_mean_s": round(self.a.mean, 3),
            "b_mean_s": round(self.b.mean, 3),
            "speedup": round(self.a.mean / self.b.mean, 3) if self.b.mean else None,
            "saved_s": round(self.a.mean - self.b.mean, 3),
            "saved_percent": round(100 * (self.a.mean - self.b.mean) / self.a.mean, 1) if self.a.mean else None,
            "welch_t": round(float(t), 3),
            "p_value": float(p),
            "significant_005": bool(p < 0.05),
            "cohens_d": round(d, 3),
            "effect": magnitude(d),
            "ci95_diff_s": [round(low, 3), round(high, 3)],
            "n_a": self.a.n,
            "n_b": self.b.n,
        }


def amdahl_serial_fraction(sample_by_workers: dict[int, Sample]) -> float | None:
    """Estimates the serial fraction from the measured speedups, by the
    Karp–Flatt metric. It is the honest ceiling on what more workers can buy:
    a suite whose graph is a chain has a serial fraction of 1 and cannot be
    made faster by adding agents."""
    t1 = sample_by_workers.get(1)
    if not t1:
        return None

    fractions = []
    for workers, s in sample_by_workers.items():
        if workers < 2 or not s.mean:
            continue
        speedup = t1.mean / s.mean
        if speedup <= 0:
            continue
        f = (1 / speedup - 1 / workers) / (1 - 1 / workers)
        fractions.append(f)
    return statistics.mean(fractions) if fractions else None


def table(rows: list[list[str]], headers: list[str]) -> str:
    widths = [max(len(str(r[i])) for r in [headers] + rows) for i in range(len(headers))]
    line = lambda cells: "| " + " | ".join(str(c).ljust(widths[i]) for i, c in enumerate(cells)) + " |"
    sep = "|" + "|".join("-" * (w + 2) for w in widths) + "|"
    return "\n".join([line(headers), sep] + [line(r) for r in rows])


def summary_table(samples: dict[str, Sample]) -> str:
    rows = []
    for cfg in sorted(samples):
        s = samples[cfg]
        rows.append([
            CONFIG_LABELS.get(cfg, cfg),
            s.n,
            f"{s.mean:.2f}",
            f"{s.stdev:.3f}",
            f"{s.cv:.1f}%",
            f"{s.mean_worker_seconds:.2f}" if s.worker_seconds else "—",
            f"{statistics.mean(s.passed):.0f}/{statistics.mean(s.total):.0f}" if s.passed else "—",
        ])
    return table(rows, ["configuration", "n", "mean s", "stdev", "CV%", "worker-s", "passed"])


def comparison_table(results: list[dict]) -> str:
    rows = []
    for r in results:
        rows.append([
            r["comparison"],
            f'{r["a_mean_s"]:.2f}',
            f'{r["b_mean_s"]:.2f}',
            f'{r["speedup"]:.2f}x' if r["speedup"] else "—",
            f'{r["saved_percent"]:.1f}%' if r["saved_percent"] is not None else "—",
            f'{r["welch_t"]:.2f}',
            f'{r["p_value"]:.2e}',
            "yes" if r["significant_005"] else "no",
            f'{r["cohens_d"]:.2f} ({r["effect"]})',
            f'[{r["ci95_diff_s"][0]:.2f}, {r["ci95_diff_s"][1]:.2f}]',
        ])
    return table(rows, ["comparison", "A mean", "B mean", "speedup", "saved", "t", "p", "sig", "Cohen's d", "95% CI of saving"])


def scaling_rows(samples: dict[str, Sample]) -> tuple[dict[int, Sample], list[list[str]]]:
    by_workers = {}
    for cfg, s in samples.items():
        if cfg.startswith("s") and cfg[1:].isdigit():
            by_workers[int(cfg[1:])] = s

    rows = []
    base = by_workers.get(1)
    for w in sorted(by_workers):
        s = by_workers[w]
        speedup = base.mean / s.mean if base and s.mean else float("nan")
        rows.append([
            w,
            f"{s.mean:.2f}",
            f"{s.cv:.1f}%",
            f"{speedup:.2f}x",
            f"{speedup / w:.2f}",  # efficiency
            f"{s.mean_worker_seconds:.2f}" if s.worker_seconds else "—",
        ])
    return by_workers, rows


def plot(samples: dict[str, Sample], by_workers: dict[int, Sample], serial: float | None, out_dir: str) -> list[str]:
    import matplotlib

    matplotlib.use("Agg")  # no display in CI
    import matplotlib.pyplot as plt

    written = []

    # ── figure 1: what each configuration costs ──────────────────────────
    order = [c for c in ["e1", "e1p", "e2", "e3"] if c in samples]
    if order:
        fig, ax = plt.subplots(figsize=(8, 4.5))
        means = [samples[c].mean for c in order]
        errs = [1.96 * samples[c].stdev / math.sqrt(samples[c].n) for c in order]
        colours = ["#9ca3af", "#6b7280", "#2563eb", "#16a34a"]

        bars = ax.bar([CONFIG_LABELS[c] for c in order], means, yerr=errs, capsize=5,
                      color=colours[: len(order)])
        for b, m in zip(bars, means):
            ax.text(b.get_x() + b.get_width() / 2, b.get_height(), f"{m:.2f}s",
                    ha="center", va="bottom", fontsize=9)

        ax.set_ylabel("wall clock, seconds")
        ax.set_title("Run time by configuration (mean of measured runs, 95% CI)")
        ax.spines[["top", "right"]].set_visible(False)
        plt.xticks(rotation=15, ha="right")
        plt.tight_layout()
        path = os.path.join(out_dir, "fig1-configurations.png")
        fig.savefig(path, dpi=150)
        plt.close(fig)
        written.append(path)

    # ── figure 2: speedup against Amdahl's law ───────────────────────────
    if len(by_workers) > 1 and 1 in by_workers:
        fig, ax = plt.subplots(figsize=(7, 4.5))
        ws = sorted(by_workers)
        measured = [by_workers[1].mean / by_workers[w].mean for w in ws]

        ax.plot(ws, ws, "--", color="#9ca3af", label="linear (ideal)")
        ax.plot(ws, measured, "o-", color="#16a34a", label="measured")

        if serial is not None and 0 <= serial <= 1:
            amdahl = [1 / (serial + (1 - serial) / w) for w in ws]
            ax.plot(ws, amdahl, ":", color="#2563eb",
                    label=f"Amdahl, serial fraction {serial:.2f}")

        ax.set_xlabel("agents")
        ax.set_ylabel("speedup vs 1 agent")
        ax.set_title("Scaling of the orchestrated suite")
        ax.set_xticks(ws)
        ax.legend()
        ax.spines[["top", "right"]].set_visible(False)
        plt.tight_layout()
        path = os.path.join(out_dir, "fig2-scaling.png")
        fig.savefig(path, dpi=150)
        plt.close(fig)
        written.append(path)

    # ── figure 3: the spread of every run ────────────────────────────────
    spread = [c for c in ["e1", "e1p", "e2", "e3"] if c in samples]
    if spread:
        fig, ax = plt.subplots(figsize=(8, 4.5))
        # tick_labels, not labels: matplotlib 3.9 renamed it and removed the old
        # spelling in 3.11.
        ax.boxplot([samples[c].seconds for c in spread],
                   tick_labels=[CONFIG_LABELS[c] for c in spread], showmeans=True)
        ax.set_ylabel("wall clock, seconds")
        ax.set_title("Spread across runs (warm-up dropped)")
        ax.spines[["top", "right"]].set_visible(False)
        plt.xticks(rotation=15, ha="right")
        plt.tight_layout()
        path = os.path.join(out_dir, "fig3-spread.png")
        fig.savefig(path, dpi=150)
        plt.close(fig)
        written.append(path)

    return written


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("results", help="directory of run files written by run.sh")
    ap.add_argument("-o", "--out", help="where to write tables and figures (default: the results directory)")
    ap.add_argument("--no-plots", action="store_true", help="skip the figures")
    args = ap.parse_args()

    out_dir = args.out or args.results
    os.makedirs(out_dir, exist_ok=True)

    samples = load(args.results)
    if not samples:
        return f"no run files in {args.results}; run experiments/run.sh first"

    thin = [s.config for s in samples.values() if s.n < 3]
    if thin:
        print(f"warning: too few runs to say anything about: {', '.join(thin)}\n", file=sys.stderr)

    comparisons = []
    if "e1" in samples and "e2" in samples:
        comparisons.append(Comparison("E1 → E2", "what context sharing alone saves", samples["e1"], samples["e2"]))
    if "e1" in samples and "e3" in samples:
        comparisons.append(Comparison("E1 → E3", "the end-to-end claim: sharing plus parallelism", samples["e1"], samples["e3"]))
    if "e1p" in samples and "e3" in samples:
        comparisons.append(Comparison("E1p → E3", "orchestration at equal parallelism", samples["e1p"], samples["e3"]))
    if "e2" in samples and "e3" in samples:
        comparisons.append(Comparison("E2 → E3", "what dynamic parallelism adds", samples["e2"], samples["e3"]))
    if "e4b" in samples and "e4" in samples:
        comparisons.append(Comparison("E4b → E4", "what fail-fast saves when a blocker breaks", samples["e4b"], samples["e4"]))

    results = [c.result() for c in comparisons]
    by_workers, sc_rows = scaling_rows(samples)
    serial = amdahl_serial_fraction(by_workers)

    lines = ["# Experiment results", ""]
    lines += [f"Measured runs per configuration: {min(s.n for s in samples.values())}–{max(s.n for s in samples.values())} "
              f"(warm-up run dropped).", ""]
    lines += ["## Configurations", "", summary_table(samples), ""]

    if results:
        lines += ["## Comparisons", "",
                  "Welch's t-test (no equal-variance assumption), Cohen's d for effect size, "
                  "and a 95% confidence interval for the saving.", "",
                  comparison_table(results), ""]
        for r in results:
            lines.append(f"- **{r['comparison']}** — {r['question']}: "
                         f"{r['a_mean_s']:.2f}s → {r['b_mean_s']:.2f}s, "
                         f"{r['saved_percent']:.1f}% saved, p = {r['p_value']:.2e}, d = {r['cohens_d']:.2f} ({r['effect']}).")
        lines.append("")

    if sc_rows:
        lines += ["## Scaling", "",
                  table(sc_rows, ["agents", "mean s", "CV%", "speedup", "efficiency η", "worker-s"]), ""]
        if serial is not None:
            lines.append(f"Serial fraction estimated from the measured speedups (Karp–Flatt): **{serial:.3f}**. "
                         f"By Amdahl's law that caps the speedup at {1/serial:.2f}x however many agents are added."
                         if serial > 0 else
                         "The measured speedups imply no serial bottleneck in this range.")
            lines.append("")

    figures = [] if args.no_plots else plot(samples, by_workers, serial, out_dir)
    if figures:
        lines += ["## Figures", ""] + [f"- `{os.path.basename(p)}`" for p in figures] + [""]

    report = "\n".join(lines)
    md_path = os.path.join(out_dir, "results.md")
    with open(md_path, "w") as f:
        f.write(report)

    json_path = os.path.join(out_dir, "results.json")
    with open(json_path, "w") as f:
        json.dump({
            "configurations": {
                c: {
                    "n": s.n, "mean_s": round(s.mean, 4), "stdev_s": round(s.stdev, 4),
                    "cv_percent": round(s.cv, 2), "seconds": s.seconds,
                    "mean_worker_seconds": round(s.mean_worker_seconds, 4) if s.worker_seconds else None,
                }
                for c, s in sorted(samples.items())
            },
            "comparisons": results,
            "scaling": {str(w): round(by_workers[1].mean / by_workers[w].mean, 4) for w in sorted(by_workers)} if by_workers else {},
            "serial_fraction_karp_flatt": round(serial, 4) if serial is not None else None,
        }, f, indent=2)

    print(report)
    print(f"\nwrote {md_path}, {json_path}" + (f" and {len(figures)} figure(s)" if figures else ""), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
