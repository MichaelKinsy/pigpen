#!/usr/bin/env python3
"""Generates port/scenarios/*.json for the ask_user_question tool as Pi's RPC mode drives it.

Pi's RPC host reports mode "rpc" and has no custom-component UI, so the tool takes the sequential dialog path
(select and input). A scenario's `ui` list answers those dialogs in order. The model is scripted: each entry of
`turns` is one assistant turn with the given tool calls, followed by a final "done" turn."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)


def opt(label, description="A choice", preview=None):
    o = {"label": label, "description": description}
    if preview is not None:
        o["preview"] = preview
    return o


def q(question, options, header="Choice", multi=None):
    d = {"question": question, "header": header, "options": options}
    if multi is not None:
        d["multiSelect"] = multi
    return d


def ask(*questions):
    return {"name": "ask_user_question", "arguments": {"questions": list(questions)}}


def scenario(name, description, turns, ui=None, message="decide for me", **extra):
    llm = [{"toolCalls": t} for t in turns] + [{"text": "done"}]
    step = {"name": "prompt", "rpc": {"type": "prompt", "message": message}}
    if ui:
        step["ui"] = ui
    s = {"name": name, "description": description, "llm": llm, "steps": [step]}
    s.update(extra)
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


def pick(text):
    return {"value": text}


DB = [opt("Postgres", "Relational"), opt("SQLite", "Embedded"), opt("Redis", "In memory")]

scenario("single-select", "One single-select question: the user picks the second option.",
         [[ask(q("Which database?", DB, "Database"))]], [pick("2. SQLite — Embedded")])

scenario("single-other-typed", "The user picks the appended 'Type something.' row and types an answer.",
         [[ask(q("Which database?", DB))]], [pick("4. Type something."), pick("DuckDB")])

scenario("single-dismissed", "The select dialog is dismissed: the questionnaire is declined.",
         [[ask(q("Which database?", DB))]], [{"cancelled": True}])

scenario("other-input-dismissed", "The user picks 'Type something.' and then dismisses the input.",
         [[ask(q("Which database?", DB))]], [pick("4. Type something."), {"cancelled": True}])

scenario("unlisted-choice", "The select answer matches no option line (a custom client): the questionnaire is declined.",
         [[ask(q("Which database?", DB))]], [pick("Postgres")])

scenario("multi-select-numbers", "Multi-select answered with comma and space separated numbers, with a duplicate and a trailing dot.",
         [[ask(q("Which features?", [opt("Auth"), opt("Cache"), opt("Queue"), opt("Search")], "Features", True))]],
         [pick("3, 1, 3.")])

scenario("multi-select-variants", "Four multi-select questions: empty answer, free text, an out-of-range number, number zero.",
         [[ask(q("Features A?", [opt("A1"), opt("A2")], "A", True),
               q("Features B?", [opt("B1"), opt("B2")], "B", True),
               q("Features C?", [opt("C1"), opt("C2")], "C", True),
               q("Features D?", [opt("D1"), opt("D2")], "D", True))]],
         [pick("   "), pick("something else entirely"), pick("1,9"), pick("0")])

scenario("multi-select-dismissed", "The multi-select input is dismissed.",
         [[ask(q("Which features?", [opt("Auth"), opt("Cache")], "Features", True))]], [{"cancelled": True}])

scenario("mixed-questions", "Single, multi and single again: all answered; the envelope lists each in order.",
         [[ask(q("Which database?", DB, "Database"),
               q("Which features?", [opt("Auth"), opt("Cache")], "Features", True),
               q("Which region?", [opt("eu"), opt("us")], ""))]],
         [pick("1. Postgres — Relational"), pick("1,2"), pick("2. us — A choice")])

scenario("partial-then-dismissed", "The second question is dismissed: the answers so far stay in the details.",
         [[ask(q("Which database?", DB, "Database"), q("Which region?", [opt("eu"), opt("us")], "Region"))]],
         [pick("3. Redis — In memory"), {"cancelled": True}])

LONG = "x" * 700
scenario("previews", "Options with preview text: the dialog title carries the first 600 characters of each; the chosen one is echoed.",
         [[ask(q("Which layout?", [opt("Wide", "Two panes", "+----+----+\n|    |    |\n+----+----+"),
                                     opt("Tall", "One pane", LONG), opt("Plain", "No preview")], "Layout"))]],
         [pick("1. Wide — Two panes")])

scenario("line-terminators", "CRLF and lone CR in the model's text are normalised before the dialog and the envelope.",
         [[ask(q("Line one\r\nline two\rend?", [opt("A\r\nB", "desc\r\nmore"), opt("C", "d")], "H\r\n"))]],
         [pick("1. A\nB — desc\nmore")])

scenario("reserved-and-duplicates", "Rejected before any dialog: reserved labels, duplicate question text, duplicate option labels.",
         [[ask(q("Reserved other?", [opt("Other"), opt("X")]))],
          [ask(q("Reserved row?", [opt("Type something."), opt("X")]))],
          [ask(q("Reserved next?", [opt("Next"), opt("X")]))],
          [ask(q("Same?", [opt("a"), opt("b")]), q("Same?", [opt("a"), opt("b")]))],
          [ask(q("Dup labels?", [opt("a"), opt("a")]))]])

scenario("schema-rejections", "Arguments the schema rejects: no questions, five questions, one option, five options, long label, long header.",
         [[{"name": "ask_user_question", "arguments": {"questions": []}}],
          [ask(*[q("Q%d?" % i, [opt("a"), opt("b")]) for i in range(5)])],
          [ask(q("One?", [opt("a")]))],
          [ask(q("Five?", [opt(c) for c in "abcde"]))],
          [ask(q("Label?", [opt("l" * 61), opt("b")]))],
          [ask(q("Header?", [opt("a"), opt("b")], "h" * 17))],
          [{"name": "ask_user_question", "arguments": {}}]])

scenario("two-calls", "Two questionnaires in one turn run one after the other.",
         [[ask(q("First?", [opt("a"), opt("b")], "First")), ask(q("Second?", [opt("c"), opt("d")], "Second"))]],
         [pick("1. a — A choice"), pick("2. d — A choice")])

scenario("guidance-in-request", "The tool definition and its prompt guidance reach the model unchanged.", [], message="hello")
