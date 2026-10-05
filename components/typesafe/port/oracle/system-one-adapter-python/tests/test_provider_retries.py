"""Retry budgets apply to HTTP attempts inside the real provider SDKs."""

import asyncio
import json
from typing import Any

import httpx
import httpx2
import pytest
from typesafe_sdk import TypeSafeInternalServerError

from system_one_adapter import (
    AsyncSystemOneAdapterClient,
    Noul,
    RetryPolicy,
    SystemOneAdapterClient,
)
from system_one_adapter.providers.anthropic import (
    AnthropicProvider,
    AsyncAnthropicProvider,
)
from system_one_adapter.providers.gemini import AsyncGeminiProvider, GeminiProvider
from system_one_adapter.providers.openai import AsyncOpenAIProvider, OpenAIProvider
from tests.http import json_response


@pytest.mark.parametrize("retry_budget", [0, 1])
@pytest.mark.parametrize(
    "provider_class",
    [
        OpenAIProvider,
        AnthropicProvider,
        GeminiProvider,
        AsyncOpenAIProvider,
        AsyncAnthropicProvider,
        AsyncGeminiProvider,
    ],
)
def test_retry_policy_controls_http_attempts(
    monkeypatch: pytest.MonkeyPatch,
    provider_class: type[OpenAIProvider]
    | type[AnthropicProvider]
    | type[GeminiProvider]
    | type[AsyncOpenAIProvider]
    | type[AsyncAnthropicProvider]
    | type[AsyncGeminiProvider],
    retry_budget: int,
) -> None:
    calls: list[httpx.Request | httpx2.Request] = []

    def send(client: httpx.Client | httpx2.Client, request: httpx.Request | httpx2.Request, **kwargs: Any) -> httpx.Response | httpx2.Response:
        calls.append(request)
        return json_response(request, {"error": {"message": "unavailable"}}, status=503)

    async def send_async(
        client: httpx.AsyncClient | httpx2.AsyncClient, request: httpx.Request | httpx2.Request, **kwargs: Any
    ) -> httpx.Response | httpx2.Response:
        calls.append(request)
        return json_response(request, {"error": {"message": "unavailable"}}, status=503)

    for client_class, handler in [
        (httpx.Client, send),
        (httpx2.Client, send),
        (httpx.AsyncClient, send_async),
        (httpx2.AsyncClient, send_async),
    ]:
        monkeypatch.setattr(client_class, "send", handler)
    provider = provider_class("test-model")

    def no_delay(*args: Any, **kwargs: Any) -> float:
        return 0

    # Keep the regression fast even if the SDK's own retries are accidentally enabled.
    if hasattr(provider._client, "_calculate_retry_timeout"):
        monkeypatch.setattr(provider._client, "_calculate_retry_timeout", no_delay)
    retry = RetryPolicy(max_retries=retry_budget, backoff_initial=0)
    questions = {"positive": Noul(instructions="The review is positive.")}

    def assert_attempts(error: Any) -> None:
        attempts = error.debug["llm_attempts"]
        assert len(attempts) == retry_budget + 1
        assert [attempt["request"] for attempt in attempts] == [json.loads(call.content) for call in calls]
        for attempt in attempts:
            assert attempt["llm_response"] is None
            assert attempt["debug_info"]["error_type"] == "TypeSafeInternalServerError"
            assert "unavailable" in attempt["debug_info"]["error"]
        json.dumps(error.debug)

    if isinstance(provider, (AsyncOpenAIProvider, AsyncAnthropicProvider, AsyncGeminiProvider)):

        async def run() -> None:
            try:
                client = AsyncSystemOneAdapterClient(
                    structured_outputs=False,
                    llm_answer_mode="discrete",
                    model=provider,
                    retry=retry,
                )
                with pytest.raises(TypeSafeInternalServerError) as raised:
                    await client.system_one("A delightful book.", questions)
                assert_attempts(raised.value)
            finally:
                if isinstance(provider, AsyncGeminiProvider):
                    await provider.aclose()
                else:
                    await provider._client.close()

        asyncio.run(run())
    else:
        with provider._client:
            client = SystemOneAdapterClient(
                structured_outputs=False,
                llm_answer_mode="discrete",
                model=provider,
                retry=retry,
            )
            with pytest.raises(TypeSafeInternalServerError) as raised:
                client.system_one("A delightful book.", questions)
            assert_attempts(raised.value)

    assert len(calls) == retry_budget + 1
