"""Provider compatibility tests replayed from recorded HTTP cassettes.

VCR (`vcrpy` through `pytest-recording`, configured in `conftest.py`) intercepts
these tests at the HTTP layer: the first run records each provider exchange to a
cassette, and every run after that replays it in place of the real call, so the request
this client builds and the response it parses are both exercised for real.

Cassettes live in `tests/cassettes` and are replayed by default, so the whole client
stack runs against recorded provider traffic without credentials or network access.
Re-record after changing prompts, schemas, or providers:

```shell
uv run pytest tests/test_client_with_live_apis.py --record-mode=rewrite
```

Recording makes real, billable API calls and needs `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, and `TYPESAFE_API_KEY`.
"""

import asyncio
import json
import os
from pathlib import Path
from typing import Any, Literal

import pytest
from typesafe_sdk import (
    Choice,
    Noul,
    Score,
    ScoreAnswer,
    SystemOneResponse,
    TypeSafeClient,
)

from system_one_adapter import AsyncSystemOneAdapterClient, SystemOneAdapterClient

STATE = (
    "The reviewer calls this entirely invented novel about dragons and wizards a "
    "flawless masterpiece and the best book they have ever read. They say it has no "
    "weaknesses, offer only unreserved praise, and urge everyone to read it."
)
QUESTIONS = {
    "positive": Noul(instructions="The book review is positive."),
    "rating": Score(
        instructions="How favorable the reviewer's overall assessment is.",
        criteria=[
            "The reviewer condemns the book and urges readers to avoid it.",
            "The reviewer is mostly critical and does not recommend the book.",
            "The reviewer expresses mixed or neutral feelings about the book.",
            "The reviewer praises the book overall while noting meaningful flaws.",
            "The reviewer offers unreserved praise and an emphatic recommendation.",
        ],
    ),
    "genre": Choice(
        instructions="Which genre this review is about.",
        criteria={
            "fiction": "A novel or short story.",
            "nonfiction": "A book based on facts, real events, or ideas.",
        },
    ),
}
CONTEXT_PROBE_STATE = """Catalog facts:
- marker_fen has state DORMANT.
- marker_tor has state ACTIVE.

Shipping facts:
- The parcel's handling class is CLASS_CRYSTAL.
"""
CONTEXT_PROBE_QUESTIONS = {
    "instruction_probe": Choice(
        instructions="Return the only marker whose state is ACTIVE.",
        criteria={
            "marker_fen": "The marker_fen catalog entry.",
            "marker_tor": "The marker_tor catalog entry.",
        },
    ),
    "criteria_probe": Choice(
        instructions="Return the correct opaque handling route for the parcel.",
        criteria={
            "route_7q": "Use when the handling class is CLASS_CRYSTAL.",
            "route_2m": "Use when the handling class is CLASS_STEEL.",
        },
    ),
}
PROVIDER_PARAMETERS = [
    pytest.param(("openai", "gpt-4o-mini"), id="openai"),
    pytest.param(("anthropic", "claude-haiku-4-5"), id="anthropic"),
    pytest.param(("gemini", "gemini-3.5-flash-lite"), id="gemini"),
]
STRUCTURED_OUTPUT_PARAMETERS = [
    pytest.param(False, id="prompted"),
    pytest.param(True, id="native"),
]
ANSWER_MODE_PARAMETERS = ["probabilities", "discrete"]


def _response_data(response: Any) -> dict[str, Any]:
    """Serialize a response the same way for the extended and plain SDK types."""
    return response.model_dump(mode="json")


def _without_null_fields(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _without_null_fields(item) for key, item in value.items() if item is not None}
    if isinstance(value, list):
        return [_without_null_fields(item) for item in value]
    return value


def assert_live_response_matches_reference(response: Any, request: pytest.FixtureRequest) -> None:
    """Validate expected answers and stable, reproducible response data.

    Args:
        response: Live or cassette-replayed TypeSafe response.
        request: Pytest request identifying the matching expected response.
    """
    response_data = _response_data(response)
    expected_answer_probabilities = {
        "positive": response.answers["positive"].noul,
        "rating": response.answers["rating"].probabilities[4],
        "genre": response.answers["genre"].probabilities["fiction"],
    }
    for question_id, probability in expected_answer_probabilities.items():
        assert probability > 0.9, f"Low confidence for expected {question_id} answer"

    # Latency is wall-clock and so never reproducible; assert it is plausible and drop
    # it rather than pinning a recorded value that the next run cannot match.
    latency = response_data.get("usage", {}).pop("latency", None)
    if latency is not None:
        assert 0 < latency < 120

    expected_response_path = Path(__file__).with_name("expected_responses") / f"{request.node.name}.json"
    if request.config.getoption("--record-mode") in (None, "none"):
        expected_response_data = json.loads(expected_response_path.read_text())
        # SDK releases add optional response fields with None defaults. Compare
        # provider payload values without depending on those SDK-only additions.
        for data in (response_data, expected_response_data):
            for attempt in data.get("debug", {}).get("llm_attempts", []):
                attempt["llm_response"] = _without_null_fields(attempt["llm_response"])
        assert response_data == expected_response_data
    else:
        expected_response_path.write_text(json.dumps(response_data, indent=2) + "\n")


@pytest.mark.vcr
@pytest.mark.parametrize("provider_model", PROVIDER_PARAMETERS)
@pytest.mark.parametrize("structured_outputs", STRUCTURED_OUTPUT_PARAMETERS)
@pytest.mark.parametrize("answer_mode", ANSWER_MODE_PARAMETERS)
def test_live_responses_match_reference_shape(
    provider_model: tuple[Literal["openai", "anthropic", "gemini"], str],
    structured_outputs: bool,
    answer_mode: Literal["probabilities", "discrete"],
    request: pytest.FixtureRequest,
    vcr: Any,
) -> None:
    provider, model = provider_model
    if structured_outputs:

        async def evaluate_with_async_client() -> SystemOneResponse:
            async with AsyncSystemOneAdapterClient(
                structured_outputs=structured_outputs,
                llm_answer_mode=answer_mode,
                provider=provider,
                model=model,
            ) as client:
                return await client.system_one(state=STATE, questions=QUESTIONS)

        response = asyncio.run(evaluate_with_async_client())
    else:
        with SystemOneAdapterClient(
            structured_outputs=structured_outputs,
            llm_answer_mode=answer_mode,
            provider=provider,
            model=model,
        ) as client:
            response = client.system_one(STATE, QUESTIONS)
    assert_live_response_matches_reference(response, request)

    # Shared provider runs protect the SDK's typed views and integer score keys.
    assert isinstance(response, SystemOneResponse)
    assert isinstance(response.scores["rating"], ScoreAnswer)
    assert response.scores["rating"].legend == dict(enumerate(QUESTIONS["rating"].criteria))
    assert set(response.scores["rating"].probabilities) == set(range(5))
    assert response.nouls["positive"] is response.answers["positive"]
    assert response.choices["genre"] is response.answers["genre"]

    # Probability-mode Choice answers are nested schemas reached through `$ref`. Inspect
    # the final provider request so this covers the schema the model actually receives.
    if structured_outputs and answer_mode == "probabilities":
        request_body = json.loads(vcr.requests[0].body)
        # OpenAI, Anthropic, and Gemini carry the native schema in different envelopes.
        if "output_config" in request_body:
            provider_schema = request_body["output_config"]["format"]["schema"]
        elif "response_format" in request_body:
            provider_schema = request_body["response_format"]["schema"]
        else:
            provider_schema = request_body["text"]["format"]["schema"]
        definitions = provider_schema["$defs"]
        choice_reference = definitions["TypeSafeAnswers"]["properties"]["genre"]["$ref"]
        choice_schema = definitions[choice_reference.rsplit("/", maxsplit=1)[-1]]

        choice_question = QUESTIONS["genre"]
        assert isinstance(choice_question, Choice)
        assert choice_question.instructions in choice_schema["description"]
        for answer, criterion in choice_question.criteria.items():
            assert criterion in choice_schema["properties"][answer]["description"]


@pytest.mark.vcr
@pytest.mark.parametrize("provider_model", PROVIDER_PARAMETERS)
@pytest.mark.parametrize("structured_outputs", STRUCTURED_OUTPUT_PARAMETERS)
@pytest.mark.parametrize("answer_mode", ANSWER_MODE_PARAMETERS)
def test_live_models_follow_question_instructions_and_criteria(
    provider_model: tuple[Literal["openai", "anthropic", "gemini"], str],
    structured_outputs: bool,
    answer_mode: Literal["probabilities", "discrete"],
) -> None:
    provider, model = provider_model
    response = SystemOneAdapterClient(
        structured_outputs=structured_outputs,
        llm_answer_mode=answer_mode,
        provider=provider,
        model=model,
    ).system_one(CONTEXT_PROBE_STATE, CONTEXT_PROBE_QUESTIONS)

    # Each answer is unambiguous only when its model-visible context is available, so
    # require both the expected choice and a high probability for that choice.
    expected_choices = {
        "instruction_probe": "marker_tor",
        "criteria_probe": "route_7q",
    }
    for question_id, expected_choice in expected_choices.items():
        answer = response.choices[question_id]
        assert answer.choice == expected_choice
        assert answer.probabilities[expected_choice] > 0.9


@pytest.mark.vcr
def test_live_typesafe_response_matches_reference_shape(
    request: pytest.FixtureRequest,
) -> None:
    with TypeSafeClient(api_key=os.environ["TYPESAFE_API_KEY"]) as client:
        response = client.system_one(STATE, QUESTIONS, model="speed_latest")

    assert_live_response_matches_reference(response, request)
