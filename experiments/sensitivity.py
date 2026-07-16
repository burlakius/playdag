#!/usr/bin/env python3
"""Finds the point at which orchestration starts to pay.

    python3 experiments/sensitivity.py <results-dir> [-o <output-dir>]

Reads the runs written by sensitivity.sh — the baseline and the orchestrated
suite measured against the same application at several login costs — and reports
the crossing point: the cost of the repeated operation above which sharing it
wins.
"""

import argparse
import glob
import json
import os
import statistics
import sys

try:
    import numpy as np
except ImportError:
    sys.exit("this needs numpy: pip install scipy numpy matplotlib")

WARMUP = 1


def load(results_dir: str) -> dict[int, dict[str, list[float]]]:
    """Returns login-cost → {"baseline": [...], "playdag": [...]}."""
    by_delay: dict[int, dict[str, list[float]]] = {}

    for path in sorted(glob.glob(os.path.join(results_dir, "*_run_*.json"))):
        with open(path) as f:
            rec = json.load(f)
        if rec["run"] <= WARMUP:
            continue

        kind = rec["config"].split("-")[0]
        delay = rec["login_ms"]
        by_delay.setdefault(delay, {}).setdefault(kind, []).append(rec["wall_clock_seconds"])

    return by_delay


def crossing(delays: list[int], diffs: list[float]) -> float | None:
    """Where the difference (baseline − playdag) crosses zero, by linear
    interpolation between the two measured points that straddle it. Below this
    login cost the orchestration costs more than it saves."""
    for i in range(len(delays) - 1):
        a, b = diffs[i], diffs[i + 1]
        if a <= 0 <= b or b <= 0 <= a:
            if b == a:
                continue
            t = -a / (b - a)
            return delays[i] + t * (delays[i + 1] - delays[i])
    return None


def table(rows, headers) -> str:
    widths = [max(len(str(r[i])) for r in [headers] + rows) for i in range(len(headers))]
    line = lambda cells: "| " + " | ".join(str(c).ljust(widths[i]) for i, c in enumerate(cells)) + " |"
    sep = "|" + "|".join("-" * (w + 2) for w in widths) + "|"
    return "\n".join([line(headers), sep] + [line(r) for r in rows])


def plot(delays, baseline, playdag, cross, out_dir) -> str:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    fig, (ax, ax2) = plt.subplots(1, 2, figsize=(11, 4.5))

    ax.plot(delays, baseline, "o-", color="#6b7280", label="baseline: every test signs in")
    ax.plot(delays, playdag, "o-", color="#16a34a", label="playdag: one sign-in, shared")
    ax.set_xlabel("cost of signing in, ms")
    ax.set_ylabel("wall clock, seconds")
    ax.set_title("What the suite costs as the repeated operation gets dearer")
    ax.legend()
    ax.spines[["top", "right"]].set_visible(False)

    saved = [b - p for b, p in zip(baseline, playdag)]
    colours = ["#dc2626" if s < 0 else "#16a34a" for s in saved]
    ax2.bar([str(d) for d in delays], saved, color=colours)
    ax2.axhline(0, color="#111827", linewidth=0.8)
    if cross is not None:
        ax2.set_title(f"Saving (baseline − playdag); breaks even at ~{cross:.0f}ms")
    else:
        ax2.set_title("Saving (baseline − playdag)")
    ax2.set_xlabel("cost of signing in, ms")
    ax2.set_ylabel("seconds saved")
    ax2.spines[["top", "right"]].set_visible(False)

    plt.tight_layout()
    path = os.path.join(out_dir, "fig4-breakeven.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    return path


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("results")
    ap.add_argument("-o", "--out")
    ap.add_argument("--no-plots", action="store_true")
    args = ap.parse_args()

    out_dir = args.out or args.results
    os.makedirs(out_dir, exist_ok=True)

    by_delay = load(args.results)
    if not by_delay:
        return f"no run files in {args.results}; run experiments/sensitivity.sh first"

    delays = sorted(by_delay)
    rows, baseline_means, playdag_means = [], [], []

    for d in delays:
        b = by_delay[d].get("baseline", [])
        p = by_delay[d].get("playdag", [])
        if not b or not p:
            continue
        mb, mp = statistics.mean(b), statistics.mean(p)
        baseline_means.append(mb)
        playdag_means.append(mp)
        rows.append([
            d,
            f"{mb:.2f}",
            f"{mp:.2f}",
            f"{mb - mp:+.2f}",
            f"{mb / mp:.2f}x",
            "playdag" if mp < mb else "baseline",
        ])

    diffs = [b - p for b, p in zip(baseline_means, playdag_means)]
    cross = crossing(delays, diffs)

    lines = [
        "# Sensitivity: when does orchestration pay?",
        "",
        "Sharing a session saves what establishing it costs, once for every test that",
        "no longer does it. Running each node in its own framework process costs a",
        "fixed startup, every node. Which wins is therefore not a property of the",
        "method but of the suite, and it turns on one number: how expensive the",
        "repeated operation is. Here that number is varied directly.",
        "",
        table(rows, ["login ms", "baseline s", "playdag s", "saved s", "speedup", "faster"]),
        "",
    ]

    if cross is not None:
        lines += [
            f"**Break-even: a sign-in costing about {cross:.0f} ms.** Below that the "
            "per-node process startup costs more than the repeated sign-in it removes, "
            "and the baseline wins; above it, the saving grows with every test that no "
            "longer signs in.",
            "",
        ]
    else:
        winner = "playdag" if diffs and diffs[0] > 0 else "the baseline"
        lines += [f"No crossing in the measured range: {winner} is ahead throughout.", ""]

    figure = None if args.no_plots else plot(delays, baseline_means, playdag_means, cross, out_dir)
    if figure:
        lines += ["## Figure", "", f"- `{os.path.basename(figure)}`", ""]

    report = "\n".join(lines)
    with open(os.path.join(out_dir, "sensitivity.md"), "w") as f:
        f.write(report)
    with open(os.path.join(out_dir, "sensitivity.json"), "w") as f:
        json.dump({
            "login_ms": delays,
            "baseline_mean_s": [round(v, 4) for v in baseline_means],
            "playdag_mean_s": [round(v, 4) for v in playdag_means],
            "saved_s": [round(v, 4) for v in diffs],
            "breakeven_login_ms": round(cross, 1) if cross is not None else None,
        }, f, indent=2)

    print(report)
    return 0


if __name__ == "__main__":
    sys.exit(main())
