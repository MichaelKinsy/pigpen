"""Record golden traces of the Python oracle (system-one-adapter 0.2.1 at e1d4cc9).

Runs every scenario in scenarios/*.json through the unmodified Python adapter with a
scripted fake provider (the oracle's own test seam) and writes golden/<name>.json:
the provider calls (messages, schema, structured), and the resulting answers, usage
counters and diagnostics, or the terminal error.

    cd <system-one-adapter-python checkout at e1d4cc9>
    .venv/bin/python <this file> --scenarios scenarios --golden golden

The Go backend replays the same scenarios (libraries/ownmodel equivalence_test.go) and
must produce the same calls and answers.
"""
import argparse
import json
import pathlib
import sys

from pydantic_core import to_json
from typesafe_sdk import TypeSafeError

from system_one_adapter import SystemOneAdapterClient, RetryPolicy
from system_one_adapter.providers import ProviderResult


class Scripted:
    model_name = "fake-model"

    def __init__(self, steps, usage=(11, 7)):
        self.steps, self.usage, self.calls = list(steps), usage, []

    def request(self, messages, *, schema, structured):
        self.calls.append({
            "messages": [{"role": m.role, "content": m.content} for m in messages],
            "schema": json.loads(to_json(schema)),
            "structured": structured,
        })
        step = self.steps[min(len(self.calls) - 1, len(self.steps) - 1)]
        text = step if isinstance(step, str) else to_json(step).decode()
        return ProviderResult(text=text, input_tokens=self.usage[0], output_tokens=self.usage[1])

    def translate_error(self, error):
        return error if isinstance(error, TypeSafeError) else TypeSafeError(str(error))


def run(scenario):
    provider = Scripted(scenario["responses"])
    client = SystemOneAdapterClient(
        structured_outputs=scenario.get("structured", False),
        llm_answer_mode=scenario.get("mode", "probabilities"),
        normalize_probabilities=scenario.get("normalize", False),
        n_retry_malformed_structure=scenario.get("malformedRetries", 0),
    )
    out = {"name": scenario["name"], "calls": None}
    try:
        response = client.system_one(scenario["state"], scenario["questions"], model=provider)
    except Exception as error:  # the oracle's terminal error
        out["error"] = {"type": type(error).__name__, "message": str(error)[:400]}
        debug = getattr(error, "debug", None)
        if debug is not None:
            out["retryReasons"] = [c for c, _ in debug["retry_reasons"]]
    else:
        dumped = response.model_dump(mode="json")
        out["answers"] = dumped["answers"]
        usage = dumped["usage"]
        usage.pop("latency", None)
        out["usage"] = usage
        debug = dict(response.debug)
        out["retryReasons"] = [c for c, _ in debug["retry_reasons"]]
        out["debug"] = {k: v for k, v in debug.items() if k not in ("llm_attempts", "retry_reasons")}
    out["calls"] = provider.calls
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--scenarios", required=True)
    ap.add_argument("--golden", required=True)
    args = ap.parse_args()
    golden = pathlib.Path(args.golden)
    golden.mkdir(parents=True, exist_ok=True)
    for path in sorted(pathlib.Path(args.scenarios).glob("*.json")):
        scenario = json.loads(path.read_text())
        (golden / path.name).write_text(json.dumps(run(scenario), indent=1, ensure_ascii=False) + "\n")
        print("recorded", path.name)


if __name__ == "__main__":
    sys.exit(main())
