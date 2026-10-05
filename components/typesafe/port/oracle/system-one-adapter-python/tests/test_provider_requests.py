"""Provider request-building and result-parsing, without any network call.

These cover the OpenAI- and Anthropic-specific request envelopes and response parsing
that the cassette-backed live tests would otherwise be the only ones to exercise.
"""

import asyncio
from types import SimpleNamespace
from typing import Any

import pytest
from anthropic.types import Message as AnthropicMessage
from typesafe_sdk import TypeSafeError

from system_one_adapter import (
    AsyncSystemOneAdapterClient,
    Noul,
    RetryPolicy,
    SystemOneAdapterClient,
    SystemOneResponse,
)
from system_one_adapter.providers import (
    Message,
    build_async_provider,
    build_sync_provider,
)
from system_one_adapter.providers.anthropic import (
    AnthropicProvider,
    AsyncAnthropicProvider,
    _request_kwargs,
)
from system_one_adapter.providers.anthropic import _result as anthropic_result
from system_one_adapter.providers.gemini import AsyncGeminiProvider, GeminiProvider
from system_one_adapter.providers.gemini import _request_kwargs as gemini_request_kwargs
from system_one_adapter.providers.gemini import _result as gemini_result
from system_one_adapter.providers.openai import _response_format
from system_one_adapter.providers.openai import _result as openai_result

SCHEMA = {"type": "object", "properties": {"answers": {"type": "object"}}}
MESSAGES = [
    Message(role="system", content="system prompt"),
    Message(role="user", content="the document"),
]


def test_openai_native_response_format_wraps_schema() -> None:
    assert _response_format(SCHEMA, structured=True) == {
        "type": "json_schema",
        "json_schema": {"name": "evaluation", "schema": SCHEMA, "strict": True},
    }


def test_openai_prompted_sends_no_response_format() -> None:
    assert _response_format(SCHEMA, structured=False) is None


def test_openai_result_reads_content_and_usage() -> None:
    response = SimpleNamespace(
        choices=[SimpleNamespace(message=SimpleNamespace(content='{"answers": {}}'), finish_reason="stop")],
        usage=SimpleNamespace(prompt_tokens=12, completion_tokens=3),
    )
    result = openai_result(response)
    assert result.text == '{"answers": {}}'
    assert (result.input_tokens, result.output_tokens) == (12, 3)


def test_anthropic_request_puts_schema_in_output_config_when_structured() -> None:
    kwargs = _request_kwargs("claude-haiku-4-5", MESSAGES, SCHEMA, structured=True, max_tokens=4096)
    assert kwargs["system"] == "system prompt"
    assert kwargs["messages"] == [{"role": "user", "content": "the document"}]
    assert kwargs["max_tokens"] > 0
    assert kwargs["output_config"] == {"format": {"type": "json_schema", "schema": SCHEMA}}


def test_anthropic_request_omits_output_config_when_prompted() -> None:
    kwargs = _request_kwargs("claude-haiku-4-5", MESSAGES, SCHEMA, structured=False, max_tokens=4096)
    assert "output_config" not in kwargs


def test_gemini_request_puts_schema_in_response_format_when_structured() -> None:
    kwargs = gemini_request_kwargs("gemini-3.8-flash", MESSAGES, SCHEMA, structured=True)
    assert kwargs["system_instruction"] == "system prompt"
    assert kwargs["input"] == [{"type": "user_input", "content": [{"type": "text", "text": "the document"}]}]
    assert kwargs["store"] is False
    assert kwargs["response_format"] == {
        "type": "text",
        "mime_type": "application/json",
        "schema": SCHEMA,
    }


def test_gemini_request_omits_response_format_when_prompted() -> None:
    kwargs = gemini_request_kwargs("gemini-3.8-flash", MESSAGES, SCHEMA, structured=False)
    assert "response_format" not in kwargs
    assert kwargs["store"] is False


def test_gemini_request_sends_correction_turns_as_steps() -> None:
    messages = [
        *MESSAGES,
        Message(role="assistant", content='{"answers":'),
        Message(role="user", content="fix it"),
    ]
    kwargs = gemini_request_kwargs("gemini-3.8-flash", messages, SCHEMA, structured=True)
    assert kwargs["input"] == [
        {"type": "user_input", "content": [{"type": "text", "text": "the document"}]},
        {"type": "model_output", "content": [{"type": "text", "text": '{"answers":'}]},
        {"type": "user_input", "content": [{"type": "text", "text": "fix it"}]},
    ]


def test_gemini_result_reads_output_text_and_usage() -> None:
    response = SimpleNamespace(
        status="completed",
        output_text='{"answers": {}}',
        usage=SimpleNamespace(total_input_tokens=20, total_output_tokens=5),
        errors=None,
    )
    result = gemini_result(response)
    assert result.text == '{"answers": {}}'
    assert (result.input_tokens, result.output_tokens) == (20, 5)


@pytest.mark.parametrize("status", ["incomplete", "failed", "cancelled", "budget_exceeded"])
def test_gemini_incomplete_status_is_not_treated_as_an_answer(status: str) -> None:
    response = SimpleNamespace(
        status=status,
        output_text='{"answers":{"positive":true}}',
        usage=SimpleNamespace(total_input_tokens=20, total_output_tokens=5),
        errors=None,
    )
    with pytest.raises(TypeSafeError, match=r"Gemini response did not complete"):
        gemini_result(response)


def test_gemini_omitted_usage_is_not_treated_as_an_answer() -> None:
    response = SimpleNamespace(status="completed", output_text='{"answers":{}}', usage=None, errors=None)
    with pytest.raises(TypeSafeError, match="omitted usage"):
        gemini_result(response)


def test_anthropic_result_joins_text_blocks_and_reads_usage() -> None:
    response = SimpleNamespace(
        stop_reason="end_turn",
        content=[
            SimpleNamespace(type="text", text='{"answers":'),
            SimpleNamespace(type="thinking", text="ignored"),
            SimpleNamespace(type="text", text=" {}}"),
        ],
        usage=SimpleNamespace(input_tokens=20, output_tokens=5),
    )
    result = anthropic_result(response)
    assert result.text == '{"answers": {}}'
    assert (result.input_tokens, result.output_tokens) == (20, 5)


@pytest.mark.parametrize("provider_class", [AnthropicProvider, AsyncAnthropicProvider])
@pytest.mark.parametrize("structured", [False, True])
@pytest.mark.parametrize("truncated", [False, True])
def test_anthropic_output_limit(
    monkeypatch: pytest.MonkeyPatch,
    provider_class: type[AnthropicProvider] | type[AsyncAnthropicProvider],
    structured: bool,
    truncated: bool,
) -> None:
    provider = provider_class("claude-haiku-4-5", max_tokens=8192)
    requests: list[dict[str, Any]] = []

    def create(**kwargs: Any) -> AnthropicMessage:
        requests.append(kwargs)
        return AnthropicMessage(
            id="msg-test",
            type="message",
            role="assistant",
            model=provider.model_name,
            stop_reason="max_tokens" if truncated else "end_turn",
            # Even syntactically valid JSON must not hide a truncated generation.
            content=[{"type": "text", "text": '{"answers":{"positive":true}}'}],
            usage={"input_tokens": 20, "output_tokens": 8192 if truncated else 10},
        )

    async def create_async(**kwargs: Any) -> AnthropicMessage:
        return create(**kwargs)

    retry = RetryPolicy(max_retries=2, backoff_initial=0)
    questions = {"positive": Noul(instructions="The review is positive.")}

    def evaluate() -> SystemOneResponse:
        if isinstance(provider, AsyncAnthropicProvider):
            monkeypatch.setattr(provider._client.messages, "create", create_async)

            async def run() -> SystemOneResponse:
                async with provider._client:
                    client = AsyncSystemOneAdapterClient(
                        structured_outputs=structured,
                        llm_answer_mode="discrete",
                        n_retry_malformed_structure=2,
                        retry=retry,
                        model=provider,
                    )
                    return await client.system_one("A delightful book.", questions)

            return asyncio.run(run())
        monkeypatch.setattr(provider._client.messages, "create", create)
        with provider._client:
            client = SystemOneAdapterClient(
                structured_outputs=structured,
                llm_answer_mode="discrete",
                n_retry_malformed_structure=2,
                retry=retry,
                model=provider,
            )
            return client.system_one("A delightful book.", questions)

    if truncated:
        with pytest.raises(TypeSafeError, match=r"truncated.*Increase max_tokens") as raised:
            evaluate()
        debug = raised.value.debug  # pyrefly: ignore[missing-attribute]
    else:
        response = evaluate()
        assert response.nouls["positive"].noul == 1.0
        debug = response.debug
    assert len(requests) == 1
    assert requests[0]["max_tokens"] == 8192
    assert len(debug["llm_attempts"]) == 1
    attempt = debug["llm_attempts"][0]
    assert attempt["request"] == requests[0]
    assert attempt["llm_response"]["content"][0]["text"] == '{"answers":{"positive":true}}'
    assert attempt["debug_info"]["finish_reason"] == ("max_tokens" if truncated else "end_turn")
    assert ("error" in attempt["debug_info"]) == truncated


@pytest.mark.parametrize("provider_class", [AnthropicProvider, AsyncAnthropicProvider])
@pytest.mark.parametrize("max_tokens", [0, -1])
def test_anthropic_rejects_nonpositive_output_limit(
    provider_class: type[AnthropicProvider] | type[AsyncAnthropicProvider],
    max_tokens: int,
) -> None:
    with pytest.raises(ValueError, match="max_tokens must be > 0"):
        provider_class("claude-haiku-4-5", max_tokens=max_tokens)


def test_build_providers_select_gemini() -> None:
    sync_provider = build_sync_provider("gemini", "gemini-3.8-flash")
    async_provider = build_async_provider("gemini", "gemini-3.8-flash")
    try:
        assert isinstance(sync_provider, GeminiProvider)
        assert isinstance(async_provider, AsyncGeminiProvider)
    finally:
        sync_provider.close()
        asyncio.run(async_provider.aclose())


def test_unknown_provider_is_rejected() -> None:
    with pytest.raises(ValueError, match="Unknown provider"):
        build_sync_provider("nope", "model")  # type: ignore[arg-type]
