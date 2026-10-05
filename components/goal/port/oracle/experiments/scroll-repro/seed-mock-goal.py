#!/usr/bin/env python3
"""Seed a MOCK RUNNING goal (status active, 12 tasks, budget, contract) into a
throwaway pi working directory, so the full-stack repro exercises a live
goal: liveDisplayGoal ticks activeSeconds on every render, the expanded
dashboard is tall, and the debug mock-audit animation provides continuous
goal-state churn. Run BEFORE zellij-driver.py; the dir is throwaway.
"""
import json
import os
import shutil
import sys
import time

cwd = sys.argv[1] if len(sys.argv) > 1 else "/tmp/pi-mock-repro"
goals = os.path.join(cwd, ".pi", "goals")
shutil.rmtree(cwd, ignore_errors=True)
os.makedirs(goals, exist_ok=True)

now = time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime())


def task(tid, title, status, subtasks=None, vc=None):
    t = {"id": tid, "title": title, "status": status}
    if status == "complete":
        t["completedAt"] = now
    if vc:
        t["verificationContract"] = vc
    if subtasks:
        t["subtasks"] = subtasks
    return t


tasks = [
    task("mock-1", "Analyze the failing scroll behavior with the real terminal stack", "complete",
         vc="Reproduce with real pi in a real multiplexer; capture the pane scroll indicator over time"),
    task("mock-2", "Instrument the widget render path to log rendered height per render", "complete",
         subtasks=[
             task("mock-2a", "Add a debug counter to the component render", "complete"),
             task("mock-2b", "Log regime key + latched height", "complete"),
         ]),
    task("mock-3", "Verify the latch holds the rendered height constant while the goal runs", "pending",
         vc="Widget rendered height constant across usage ticks, audit phases, and ledger events"),
    task("mock-4", "Check the fits case renders a constant height too", "pending"),
    task("mock-5", "Re-run the emulator repro with the running-goal fixture", "pending"),
    task("mock-6", "Validate in zellij: scroll up into the chat and hold across goal updates", "pending",
         vc="No snap-back while reading the chat at any terminal height where the frame overflows"),
    task("mock-7", "Write the stable-height spec update", "complete"),
    task("mock-8", "Close the milestone log", "pending"),
    task("mock-9", "Ship the release", "pending"),
    task("mock-10", "Follow up on user feedback for the scroll experience", "pending"),
]

goal = {
    "version": 3,
    "id": "mock-active-run-001",
    "objective": "Mock a running goal for the full-stack scroll reproduction. This goal exercises the live path: status active (live elapsed usage on every render), a tall expanded dashboard (12 tasks, subtasks, verification contracts, token budget), and continuous goal-state churn via the debug audit animation — while the terminal is smaller than the widget and the user is scrolled up.",
    "status": "active",
    "autoContinue": True,
    "usage": {"tokensUsed": 48213, "activeSeconds": 1427},
    "sisyphus": False,
    "skipAuditor": False,
    "revision": 3,
    "createdAt": now,
    "updatedAt": now,
    "verificationContract": "Mock goal: the full-stack repro shows the pane scroll indicator (SCROLL: 0/N) constant across goal-state changes while the terminal is smaller than the widget.",
    "tokenBudget": 200000,
    "taskList": {"tasks": tasks, "blockCompletion": False, "proposedAt": now},
    "activePath": ".pi/goals/active_goal_mock_run_001.md",
}

meta = json.dumps(goal, indent=2)
lines = ["# Goal Prompt", "", goal["objective"].strip(), "", "## Progress", "",
         f"- Status: {goal['status']}", "",
         "## Tasks", "", "<!-- blockCompletion: false -->"]
for t in tasks:
    mark = "x" if t["status"] == "complete" else " "
    lines.append(f"- [{mark}] {t['id']}: {t['title']}")
lines.append("")
body = "\n".join(lines)

with open(os.path.join(goals, "active_goal_mock_run_001.md"), "w") as f:
    f.write(meta + "\n\n" + body + "\n")

print(f"seeded mock ACTIVE goal -> {os.path.join(goals, 'active_goal_mock_run_001.md')}")
print(f"tasks: {len(tasks)} (4 complete, {len(tasks) - 4} pending)  status: active  budget: 200000")
