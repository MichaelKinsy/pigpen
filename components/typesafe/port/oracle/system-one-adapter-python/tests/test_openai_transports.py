"""Exercise both OpenAI transports through the SDK's HTTP parsing and client retries."""

import asyncio
import json
from types import SimpleNamespace
from typing import Any, Literal

import httpx
import httpx2
import pytest
from typesafe_sdk import TypeSafeError

from system_one_adapter import AsyncSystemOneAdapterClient, Noul, SystemOneAdapterClient, SystemOneResponse
from system_one_adapter.providers import Message
from system_one_adapter.providers.openai import AsyncOpenAIProvider, OpenAIProvider, _responses_result
from tests.http import json_response


def _assert_attempts(response: SystemOneResponse, requests: list[dict[str, Any]], endpoint: str, malformed: str) -> None:
    attempts = response.debug["llm_attempts"]
    assert [attempt["request"] for attempt in attempts] == requests
    assert [len(attempt["messages"]) for attempt in attempts] == [2, 4]
    for index, attempt in enumerate(attempts):
        raw = attempt["llm_response"]
        if endpoint == "/v1/responses":
            for key, value in attempt["request"]["text"]["format"].items():
                assert raw["text"]["format"][key] == value
        text = raw["output"][1]["content"][0]["text"] if endpoint == "/v1/responses" else raw["choices"][0]["message"]["content"]
        assert text == (malformed if index == 0 else '{"answers":{"positive":true}}')
        assert attempt["debug_info"]["api"] == endpoint.rsplit("/v1/", maxsplit=1)[-1].replace("/", "_")
    json.dumps(response.debug)


@pytest.mark.parametrize("provider_class", [OpenAIProvider, AsyncOpenAIProvider])
@pytest.mark.parametrize("structured", [False, True])
@pytest.mark.parametrize(
    "base_url,api,endpoint",
    [
        (None, None, "/v1/responses"),
        ("https://compatible.test/v1", None, "/v1/chat/completions"),
        ("https://proxy.test/v1", "responses", "/v1/responses"),
        (None, "chat_completions", "/v1/chat/completions"),
    ],
)
def test_openai_transport_preserves_corrections_and_usage(
    monkeypatch: pytest.MonkeyPatch,
    provider_class: type[OpenAIProvider] | type[AsyncOpenAIProvider],
    structured: bool,
    base_url: str | None,
    api: Literal["responses", "chat_completions"] | None,
    endpoint: str,
) -> None:
    monkeypatch.delenv("OPENAI_BASE_URL", raising=False)
    requests: list[dict[str, Any]] = []
    malformed = '{"answers":'

    def respond(request: httpx.Request | httpx2.Request) -> httpx.Response | httpx2.Response:
        assert request.url.path == endpoint
        requests.append(json.loads(request.content))
        text = malformed if len(requests) == 1 else '{"answers":{"positive":true}}'
        if endpoint == "/v1/responses":
            payload = {
                "status": "completed",
                "text": requests[-1]["text"],
                "output": [
                    {"id": "reasoning-test", "type": "reasoning", "summary": [{"type": "summary_text", "text": "Ignored summary."}]},
                    {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": text}]},
                ],
                "usage": {"input_tokens": 12, "output_tokens": 7, "total_tokens": 19},
            }
        else:
            payload = {
                "choices": [{"message": {"role": "assistant", "content": text}}],
                "usage": {"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19},
            }
        return json_response(request, payload)

    def send(client: httpx.Client | httpx2.Client, request: httpx.Request | httpx2.Request, **kwargs: Any) -> httpx.Response | httpx2.Response:
        return respond(request)

    async def send_async(
        client: httpx.AsyncClient | httpx2.AsyncClient, request: httpx.Request | httpx2.Request, **kwargs: Any
    ) -> httpx.Response | httpx2.Response:
        return respond(request)

    for client_class, handler in [
        (httpx.Client, send),
        (httpx2.Client, send),
        (httpx.AsyncClient, send_async),
        (httpx2.AsyncClient, send_async),
    ]:
        monkeypatch.setattr(client_class, "send", handler)
    provider = provider_class("test-model", base_url=base_url, api=api)
    questions = {"positive": Noul(instructions="The review is positive.")}
    if isinstance(provider, AsyncOpenAIProvider):

        async def run() -> SystemOneResponse:
            async with provider._client:
                client = AsyncSystemOneAdapterClient(
                    structured_outputs=structured, llm_answer_mode="discrete", n_retry_malformed_structure=1, model=provider
                )
                return await client.system_one("A delightful book.", questions)

        response = asyncio.run(run())
    else:
        with provider._client:
            client = SystemOneAdapterClient(
                structured_outputs=structured, llm_answer_mode="discrete", n_retry_malformed_structure=1, model=provider
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
    _assert_attempts(response, requests, endpoint, malformed)

    for body in requests:
        if endpoint == "/v1/responses":
            assert body["store"] is False
            assert "previous_response_id" not in body
            assert body.get("instructions", body["input"][0]["content"]).startswith("Evaluate every question")
            assert structured or "JSON" in str(body["input"])
            output_format = body["text"]["format"]
            assert output_format["type"] == ("json_schema" if structured else "json_object")
            if structured:
                assert output_format["strict"] is True
                assert "positive" in output_format["schema"]["$defs"]["TypeSafeAnswers"]["properties"]
            assert body["input"][0]["role"] == ("user" if structured else "system")
        else:
            assert body["messages"][0]["role"] == "system"
            if structured:
                assert body["response_format"]["json_schema"]["strict"] is True
            else:
                assert body["response_format"] is None

    messages = requests[-1]["input" if endpoint == "/v1/responses" else "messages"]
    assert messages[-2] == {"role": "assistant", "content": malformed}
    assert messages[-1]["role"] == "user"
    assert "previous response did not match" in messages[-1]["content"]


@pytest.mark.parametrize("status", ["failed", "incomplete"])
def test_unfinished_responses_are_not_treated_as_answers(status: str) -> None:
    response = SimpleNamespace(
        status=status,
        error=SimpleNamespace(message="generation failed") if status == "failed" else None,
        incomplete_details=SimpleNamespace(reason="max_output_tokens") if status == "incomplete" else None,
        output_text='{"answers":{"positive":true}}',
    )
    reason = "generation failed" if status == "failed" else "max_output_tokens"
    with pytest.raises(TypeSafeError, match=f"OpenAI response did not complete: {reason}"):
        _responses_result(response)


@pytest.mark.parametrize("first_status", ["completed", "incomplete", "failed"])
def test_concurrent_attempts_are_isolated_and_preserve_failed_responses(monkeypatch: pytest.MonkeyPatch, first_status: str) -> None:
    async def run() -> None:
        requests: list[dict[str, Any]] = []
        ready = asyncio.Event()

        async def send(
            client: httpx.AsyncClient | httpx2.AsyncClient, request: httpx.Request | httpx2.Request, **kwargs: Any
        ) -> httpx.Response | httpx2.Response:
            body = json.loads(request.content)
            requests.append(body)
            if len(requests) >= 2:
                ready.set()
            await asyncio.wait_for(ready.wait(), timeout=5)
            document = body["input"][0]["content"]
            status = first_status if "first document" in document else "completed"
            payload = {
                "status": status,
                "error": {"code": "server_error", "message": "generation failed"} if status == "failed" else None,
                "incomplete_details": {"reason": "max_output_tokens"} if status == "incomplete" else None,
                "output": [
                    {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": '{"answers":{"positive":true}}'}]}
                ],
                "usage": {"input_tokens": 12, "output_tokens": 7, "total_tokens": 19},
            }
            return json_response(request, payload)

        monkeypatch.setattr(httpx.AsyncClient, "send", send)
        monkeypatch.setattr(httpx2.AsyncClient, "send", send)
        provider = AsyncOpenAIProvider("test-model", api="responses")
        async with provider._client:
            client = AsyncSystemOneAdapterClient(structured_outputs=True, llm_answer_mode="discrete", model=provider)
            questions = {"positive": Noul(instructions="The review is positive.")}

            async def evaluate(document: str) -> dict[str, Any]:
                try:
                    return (await client.system_one(document, questions)).debug
                except TypeSafeError as error:
                    return error.debug  # pyrefly: ignore[missing-attribute]

            first, second = await asyncio.gather(evaluate("first document"), evaluate("second document"))
            for debug, document, status in [(first, "first document", first_status), (second, "second document", "completed")]:
                assert len(debug["llm_attempts"]) == 1
                attempt = debug["llm_attempts"][0]
                assert document in attempt["messages"][1]["content"]
                assert document in attempt["request"]["input"][0]["content"]
                assert attempt["request"] in requests
                assert attempt["llm_response"]["status"] == status
                assert attempt["debug_info"]["finish_reason"] == status
                assert ("error" in attempt["debug_info"]) == (status != "completed")

            # A later direct provider call must not mutate either finished trace.
            before = json.dumps([first, second])
            attempt = second["llm_attempts"][0]
            await provider.request([Message(**message) for message in attempt["messages"]], **attempt["model_request_parameters"])
            assert json.dumps([first, second]) == before

    asyncio.run(run())


@pytest.mark.parametrize("provider_class", [OpenAIProvider, AsyncOpenAIProvider])
def test_custom_endpoint_from_environment_defaults_to_chat(
    monkeypatch: pytest.MonkeyPatch, provider_class: type[OpenAIProvider] | type[AsyncOpenAIProvider]
) -> None:
    monkeypatch.setenv("OPENAI_BASE_URL", "https://compatible.test/v1")
    provider = provider_class("test-model")
    try:
        assert provider.api == "chat_completions"
    finally:
        if isinstance(provider, AsyncOpenAIProvider):
            asyncio.run(provider._client.close())
        else:
            provider._client.close()
