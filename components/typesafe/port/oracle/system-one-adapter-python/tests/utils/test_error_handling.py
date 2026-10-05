"""Error translation and retry tests."""

from collections.abc import Callable
from typing import Any

import anthropic
import httpx
import httpx2
import openai
import pytest
from google.genai._gaos.lib import compat_errors as gemini_sdk
from typesafe_sdk import (
    RetryPolicy,
    TypeSafeAPIConnectionError,
    TypeSafeAPIError,
    TypeSafeAPITimeoutError,
    TypeSafeAuthenticationError,
    TypeSafeBadRequestError,
    TypeSafeError,
    TypeSafeInternalServerError,
    TypeSafePermissionDeniedError,
    TypeSafeRateLimitError,
)

from system_one_adapter._utils.error_handling import RetryReasons, run_with_retries
from system_one_adapter.providers.anthropic import _AnthropicErrors
from system_one_adapter.providers.base import translating
from system_one_adapter.providers.gemini import _GeminiErrors
from system_one_adapter.providers.openai import _OpenAIErrors
from tests.http import json_response

_TRANSLATORS: dict[str, tuple[Callable[[Exception], TypeSafeError], Any]] = {
    "openai": (_OpenAIErrors.translate_error, openai),
    "anthropic": (_AnthropicErrors.translate_error, anthropic),
    "gemini": (_GeminiErrors.translate_error, gemini_sdk),
}


def _status_error(sdk: Any, status: int, body: dict[str, Any]) -> Exception:
    transport = httpx2 if sdk is not gemini_sdk and issubclass(sdk.DefaultHttpxClient, httpx2.Client) else httpx
    request = transport.Request("POST", "https://example.test")
    response = json_response(request, body, status=status)
    return sdk.APIStatusError("error", response=response, body=body)


@pytest.mark.parametrize("provider", ["openai", "anthropic", "gemini"])
@pytest.mark.parametrize(
    "status,expected_error",
    [
        pytest.param(400, TypeSafeBadRequestError, id="bad-request"),
        pytest.param(401, TypeSafeAuthenticationError, id="auth"),
        pytest.param(403, TypeSafePermissionDeniedError, id="permission"),
        pytest.param(429, TypeSafeRateLimitError, id="rate-limit"),
        pytest.param(500, TypeSafeInternalServerError, id="server"),
        pytest.param(418, TypeSafeAPIError, id="other-status"),
    ],
)
def test_status_errors_map_and_preserve_status_and_body(provider: str, status: int, expected_error: type[Exception]) -> None:
    translate, sdk = _TRANSLATORS[provider]
    body = {"error": {"message": "boom"}}

    translated = translate(_status_error(sdk, status, body))

    assert isinstance(translated, expected_error)
    assert isinstance(translated, TypeSafeAPIError)
    assert translated.status == status
    assert translated.body == body


@pytest.mark.parametrize("provider", ["openai", "anthropic", "gemini"])
def test_timeout_and_connection_errors_map(provider: str) -> None:
    translate, sdk = _TRANSLATORS[provider]
    transport = httpx2 if sdk is not gemini_sdk and issubclass(sdk.DefaultHttpxClient, httpx2.Client) else httpx
    request = transport.Request("POST", "https://example.test")

    assert isinstance(translate(sdk.APITimeoutError(request=request)), TypeSafeAPITimeoutError)
    assert isinstance(translate(sdk.APIConnectionError(request=request)), TypeSafeAPIConnectionError)


@pytest.mark.parametrize("provider", ["openai", "anthropic", "gemini"])
def test_unknown_and_sdk_errors_pass_through(provider: str) -> None:
    translate, _ = _TRANSLATORS[provider]
    sdk_error = TypeSafeBadRequestError(400, "already mapped", httpx2.Headers(), None)

    # A pre-existing SDK error is returned unchanged; anything else becomes a base one.
    assert translate(sdk_error) is sdk_error
    assert isinstance(translate(RuntimeError("weird")), TypeSafeError)
    assert not isinstance(translate(RuntimeError("weird")), TypeSafeAPIError)


def test_translating_context_manager_reraises_translated_error() -> None:
    original = _status_error(openai, 429, {"m": 1})

    with (
        pytest.raises(TypeSafeRateLimitError) as raised,
        translating(_OpenAIErrors.translate_error),
    ):
        raise original

    assert raised.value.status == 429
    assert raised.value.__cause__ is original


def test_retries_succeed_after_transient_error() -> None:
    calls = 0

    def fail_once() -> str:
        nonlocal calls
        calls += 1
        if calls == 1:
            raise _OpenAIErrors.translate_error(_status_error(openai, 503, {"m": "unavailable"}))
        return "success"

    result, n_retries = run_with_retries(
        fail_once,
        RetryPolicy(max_retries=1, backoff_initial=0.001, backoff_jitter=0),
    )

    assert result == "success"
    assert calls == 2
    assert n_retries == 1


def test_non_retryable_error_is_not_retried() -> None:
    calls = 0

    def raise_bad_request() -> None:
        nonlocal calls
        calls += 1
        raise _OpenAIErrors.translate_error(_status_error(openai, 400, {"m": "bad"}))

    with pytest.raises(TypeSafeBadRequestError):
        run_with_retries(
            raise_bad_request,
            RetryPolicy(max_retries=2, backoff_initial=0.001, backoff_jitter=0),
        )

    assert calls == 1


def test_retries_are_exhausted_and_reasons_recorded() -> None:
    reasons: list[RetryReasons] = []

    def always_fail() -> None:
        raise _OpenAIErrors.translate_error(_status_error(openai, 503, {"m": "unavailable"}))

    with pytest.raises(TypeSafeInternalServerError):
        run_with_retries(
            always_fail,
            RetryPolicy(max_retries=2, backoff_initial=0.001, backoff_jitter=0),
            reasons,
        )

    assert [reason.category for reason in reasons] == [
        "provider_error",
        "provider_error",
    ]
