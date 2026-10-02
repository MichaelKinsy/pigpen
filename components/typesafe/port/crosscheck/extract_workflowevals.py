"""Extracts real request inputs from WorkflowEvals (Apache-2.0, pinned commit) for the cross-check.

Run from a WorkflowEvals checkout with typesafe-sdk, pydantic, huggingface-hub and pyarrow installed:

    python extract_workflowevals.py <cases-per-workflow> [full.json] > workflowevals.json

stdout gets the committed form: real questions (they come from the Apache-2.0 code) with a small
synthetic state, so no dataset content is stored. The optional full.json gets the real states
from the datasets, for a local run of the cross-check (WORKFLOWEVALS_FULL=full.json).

Each workflow's own eval adapter runs on real dataset cases (downloaded from Hugging Face) with a
recording session in place of the model: every system_one(state, questions) call it makes is captured
with the wire form of the questions and with the exact request body the Python SDK would send
(typesafe_sdk's own prepare step, no network involved). Synthetic answers keep the decision graph moving.
No TypeSafe API is called and no credential is used.
"""
import json
import random
import sys
from types import SimpleNamespace

from typesafe_sdk import SystemOneResponse
from typesafe_sdk._core.json import serialize
from typesafe_sdk._core.questions import normalize_questions

from core.dataset import load_dataset
from core.workflow import WORKFLOWS, load


def synthetic(questions, rng, high):
    answers = {}
    for name, q in questions.items():
        kind = q.type
        if kind == "noul":
            answers[name] = {"type": "noul", "noul": 0.9 if high else rng.random()}
        elif kind == "choice":
            labels = list(q.criteria)
            probs = {l: 0.01 for l in labels}
            probs[labels[0] if high else rng.choice(labels)] = 0.9
            answers[name] = {"type": "choice", "choice": max(probs, key=probs.get), "confidence": 0.8, "probabilities": probs}
        else:
            n = len(q.criteria)
            lvl = n - 1 if high else rng.randrange(n)
            probs = {i: (0.9 if i == lvl else 0.1 / max(n - 1, 1)) for i in range(n)}
            answers[name] = {"type": "score", "score": float(lvl), "confidence": 0.8, "probabilities": probs, "legend": {i: (c if isinstance(c, str) else json.dumps(c)) for i, c in enumerate(q.criteria)}}
    return SystemOneResponse.model_validate(
        {"model": "m", "answers": answers, "usage": {"input_tokens": 1, "output_tokens": 1}}
    )


class Recorder:
    def __init__(self, sink, workflow, case_id, rng, high, policy_ids):
        self.sink, self.workflow, self.case_id, self.rng, self.high = sink, workflow, case_id, rng, high
        self.node = ""
        self.policy_ids = policy_ids
        self.config = SimpleNamespace(model="m", thinking="off")

    def system_one(self, _model, state, questions):
        wire = normalize_questions(questions)
        body = {"state": state, "model": "m", "questions": wire}
        self.sink.append(
            {
                "workflow": self.workflow,
                "case_id": self.case_id,
                "node": self.node,
                "state": state,
                "questions": json.loads(serialize(wire)),
                "python_body": serialize(body).decode("utf-8"),
            }
        )
        return synthetic(questions, self.rng, self.high)


def main():
    per = int(sys.argv[1])
    out, seen = [], set()
    for name in WORKFLOWS:
        workflow = load(name)
        for case_id in [c.case_id for c in workflow.bundle.cases][:per]:
            for high in (True, False):
                calls = []
                try:
                    workflow.execute(case_id, Recorder(calls, name, case_id, random.Random(7), high, [p.policy_id for p in workflow.bundle.policies]))
                except Exception as exc:  # a synthetic answer may not fit a workflow's invariants
                    print(f"{name}/{case_id} high={high}: {type(exc).__name__}: {exc}", file=sys.stderr)
                for c in calls:
                    key = (c["workflow"], c["case_id"], c["python_body"])
                    if key not in seen:
                        seen.add(key)
                        out.append(c)
    # Keep the call with the smallest state for every distinct question set (real states can be large).
    best = {}
    for c in out:
        key = (c["workflow"], json.dumps(c["questions"], sort_keys=True))
        if key not in best or len(c["python_body"]) < len(best[key]["python_body"]):
            best[key] = c
    out = list(best.values())
    if len(sys.argv) > 2:
        with open(sys.argv[2], "w") as f:
            json.dump(out, f, ensure_ascii=False)
    slim = []
    for c in out:
        state = f"<synthetic state of {c['workflow']} node {c['node']}>"
        body = {"state": state, "model": "m", "questions": c["questions"]}
        slim.append({**c, "case_id": "synthetic", "state": state, "python_body": serialize(body).decode("utf-8")})
    out = slim
    json.dump(out, sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")
    print(f"{len(out)} calls from {len(WORKFLOWS)} workflows", file=sys.stderr)


if __name__ == "__main__":
    main()
