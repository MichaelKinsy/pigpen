"""Exercise the Gemini Interactions transport through the SDK's HTTP parsing."""

import asyncio
import json
from typing import Any

import httpx
import pytest
from typesafe_sdk import RetryPolicy, TypeSafeAPIConnectionError, TypeSafeAPITimeoutError, TypeSafeError

from system_one_adapter import (
    AsyncSystemOneAdapterClient,
    Noul,
    SystemOneAdapterClient,
    SystemOneResponse,
)
from system_one_adapter.providers.gemini import AsyncGeminiProvider, GeminiProvider


def _interaction_payload(text: str, *, status: str = "completed") -> dict[str, Any]:
    return {
        "id": "interaction-test",
        "status": status,
        "model": "gemini-3.8-flash",
        "steps": [
            {
                "type": "model_output",
                "content": [{"type": "text", "text": text}],
            }
        ],
        "usage": {
            "total_input_tokens": 12,
            "total_output_tokens": 7,
            "total_tokens": 19,
        },
    }


@pytest.mark.parametrize("provider_class", [GeminiProvider, AsyncGeminiProvider])
@pytest.mark.parametrize("structured", [False, True])
def test_gemini_transport_preserves_corrections_and_usage(
    monkeypatch: pytest.MonkeyPatch,
    provider_class: type[GeminiProvider] | type[AsyncGeminiProvider],
    structured: bool,
) -> None:
    requests: list[dict[str, Any]] = []
    malformed = '{"answers":'

    def respond(request: httpx.Request) -> httpx.Response:
        requests.append(json.loads(request.content))
        text = malformed if len(requests) == 1 else '{"answers":{"positive":true}}'
        return httpx.Response(200, request=request, json=_interaction_payload(text))

    def send(client: httpx.Client, request: httpx.Request, **kwargs: Any) -> httpx.Response:
        return respond(request)

    async def send_async(client: httpx.AsyncClient, request: httpx.Request, **kwargs: Any) -> httpx.Response:
        return respond(request)

    monkeypatch.setattr(httpx.Client, "send", send)
    monkeypatch.setattr(httpx.AsyncClient, "send", send_async)
    provider = provider_class("gemini-3.8-flash")
    questions = {"positive": Noul(instructions="The review is positive.")}
    if isinstance(provider, AsyncGeminiProvider):

        async def run() -> SystemOneResponse:
            try:
                client = AsyncSystemOneAdapterClient(
                    structured_outputs=structured,
                    llm_answer_mode="discrete",
                    n_retry_malformed_structure=1,
                    model=provider,
                )
                return await client.system_one("A delightful book.", questions)
            finally:
                await provider.aclose()

        response = asyncio.run(run())
    else:
        with provider._client:
            client = SystemOneAdapterClient(
                structured_outputs=structured,
                llm_answer_mode="discrete",
                n_retry_malformed_structure=1,
                model=provider,
            )
            response = client.system_one("A delightful book.", questions)

    assert response.nouls["positive"].noul == 1.0
    assert (
        response.usage.input_tokens,
        response.usage.output_tokens,
        response.usage.input_tokens_total,
        response.usage.output_tokens_total,
        response.usage.n_retries,
        response.usage.n_retries_malformed_structure,
    ) == (12, 7, 24, 14, 0, 1)
    attempts = response.debug["llm_attempts"]
    assert len(attempts) == 2
    assert [attempt["request"] for attempt in attempts] == requests
    assert requests[0]["store"] is False
    assert requests[0]["system_instruction"].startswith("Evaluate every question")
    if structured:
        assert requests[0]["response_format"]["schema"]["$defs"]["TypeSafeAnswers"]["properties"]["positive"]
    else:
        assert "response_format" not in requests[0]
    assert requests[-1]["input"][-2] == {
        "type": "model_output",
        "content": [{"type": "text", "text": malformed}],
    }
    assert requests[-1]["input"][-1]["type"] == "user_input"
    assert "previous response did not match" in requests[-1]["input"][-1]["content"][0]["text"]


@pytest.mark.parametrize("provider_class", [GeminiProvider, AsyncGeminiProvider])
def test_gemini_incomplete_http_response_is_not_an_answer(
    monkeypatch: pytest.MonkeyPatch,
    provider_class: type[GeminiProvider] | type[AsyncGeminiProvider],
) -> None:
    def respond(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            request=request,
            json=_interaction_payload('{"answers":{"positive":true}}', status="incomplete"),
        )

    def send(client: httpx.Client, request: httpx.Request, **kwargs: Any) -> httpx.Response:
        return respond(request)

    async def send_async(client: httpx.AsyncClient, request: httpx.Request, **kwargs: Any) -> httpx.Response:
        return respond(request)

    monkeypatch.setattr(httpx.Client, "send", send)
    monkeypatch.setattr(httpx.AsyncClient, "send", send_async)
    provider = provider_class("gemini-3.8-flash")
    questions = {"positive": Noul(instructions="The review is positive.")}

    def evaluate() -> None:
        if isinstance(provider, AsyncGeminiProvider):

            async def run() -> None:
                try:
                    client = AsyncSystemOneAdapterClient(
                        structured_outputs=True,
                        llm_answer_mode="discrete",
                        model=provider,
                    )
                    await client.system_one("A delightful book.", questions)
                finally:
                    await provider.aclose()

            asyncio.run(run())
        else:
            with provider._client:
                client = SystemOneAdapterClient(structured_outputs=True, llm_answer_mode="discrete", model=provider)
                client.system_one("A delightful book.", questions)

    with pytest.raises(TypeSafeError, match="did not complete"):
        evaluate()


@pytest.mark.parametrize("provider_class", [GeminiProvider, AsyncGeminiProvider])
@pytest.mark.parametrize("retry_budget", [0, 1])
@pytest.mark.parametrize(
    "transport_error,expected_error",
    [(httpx.ConnectError, TypeSafeAPIConnectionError), (httpx.ReadTimeout, TypeSafeAPITimeoutError)],
)
def test_gemini_transport_errors_obey_retry_budget(
    monkeypatch: pytest.MonkeyPatch,
    provider_class: type[GeminiProvider] | type[AsyncGeminiProvider],
    retry_budget: int,
    transport_error: type[httpx.RequestError],
    expected_error: type[TypeSafeError],
) -> None:
    requests: list[httpx.Request] = []

    def send(client: httpx.Client, request: httpx.Request, **kwargs: Any) -> httpx.Response:
        requests.append(request)
        raise transport_error("unavailable", request=request)

    async def send_async(client: httpx.AsyncClient, request: httpx.Request, **kwargs: Any) -> httpx.Response:
        requests.append(request)
        raise transport_error("unavailable", request=request)

    monkeypatch.setattr(httpx.Client, "send", send)
    monkeypatch.setattr(httpx.AsyncClient, "send", send_async)
    retry = RetryPolicy(max_retries=retry_budget, backoff_initial=0)
    questions = {"positive": Noul(instructions="The review is positive.")}
    provider = provider_class("test-model")

    def evaluate() -> None:
        if isinstance(provider, AsyncGeminiProvider):

            async def run() -> None:
                try:
                    async with AsyncSystemOneAdapterClient(
                        structured_outputs=False, llm_answer_mode="discrete", model=provider, retry=retry
                    ) as client:
                        await client.system_one("A delightful book.", questions)
                finally:
                    await provider.aclose()

            asyncio.run(run())
        else:
            try:
                with SystemOneAdapterClient(structured_outputs=False, llm_answer_mode="discrete", model=provider, retry=retry) as client:
                    client.system_one("A delightful book.", questions)
            finally:
                provider.close()

    with pytest.raises(expected_error) as raised:
        evaluate()
    assert len(requests) == retry_budget + 1
    assert len(raised.value.debug["llm_attempts"]) == len(requests)  # pyrefly: ignore[missing-attribute]
