"""Confidence metric tests."""

from collections.abc import Callable

import pytest

from system_one_adapter._utils.confidence_metrics import (
    choice_confidence,
    score_confidence,
)


@pytest.mark.parametrize(
    "confidence_metric,probabilities,expected_confidence",
    [
        (score_confidence, [0.2] * 5, 0.0),
        (score_confidence, [0.04] * 5, 0.0),
        (score_confidence, [0.01, 0.02, 0.07, 0.3, 0.6], 0.55),
        (choice_confidence, [0.5, 0.5], 0.0),
        (choice_confidence, [0.2, 0.2], 0.0),
        (choice_confidence, [0.82, 0.18], 0.64),
        (score_confidence, [1.0], 1.0),
        (choice_confidence, [1.0], 1.0),
    ],
)
def test_confidence_metrics(
    confidence_metric: Callable[[list[float]], float],
    probabilities: list[float],
    expected_confidence: float,
) -> None:
    assert confidence_metric(probabilities) == pytest.approx(expected_confidence)
