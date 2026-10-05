"""Native Gemini Interactions API providers."""

from __future__ import annotations

from typing import Any

from google.genai import Client
from google.genai._gaos.lib import compat_errors
from typesafe_sdk import TypeSafeError

from system_one_adapter._utils.error_handling import map_provider_error
from system_one_adapter.providers.base import (
    Message,
    ProviderResult,
    record_request,
    record_response,
    translating,
)


class _GeminiErrors:
    """Shared Gemini-SDK error translation for the sync and async providers."""

    @staticmethod
    def translate_error(error: Exception) -> TypeSafeError:
        """Map a Gemini SDK exception to an SDK error."""
        return map_provider_error(
            error,
            status_errors=(compat_errors.APIStatusError,),
            timeout_errors=(compat_errors.APITimeoutError,),
            connection_errors=(compat_errors.APIConnectionError,),
        )


def _request_kwargs(
    model_name: str,
    messages: list[Message],
    schema: dict[str, Any],
    *,
    structured: bool,
) -> dict[str, Any]:
    system = "\n\n".join(message.content for message in messages if message.role == "system")
    conversation = [
        {
            "type": "model_output" if message.role == "assistant" else "user_input",
            "content": [{"type": "text", "text": message.content}],
        }
        for message in messages
        if message.role != "system"
    ]
    kwargs: dict[str, Any] = {
        "model": model_name,
        "input": conversation,
        "store": False,
    }
    if system:
        kwargs["system_instruction"] = system
    if structured:
        kwargs["response_format"] = {
            "type": "text",
            "mime_type": "application/json",
            "schema": schema,
        }
    return kwargs


def _token_count(usage: Any, field: str) -> int:
    value = getattr(usage, field, None)
    if value is None:
        raise TypeSafeError("Gemini response omitted usage.")
    return int(value)


def _result(response: Any) -> ProviderResult:
    record_response(response, finish_reason=getattr(response, "status", None))
    status = getattr(response, "status", None)
    if status != "completed":
        reason = status or "unknown"
        errors = getattr(response, "errors", None)
        if errors:
            reason = str(errors)
        raise TypeSafeError(f"Gemini response did not complete: {reason}.")
    usage = getattr(response, "usage", None)
    if usage is None:
        raise TypeSafeError("Gemini response omitted usage.")
    return ProviderResult(
        text=response.output_text or "",
        input_tokens=_token_count(usage, "total_input_tokens"),
        output_tokens=_token_count(usage, "total_output_tokens"),
    )


class GeminiProvider(_GeminiErrors):
    """Synchronously call the Gemini Interactions API."""

    def __init__(self, model_name: str, *, api_key: str | None = None) -> None:
        """Initialize the model and synchronous SDK client.

        Args:
            model_name: Gemini model to request.
            api_key: Gemini API key. Defaults to the SDK's own resolution,
                including `GEMINI_API_KEY` and `GOOGLE_API_KEY`.
        """
        self.model_name = model_name
        self._client = Client(api_key=api_key)
        # Interactions interprets HttpRetryOptions.attempts as extra retries, unlike
        # generateContent. Disable its own retry policy so RetryPolicy owns all calls.
        self._client.interactions.sdk_configuration.retry_config = None

    def close(self) -> None:
        """Release the SDK client's connection pool."""
        self._client.close()

    def request(
        self,
        messages: list[Message],
        *,
        schema: dict[str, Any],
        structured: bool,
    ) -> ProviderResult:
        """Perform one Interactions request and return its raw payload and usage.

        Args:
            messages: Conversation in provider-neutral form.
            schema: JSON schema for the model's answer.
            structured: Whether to use native structured output.

        Returns:
            The response text and input and output token counts.

        Raises:
            TypeSafeError: The SDK request fails, the interaction does not
                complete, or the response omits usage.
        """
        with translating(self.translate_error):
            kwargs = _request_kwargs(self.model_name, messages, schema, structured=structured)
            record_request(kwargs, api="interactions")
            response = self._client.interactions.create(**kwargs)
        return _result(response)


class AsyncGeminiProvider(_GeminiErrors):
    """Asynchronously call the Gemini Interactions API."""

    def __init__(self, model_name: str, *, api_key: str | None = None) -> None:
        """Initialize the model and asynchronous SDK client.

        Args:
            model_name: Gemini model to request.
            api_key: Gemini API key. Defaults to the SDK's own resolution,
                including `GEMINI_API_KEY` and `GOOGLE_API_KEY`.
        """
        self.model_name = model_name
        self._client = Client(api_key=api_key)
        # See GeminiProvider: Interactions has a separate SDK retry policy.
        self._client.aio.interactions.sdk_configuration.retry_config = None

    async def aclose(self) -> None:
        """Release the SDK client's connection pools."""
        await self._client.aio.aclose()
        self._client.close()

    async def request(
        self,
        messages: list[Message],
        *,
        schema: dict[str, Any],
        structured: bool,
    ) -> ProviderResult:
        """Perform one Interactions request and return its raw payload and usage.

        Args:
            messages: Conversation in provider-neutral form.
            schema: JSON schema for the model's answer.
            structured: Whether to use native structured output.

        Returns:
            The response text and input and output token counts.

        Raises:
            TypeSafeError: The SDK request fails, the interaction does not
                complete, or the response omits usage.
        """
        with translating(self.translate_error):
            kwargs = _request_kwargs(self.model_name, messages, schema, structured=structured)
            record_request(kwargs, api="interactions")
            response = await self._client.aio.interactions.create(**kwargs)
        return _result(response)
