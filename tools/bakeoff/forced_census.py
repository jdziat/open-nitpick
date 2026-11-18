"""Count forced submissions in retained deep-review stderr logs.

It makes no model call. A forced submission is the extra request an agent
makes when it stops without submitting findings; a length cut is one of those
requests running out of output budget, which steps the ladder down.

    python3 tools/bakeoff/forced_census.py --runs /tmp/bakeoff/runs --output out.json
"""

import argparse
import hashlib
import json
import re
from pathlib import Path

FIELD = re.compile(r'(\w+)=("[^"]*"|\S+)')
FORCED_CAP = 65536


def census(lines):
    agents = []
    cuts = 0
    failed = 0
    fallbacks = 0
    for line in lines:
        if "deep-review agent finished" in line:
            agents.append({k: v.strip('"') for k, v in FIELD.findall(line)})
        elif "a forced submission made no usable tool call" in line and "finish=length" in line:
            cuts += 1
        elif 'msg="a deep-review agent failed; keeping the other agents\' findings"' in line:
            failed += 1
        elif 'msg="every deep-review agent failed; reviewing the batch from the diff alone"' in line:
            fallbacks += 1
    forced = [a for a in agents if a.get("forced") == "true"]
    completion = sum(int(a.get("completion_tokens", 0)) for a in agents)
    return {
        "agents": len(agents),
        "agents_lost": failed,
        "whole_batch_diff_only_fallbacks": fallbacks,
        "forced": len(forced),
        "forced_rate": round(len(forced) / len(agents), 3) if agents else None,
        "length_cut_forced_calls": cuts,
        "agents_with_ladder_step_down": sum(
            1 for a in agents if int(a["reasoning_low"]) + int(a["reasoning_off"]) > 0
        ),
        "forced_with_zero_findings": sum(1 for a in forced if a["findings"] == "0"),
        "completion_tokens_total": completion,
        "cut_token_upper_bound": cuts * FORCED_CAP,
        "cut_token_upper_bound_share": round(cuts * FORCED_CAP / completion, 3) if completion else None,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--runs", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    lines = []
    sources = []
    for path in sorted(args.runs.glob("*/*/stderr.txt")):
        data = path.read_bytes()
        sources.append({"path": str(path.relative_to(args.runs)), "sha256": hashlib.sha256(data).hexdigest()})
        lines.extend(data.decode(errors="replace").splitlines())
    if not sources:
        parser.error("no stderr logs found under the runs directory")
    result = census(lines)
    result["sources"] = sources
    args.output.write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
