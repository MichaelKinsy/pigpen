"""Provider-declared failures must preserve traces without spending retry budgets."""

import asyncio
import json
from typing import Any, Literal

import httpx
import httpx2
import pytest
from typesafe_sdk import RetryPolicy, TypeSafeError

from system_one_adapter import AsyncSystemOneAdapterClient, Noul, SystemOneAdapterClient, SystemOneResponse
from system_one_adapter.providers.anthropic import AnthropicProvider, AsyncAnthropicProvider
from system_one_adapter.providers.openai import AsyncOpenAIProvider, OpenAIProvider
from tests.http import json_response


@pytest.fixture
def http_response(monkeypatch: pytest.MonkeyPatch) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    payload: dict[str, Any] = {}
    requests: list[dict[str, Any]] = []

    def respond(request: httpx.Request | httpx2.Request) -> httpx.Response | httpx2.Response:
        requests.append(json.loads(request.content))
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
    return payload, requests


def _evaluate(
    provider: OpenAIProvider | AsyncOpenAIProvider | AnthropicProvider | AsyncAnthropicProvider, *, structured: bool
) -> SystemOneResponse:
    questions = {"positive": Noul(instructions="The review is positive.")}
    retry = RetryPolicy(max_retries=2, backoff_initial=0)
    if isinstance(provider, (AsyncOpenAIProvider, AsyncAnthropicProvider)):

        async def run() -> SystemOneResponse:
            try:
                async with AsyncSystemOneAdapterClient(
                    structured_outputs=structured, llm_answer_mode="discrete", n_retry_malformed_structure=2, retry=retry, model=provider
                ) as client:
                    return await client.system_one("A delightful book.", questions)
            finally:
                await provider.aclose()

        return asyncio.run(run())
    try:
        with SystemOneAdapterClient(
            structured_outputs=structured, llm_answer_mode="discrete", n_retry_malformed_structure=2, retry=retry, model=provider
        ) as client:
            return client.system_one("A delightful book.", questions)
    finally:
        provider.close()


@pytest.mark.parametrize("provider_class", [OpenAIProvider, AsyncOpenAIProvider])
@pytest.mark.parametrize("structured", [False, True])
@pytest.mark.parametrize("finish_reason", ["stop", None, "length", "content_filter", "tool_calls", "function_call", "unknown"])
def test_chat_completion_finish_reason(
    http_response: tuple[dict[str, Any], list[dict[str, Any]]],
    provider_class: type[OpenAIProvider] | type[AsyncOpenAIProvider],
    structured: bool,
    finish_reason: str | None,
) -> None:
    payload, requests = http_response
    payload.update(
        choices=[{"finish_reason": finish_reason, "message": {"role": "assistant", "content": '{"answers":{"positive":true}}'}}],
        usage={"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19},
    )
    provider = provider_class("test-model", api="chat_completions")
    if finish_reason in ("stop", None):
        response = _evaluate(provider, structured=structured)
        assert response.nouls["positive"].noul == 1.0
        debug = response.debug
    else:
        with pytest.raises(TypeSafeError, match=f"did not complete: {finish_reason}") as raised:
            _evaluate(provider, structured=structured)
        debug = raised.value.debug  # pyrefly: ignore[missing-attribute]
        assert debug["retry_reasons"] == []
    assert len(requests) == 1
    assert len(debug["llm_attempts"]) == 1
    attempt = debug["llm_attempts"][0]
    assert attempt["request"] == requests[0]
    assert attempt["llm_response"]["choices"][0]["message"]["content"] == payload["choices"][0]["message"]["content"]
    assert attempt["llm_response"]["choices"][0]["finish_reason"] == finish_reason
    assert attempt["debug_info"]["finish_reason"] == finish_reason
    assert ("error" in attempt["debug_info"]) == (finish_reason not in ("stop", None))
    json.dumps(debug)


@pytest.mark.parametrize("provider_class", [OpenAIProvider, AsyncOpenAIProvider])
@pytest.mark.parametrize("structured", [False, True])
@pytest.mark.parametrize("api", ["chat_completions", "responses"])
@pytest.mark.parametrize("usage_case", ["omitted", "null", "missing_input", "null_input", "missing_output", "null_output", "zero", "present"])
def test_openai_missing_usage(
    http_response: tuple[dict[str, Any], list[dict[str, Any]]],
    provider_class: type[OpenAIProvider] | type[AsyncOpenAIProvider],
    structured: bool,
    api: Literal["chat_completions", "responses"],
    usage_case: str,
) -> None:
    payload, requests = http_response
    text = '{"answers":{"positive":true}}'
    if api == "responses":
        payload.update(
            status="completed",
            output=[{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": text}]}],
        )
        input_field, output_field = "input_tokens", "output_tokens"
    else:
        payload["choices"] = [{"finish_reason": "stop", "message": {"role": "assistant", "content": text}}]
        input_field, output_field = "prompt_tokens", "completion_tokens"
    usage = {input_field: 12, output_field: 7, "total_tokens": 19}
    if usage_case == "zero":
        usage = dict.fromkeys(usage, 0)
    if usage_case != "omitted":
        payload["usage"] = None if usage_case == "null" else usage
    for field, name in [(input_field, "input"), (output_field, "output")]:
        if usage_case == f"missing_{name}":
            del usage[field]
        elif usage_case == f"null_{name}":
            payload["usage"][field] = None

    provider = provider_class("test-model", api=api)
    response = _evaluate(provider, structured=structured)
    assert response.nouls["positive"].noul == 1.0
    expected_input = None if usage_case in ("omitted", "null", "missing_input", "null_input") else usage[input_field]
    expected_output = None if usage_case in ("omitted", "null", "missing_output", "null_output") else usage[output_field]
    assert response.usage.input_tokens == response.usage.input_tokens_total == expected_input
    assert response.usage.output_tokens == response.usage.output_tokens_total == expected_output
    assert response.usage.n_retries == response.usage.n_retries_malformed_structure == 0
    assert response.model_dump()["usage"]["input_tokens"] == expected_input
    assert response.model_dump()["usage"]["output_tokens"] == expected_output
    debug = response.debug
    assert debug["retry_reasons"] == []
    assert len(requests) == len(debug["llm_attempts"]) == 1
    attempt = debug["llm_attempts"][0]
    assert attempt["request"] == requests[0]
    assert attempt["llm_response"] is not None
    if usage_case in ("omitted", "null"):
        assert attempt["llm_response"]["usage"] is None
    else:
        assert all(attempt["llm_response"]["usage"][field] == value for field, value in usage.items())
    assert attempt["debug_info"]["finish_reason"] == ("completed" if api == "responses" else "stop")
    assert "error" not in attempt["debug_info"]
    json.dumps(debug)


@pytest.mark.parametrize("provider_class", [AnthropicProvider, AsyncAnthropicProvider])
@pytest.mark.parametrize("structured", [False, True])
@pytest.mark.parametrize(
    "stop_reason",
    ["end_turn", "stop_sequence", None, "refusal", "model_context_window_exceeded", "pause_turn", "tool_use", "unknown", "max_tokens"],
)
def test_anthropic_nonanswers(
    http_response: tuple[dict[str, Any], list[dict[str, Any]]],
    provider_class: type[AnthropicProvider] | type[AsyncAnthropicProvider],
    structured: bool,
    stop_reason: str | None,
) -> None:
    payload, requests = http_response
    payload.update(
        id="message-test",
        type="message",
        role="assistant",
        model="test-model",
        stop_reason=stop_reason,
        content=[] if stop_reason == "refusal" else [{"type": "text", "text": '{"answers":{"positive":true}}'}],
        usage={"input_tokens": 12, "output_tokens": 7},
    )
    provider = provider_class("test-model")
    succeeded = stop_reason in ("end_turn", "stop_sequence", None)
    if succeeded:
        response = _evaluate(provider, structured=structured)
        assert response.nouls["positive"].noul == 1.0
        debug = response.debug
    else:
        reason = "truncated.*Increase max_tokens" if stop_reason == "max_tokens" else f"did not complete: {stop_reason}"
        with pytest.raises(TypeSafeError, match=reason) as raised:
            _evaluate(provider, structured=structured)
        debug = raised.value.debug  # pyrefly: ignore[missing-attribute]
        assert debug["retry_reasons"] == []
    assert len(requests) == len(debug["llm_attempts"]) == 1
    attempt = debug["llm_attempts"][0]
    assert attempt["request"] == requests[0]
    assert len(attempt["llm_response"]["content"]) == len(payload["content"])
    assert attempt["llm_response"]["stop_reason"] == stop_reason
    assert attempt["debug_info"]["finish_reason"] == stop_reason
    assert ("error" in attempt["debug_info"]) != succeeded
    json.dumps(debug)


@pytest.mark.parametrize("provider_class", [OpenAIProvider, AsyncOpenAIProvider])
@pytest.mark.parametrize("structured", [False, True])
@pytest.mark.parametrize("with_valid_text", [False, True])
def test_openai_responses_refusal(
    http_response: tuple[dict[str, Any], list[dict[str, Any]]],
    provider_class: type[OpenAIProvider] | type[AsyncOpenAIProvider],
    structured: bool,
    with_valid_text: bool,
) -> None:
    payload, requests = http_response
    refusal = "Cannot evaluate this request."
    output: list[dict[str, Any]] = [{"type": "reasoning", "id": "reasoning-test", "summary": []}]
    if with_valid_text:
        output.append({"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": '{"answers":{"positive":true}}'}]})
    output.append({"type": "message", "role": "assistant", "content": [{"type": "refusal", "refusal": refusal}]})
    # Missing usage is allowed, but refusal content must still fail the evaluation.
    payload.update(status="completed", output=output)
    provider = provider_class("test-model", api="responses")
    with pytest.raises(TypeSafeError, match=f"refusal: {refusal}") as raised:
        _evaluate(provider, structured=structured)
    debug = raised.value.debug  # pyrefly: ignore[missing-attribute]
    assert debug["retry_reasons"] == []
    assert len(requests) == len(debug["llm_attempts"]) == 1
    attempt = debug["llm_attempts"][0]
    assert attempt["request"] == requests[0]
    assert attempt["llm_response"]["output"][-1]["content"][0]["refusal"] == refusal
    assert attempt["debug_info"]["finish_reason"] == "completed"
    assert "refusal" in attempt["debug_info"]["error"]
    json.dumps(debug)
