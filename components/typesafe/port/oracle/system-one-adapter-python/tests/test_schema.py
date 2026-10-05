"""Provider schemas preserve caller-defined question IDs and choice labels."""

from typing import Any

import pytest
from pydantic import ValidationError
from pydantic_core import to_json

from system_one_adapter import Choice, Noul, Score
from system_one_adapter._schema import (
    Question,
    convert_question_collection_to_validated_api_question_models,
    create_llm_output_model,
    create_raw_output_schema,
)
from system_one_adapter._utils.probability_normalization import AnswerMode

SCHEMA_KEYWORDS = ["title", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"]
FIELD_NAMES = [*SCHEMA_KEYWORDS, "model_dump", "model_config", "_private", "", "with spaces", "answer_0", "probability_0"]


@pytest.mark.parametrize(
    "question",
    [
        {"type": "unknown"},
        {"type": "noul", "instructions": 42},
        {"type": "choice", "criteria": ["yes", "no"]},
        {"type": "score", "criteria": {"0": "Bad.", "1": "Good."}},
    ],
)
def test_invalid_dictionary_questions_are_rejected(question: Any) -> None:
    with pytest.raises(ValidationError):
        convert_question_collection_to_validated_api_question_models({"answer": question})


def test_sdk_question_fields_are_revalidated() -> None:
    question = Noul()
    question.criteria = {"true": 42}  # type: ignore[assignment]

    with pytest.raises(ValidationError):
        convert_question_collection_to_validated_api_question_models({"answer": question})


@pytest.mark.parametrize("answer_mode", ["probabilities", "discrete"])
def test_question_ids_preserve_arbitrary_names(answer_mode: AnswerMode) -> None:
    questions = {key: Noul(instructions=f"Evaluate {key}.") for key in FIELD_NAMES}
    output_model = create_llm_output_model(questions, answer_mode)
    schema = create_raw_output_schema(output_model)
    answers = schema["$defs"]["TypeSafeAnswers"]

    assert schema["properties"]["answers"] == {"$ref": "#/$defs/TypeSafeAnswers"}
    assert "Use these property names verbatim" in answers["description"]
    assert set(answers["properties"]) == set(answers["required"]) == set(questions)
    for key, answer in answers["properties"].items():
        assert questions[key].instructions in answer["description"]
        assert not set(answer) & set(SCHEMA_KEYWORDS)

    payload = {"answers": dict.fromkeys(questions, True if answer_mode == "discrete" else 0.8)}
    assert output_model.model_validate_json(to_json(payload)).model_dump() == payload


def test_probability_labels_preserve_arbitrary_names() -> None:
    criteria = {key: f"The {key} option." for key in FIELD_NAMES}
    output_model = create_llm_output_model({"level": Choice(criteria=criteria)}, "probabilities")
    schema = create_raw_output_schema(output_model)
    probabilities = schema["$defs"]["ProbabilityMap0"]

    assert set(probabilities["properties"]) == set(probabilities["required"]) == set(criteria)
    assert "title" not in probabilities
    for key, probability in probabilities["properties"].items():
        assert probability["description"] == criteria[key]
        assert not set(probability) & set(SCHEMA_KEYWORDS)

    payload = {"answers": {"level": dict.fromkeys(criteria, 1 / len(criteria))}}
    assert output_model.model_validate_json(to_json(payload)).model_dump() == payload

    with pytest.raises(ValidationError):
        output_model.model_validate_json(to_json({"answers": {"level": dict.fromkeys(criteria, 2)}}))


@pytest.mark.parametrize(
    "question,answer_mode,answer",
    [
        (Noul(), "discrete", "true"),
        (Noul(), "discrete", 1),
        (Noul(), "probabilities", "0.5"),
        (Noul(), "probabilities", True),
        (Noul(), "probabilities", -0.1),
        (Noul(), "probabilities", 1.1),
        (Noul(), "probabilities", float("nan")),
        (Score(criteria=["Bad.", "Good."]), "discrete", 1.0),
        (Score(criteria=["Bad.", "Good."]), "discrete", True),
        (Score(criteria=["Bad.", "Good."]), "discrete", 2),
        (Choice(criteria={"yes": None, "no": None}), "discrete", "maybe"),
        (Choice(criteria={"yes": None, "no": None}), "probabilities", {"yes": 0.5}),
        (Choice(criteria={"yes": None, "no": None}), "probabilities", {"yes": 0.5, "no": 0.5, "maybe": 0}),
    ],
)
def test_output_validation_preserves_types_bounds_and_allowed_values(question: Question, answer_mode: AnswerMode, answer: Any) -> None:
    output_model = create_llm_output_model({"answer": question}, answer_mode)
    with pytest.raises(ValidationError):
        output_model.model_validate_json(to_json({"answers": {"answer": answer}}))


@pytest.mark.parametrize(
    "payload",
    [
        {"answers": {"answer": 0.5}, "extra": 1},
        {"answers": {"answer": 0.5, "extra": 1}},
        {"answers": {"answer_0": 0.5}},
    ],
)
def test_output_rejects_extra_fields_and_internal_field_names(payload: dict[str, Any]) -> None:
    output_model = create_llm_output_model({"answer": Noul()}, "probabilities")
    with pytest.raises(ValidationError):
        output_model.model_validate_json(to_json(payload))
