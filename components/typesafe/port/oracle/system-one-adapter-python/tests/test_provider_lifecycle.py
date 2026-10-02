"""Provider reuse and ownership, with real SDK pools and no model API calls."""

import asyncio
from collections.abc import AsyncIterator, Iterator
from concurrent.futures import ThreadPoolExecutor
from concurrent.futures import TimeoutError as FutureTimeoutError
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from threading import Barrier, Event
from typing import Any
from unittest.mock import DEFAULT, AsyncMock, Mock

import pytest
from typesafe_sdk import TypeSafeError

from system_one_adapter import AsyncSystemOneAdapterClient, Noul, SystemOneAdapterClient
from system_one_adapter.providers import Message, ProviderResult
from system_one_adapter.providers.anthropic import (
    AnthropicProvider,
    AsyncAnthropicProvider,
)
from system_one_adapter.providers.base import record_request, render_messages
from system_one_adapter.providers.openai import AsyncOpenAIProvider, OpenAIProvider
from tests.test_client_with_fake_model import FakeAsyncProvider, FakeSyncProvider

QUESTIONS = {"positive": Noul(instructions="The review is positive.")}
RESULT = ProviderResult('{"answers":{"positive":true}}', 11, 7)


@dataclass
class Lifecycle:
    asynchronous: bool
    vendor: str
    providers: list[Any] = field(default_factory=list)

    def client(self, **kwargs: Any) -> Any:
        options: dict[str, Any] = {
            "structured_outputs": True,
            "llm_answer_mode": "discrete",
            "provider": self.vendor,
            "model": "test-model",
        }
        options.update(kwargs)
        client_class = AsyncSystemOneAdapterClient if self.asynchronous else SystemOneAdapterClient
        return client_class(**options)

    async def evaluate(self, client: Any, state: str = "Great book", **kwargs: Any) -> Any:
        response = client.system_one(state, QUESTIONS, **kwargs)
        return await response if self.asynchronous else response

    async def close(self, client: Any) -> None:
        if self.asynchronous:
            await client.aclose()
        else:
            client.close()

    @asynccontextmanager
    async def context(self, client: Any) -> AsyncIterator[Any]:
        if self.asynchronous:
            async with client:
                yield client
        else:
            with client:
                yield client


@pytest.fixture(params=[False, True], ids=["sync", "async"])
def lifecycle(request: pytest.FixtureRequest, monkeypatch: pytest.MonkeyPatch, vendor: str) -> Iterator[Lifecycle]:
    case = Lifecycle(request.param, vendor)

    def watch(provider_class: Any) -> None:
        original_init = provider_class.__init__

        def initialize(provider: Any, *args: Any, **kwargs: Any) -> None:
            original_init(provider, *args, **kwargs)
            spy = AsyncMock if case.asynchronous else Mock
            monkeypatch.setattr(provider._client, "close", spy(wraps=provider._client.close))
            case.providers.append(provider)

        def respond(provider: Any, messages: list[Message], **kwargs: Any) -> ProviderResult:
            record_request({"messages": render_messages(messages)}, api="offline-test")
            return RESULT

        async def respond_async(provider: Any, messages: list[Message], **kwargs: Any) -> ProviderResult:
            result = respond(provider, messages, **kwargs)
            await asyncio.sleep(0)
            return result

        monkeypatch.setattr(provider_class, "__init__", initialize)
        monkeypatch.setattr(provider_class, "request", respond_async if case.asynchronous else respond)

    for provider_class in (AsyncOpenAIProvider, AsyncAnthropicProvider) if case.asynchronous else (OpenAIProvider, AnthropicProvider):
        watch(provider_class)
    yield case

    # Retaining providers makes missed cleanup observable; release test pools even
    # when an assertion fails or a close spy has deliberately been made to raise.
    async def cleanup() -> None:
        for provider in case.providers:
            sdk = provider._client
            if not sdk.is_closed():
                result = type(sdk).close(sdk)
                if case.asynchronous:
                    await result

    asyncio.run(cleanup())


@pytest.fixture(params=["openai", "anthropic"])
def vendor(request: pytest.FixtureRequest) -> str:
    return request.param


def test_reuses_owned_provider_and_closes_sdk_on_context_exit(lifecycle: Lifecycle) -> None:
    async def run() -> None:
        client = lifecycle.client()
        assert lifecycle.providers == []
        async with lifecycle.context(client):
            for _ in range(3):
                response = await lifecycle.evaluate(client)
                assert response.nouls["positive"].noul == 1
            assert len(lifecycle.providers) == 1
            assert not lifecycle.providers[0]._client.is_closed()
        sdk = lifecycle.providers[0]._client
        assert sdk.is_closed()
        await lifecycle.close(client)
        sdk.close.assert_called_once()
        if lifecycle.asynchronous:
            sdk.close.assert_awaited_once()
        with pytest.raises(RuntimeError, match="closed"):
            await lifecycle.evaluate(client)
        with pytest.raises(RuntimeError, match="closed"):
            async with lifecycle.context(client):
                pytest.fail("Reentered a closed client")

    asyncio.run(run())


def test_cache_uses_resolved_provider_and_model_and_is_per_client(lifecycle: Lifecycle) -> None:
    async def run() -> None:
        async with lifecycle.context(lifecycle.client()) as client:
            await lifecycle.evaluate(client)
            await lifecycle.evaluate(client, model="test-model", provider=lifecycle.vendor)
            await lifecycle.evaluate(client, model="another-model")
            other_vendor = "anthropic" if lifecycle.vendor == "openai" else "openai"
            await lifecycle.evaluate(client, provider=other_vendor)
            assert len(lifecycle.providers) == 3
            async with lifecycle.context(lifecycle.client()) as second:
                await lifecycle.evaluate(second)
                assert len(lifecycle.providers) == 4
            assert all(not provider._client.is_closed() for provider in lifecycle.providers[:3])
        assert all(provider._client.is_closed() for provider in lifecycle.providers)

    asyncio.run(run())


@pytest.mark.parametrize("constructor_default", [False, True])
def test_injected_provider_is_borrowed(lifecycle: Lifecycle, constructor_default: bool) -> None:
    async def run() -> None:
        provider_class = (
            (AsyncOpenAIProvider if lifecycle.asynchronous else OpenAIProvider)
            if lifecycle.vendor == "openai"
            else (AsyncAnthropicProvider if lifecycle.asynchronous else AnthropicProvider)
        )
        injected: Any = provider_class("test-model")
        kwargs: dict[str, Any] = {} if constructor_default else {"model": injected}
        client = lifecycle.client(model=injected) if constructor_default else lifecycle.client()
        async with lifecycle.context(client):
            await lifecycle.evaluate(client, **kwargs)
            await lifecycle.evaluate(client, model="owned-model")
            await lifecycle.evaluate(client, **kwargs)
        assert not injected._client.is_closed()
        injected._client.close.assert_not_called()
        assert lifecycle.providers[1]._client.is_closed()
        with pytest.raises(RuntimeError, match="closed"):
            await lifecycle.evaluate(client, model=injected)
        if lifecycle.asynchronous:
            await injected.aclose()
        else:
            injected.close()
        assert injected._client.is_closed()

    asyncio.run(run())


def test_custom_provider_without_close_remains_supported(lifecycle: Lifecycle) -> None:
    async def run() -> None:
        provider_class = FakeAsyncProvider if lifecycle.asynchronous else FakeSyncProvider
        provider = provider_class({"answers": {"positive": True}})
        async with lifecycle.context(lifecycle.client(model=provider)) as client:
            await lifecycle.evaluate(client)
        assert len(provider.calls) == 1
        assert lifecycle.providers == []

    asyncio.run(run())


@pytest.mark.parametrize("failure", ["body", "request", "validation", "cancelled"])
def test_exceptional_exit_closes_owned_sdks(lifecycle: Lifecycle, monkeypatch: pytest.MonkeyPatch, failure: str) -> None:
    error_type = {"body": ValueError, "request": TypeSafeError, "validation": ValueError, "cancelled": asyncio.CancelledError}[failure]

    async def fail_in_context() -> None:
        async with lifecycle.context(lifecycle.client()) as client:
            if failure == "validation":
                response = client.system_one("document", {})
                if lifecycle.asynchronous:
                    await response
            else:
                await lifecycle.evaluate(client)
                if failure == "request":
                    mock_class = AsyncMock if lifecycle.asynchronous else Mock
                    monkeypatch.setattr(lifecycle.providers[0], "request", mock_class(side_effect=TypeSafeError("failed")))
                    await lifecycle.evaluate(client)
                else:
                    raise error_type("interrupted")

    async def run() -> None:
        with pytest.raises(error_type):
            await fail_in_context()
        assert len(lifecycle.providers) == 1
        assert lifecycle.providers[0]._client.is_closed()

    asyncio.run(run())


def test_cleanup_continues_after_failure(lifecycle: Lifecycle) -> None:
    async def run() -> None:
        client = lifecycle.client()
        await lifecycle.evaluate(client)
        await lifecycle.evaluate(client, model="another-model")
        await lifecycle.evaluate(client, model="third-model")
        first_error = RuntimeError("close failed")
        lifecycle.providers[0]._client.close.side_effect = first_error
        lifecycle.providers[1]._client.close.side_effect = ValueError("another close failed")
        with pytest.raises(RuntimeError, match="close failed") as caught:
            await lifecycle.close(client)
        assert caught.value is first_error
        assert lifecycle.providers[2]._client.is_closed()
        for provider in lifecycle.providers:
            provider._client.close.assert_called_once()
        with pytest.raises(RuntimeError, match="closed"):
            await lifecycle.evaluate(client)
        lifecycle.providers[0]._client.close.side_effect = None
        lifecycle.providers[1]._client.close.side_effect = None
        await lifecycle.close(client)
        await lifecycle.close(client)
        assert all(provider._client.is_closed() for provider in lifecycle.providers)
        assert [provider._client.close.call_count for provider in lifecycle.providers] == [2, 2, 1]

    asyncio.run(run())


def test_close_before_first_use_does_not_construct_providers(lifecycle: Lifecycle) -> None:
    async def run() -> None:
        client = lifecycle.client()
        await lifecycle.close(client)
        await lifecycle.close(client)
        with pytest.raises(RuntimeError, match="closed"):
            await lifecycle.evaluate(client)
        assert lifecycle.providers == []

    asyncio.run(run())


def test_failed_construction_is_not_cached(lifecycle: Lifecycle, monkeypatch: pytest.MonkeyPatch) -> None:
    async def run() -> None:
        async with lifecycle.context(lifecycle.client()) as client:
            factory = Mock(wraps=client._build_provider, side_effect=[ValueError("constructor failed"), DEFAULT])
            monkeypatch.setattr(client, "_build_provider", factory)
            with pytest.raises(ValueError, match="constructor failed"):
                await lifecycle.evaluate(client)
            assert lifecycle.providers == []
            await lifecycle.evaluate(client)
            await lifecycle.evaluate(client)
            assert factory.call_count == 2
            assert len(lifecycle.providers) == 1

    asyncio.run(run())


def test_environment_is_captured_on_first_use(lifecycle: Lifecycle, monkeypatch: pytest.MonkeyPatch) -> None:
    async def run() -> None:
        prefix = lifecycle.vendor.upper()
        client = lifecycle.client()
        monkeypatch.setenv(f"{prefix}_API_KEY", "first-test-key")
        monkeypatch.setenv(f"{prefix}_BASE_URL", "https://first.invalid/v1")
        async with lifecycle.context(client):
            await lifecycle.evaluate(client)
            monkeypatch.setenv(f"{prefix}_API_KEY", "second-test-key")
            monkeypatch.setenv(f"{prefix}_BASE_URL", "https://second.invalid/v1")
            await lifecycle.evaluate(client)
            assert len(lifecycle.providers) == 1
            sdk = lifecycle.providers[0]._client
            assert sdk.api_key == "first-test-key"
            assert sdk.base_url.host == "first.invalid"
        async with lifecycle.context(lifecycle.client()) as fresh:
            await lifecycle.evaluate(fresh)
            sdk = lifecycle.providers[1]._client
            assert sdk.api_key == "second-test-key"
            assert sdk.base_url.host == "second.invalid"

    asyncio.run(run())


@pytest.mark.parametrize("cleanup_failure", [None, "before", "after"])
def test_async_cleanup_propagates_cancellation_after_remaining_cleanup(monkeypatch: pytest.MonkeyPatch, cleanup_failure: str | None) -> None:
    async def run() -> None:
        client = AsyncSystemOneAdapterClient(structured_outputs=True, llm_answer_mode="discrete", provider="openai")
        first = FakeAsyncProvider(RESULT.text)
        second = FakeAsyncProvider(RESULT.text)
        third = FakeAsyncProvider(RESULT.text)
        closing = asyncio.Event()

        async def wait_for_cancellation() -> None:
            closing.set()
            await asyncio.Event().wait()

        first_close = AsyncMock(side_effect=RuntimeError("first close failed") if cleanup_failure == "before" else None)
        cancelled_close = AsyncMock(side_effect=wait_for_cancellation)
        third_close = AsyncMock(side_effect=RuntimeError("last close failed") if cleanup_failure == "after" else None)
        monkeypatch.setattr(first, "aclose", first_close, raising=False)
        monkeypatch.setattr(second, "aclose", cancelled_close, raising=False)
        monkeypatch.setattr(third, "aclose", third_close, raising=False)
        monkeypatch.setattr(client, "_build_provider", Mock(side_effect=[first, second, third]))
        await client.system_one("document", QUESTIONS, model="first")
        await client.system_one("document", QUESTIONS, model="second")
        await client.system_one("document", QUESTIONS, model="third")
        task = asyncio.create_task(client.aclose())
        await asyncio.wait_for(closing.wait(), timeout=10)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert task.cancelled()
        first_close.assert_awaited_once()
        cancelled_close.assert_awaited_once()
        third_close.assert_awaited_once()
        first_close.side_effect = None
        cancelled_close.side_effect = None
        third_close.side_effect = None
        await client.aclose()
        await client.aclose()
        assert first_close.await_count == (2 if cleanup_failure == "before" else 1)
        assert cancelled_close.await_count == 2
        assert third_close.await_count == (2 if cleanup_failure == "after" else 1)

    asyncio.run(run())


@pytest.mark.parametrize("fails", [False, True], ids=["success", "failure"])
def test_concurrent_close_waits_for_same_cleanup(lifecycle: Lifecycle, fails: bool) -> None:
    async def run() -> None:
        client = lifecycle.client()
        await lifecycle.evaluate(client)
        sdk = lifecycle.providers[0]._client
        error = RuntimeError("close failed")
        if lifecycle.asynchronous:
            started = asyncio.Event()
            release = asyncio.Event()

            async def close_async() -> Any:
                started.set()
                await release.wait()
                if fails:
                    raise error
                return DEFAULT

            sdk.close.side_effect = close_async
            first = asyncio.create_task(client.aclose())
            await asyncio.wait_for(started.wait(), timeout=10)
            second = asyncio.create_task(client.aclose())
            try:
                await asyncio.sleep(0)
                assert not second.done()
            finally:
                release.set()
                results = list(await asyncio.gather(first, second, return_exceptions=True))
        else:
            started_sync = Event()
            release_sync = Event()

            def close_sync() -> Any:
                started_sync.set()
                assert release_sync.wait(timeout=10)
                if fails:
                    raise error
                return DEFAULT

            sdk.close.side_effect = close_sync
            with ThreadPoolExecutor(max_workers=2) as executor:
                first_sync = executor.submit(client.close)
                try:
                    assert started_sync.wait(timeout=10)
                    second_sync = executor.submit(client.close)
                    with pytest.raises(FutureTimeoutError):
                        second_sync.result(timeout=0.05)
                finally:
                    release_sync.set()
                results = [future.exception(timeout=10) for future in (first_sync, second_sync)]
        assert results == ([error, error] if fails else [None, None])
        sdk.close.assert_called_once()
        assert sdk.is_closed() is not fails

    asyncio.run(run())


def test_cancelling_close_waiter_does_not_interrupt_cleanup(monkeypatch: pytest.MonkeyPatch) -> None:
    async def run() -> None:
        client = AsyncSystemOneAdapterClient(structured_outputs=True, llm_answer_mode="discrete", provider="openai")
        provider = FakeAsyncProvider(RESULT.text)
        started = asyncio.Event()
        release = asyncio.Event()

        async def close() -> None:
            started.set()
            await release.wait()

        close_mock = AsyncMock(side_effect=close)
        monkeypatch.setattr(provider, "aclose", close_mock, raising=False)
        monkeypatch.setattr(client, "_build_provider", Mock(return_value=provider))
        await client.system_one("document", QUESTIONS, model="test")
        owner = asyncio.create_task(client.aclose())
        await asyncio.wait_for(started.wait(), timeout=10)
        waiter = asyncio.create_task(client.aclose())
        try:
            await asyncio.sleep(0)
            waiter.cancel()
            with pytest.raises(asyncio.CancelledError):
                await waiter
            assert not owner.done()
            another_waiter = asyncio.create_task(client.aclose())
            await asyncio.sleep(0)
            assert not another_waiter.done()
        finally:
            release.set()
            await owner
        await another_waiter
        await client.aclose()
        close_mock.assert_awaited_once()

    asyncio.run(run())


def test_concurrent_first_use_reuses_pool_and_isolates_traces(lifecycle: Lifecycle, monkeypatch: pytest.MonkeyPatch) -> None:
    async def run() -> None:
        provider_class = (
            (AsyncOpenAIProvider if lifecycle.asynchronous else OpenAIProvider)
            if lifecycle.vendor == "openai"
            else (AsyncAnthropicProvider if lifecycle.asynchronous else AnthropicProvider)
        )
        original_sync_request = (OpenAIProvider if lifecycle.vendor == "openai" else AnthropicProvider).request
        original_async_request = (AsyncOpenAIProvider if lifecycle.vendor == "openai" else AsyncAnthropicProvider).request
        request_barrier = Barrier(8)
        all_started = asyncio.Event()
        started = 0

        def request(provider: Any, messages: list[Message], **kwargs: Any) -> ProviderResult:
            request_barrier.wait(timeout=10)
            return original_sync_request(provider, messages, **kwargs)

        async def request_async(provider: Any, messages: list[Message], **kwargs: Any) -> ProviderResult:
            nonlocal started
            started += 1
            if started == 8:
                all_started.set()
            await asyncio.wait_for(all_started.wait(), timeout=10)
            return await original_async_request(provider, messages, **kwargs)

        monkeypatch.setattr(provider_class, "request", request_async if lifecycle.asynchronous else request)
        async with lifecycle.context(lifecycle.client()) as client:
            if lifecycle.asynchronous:
                responses = await asyncio.gather(*(lifecycle.evaluate(client, f"document-{i}") for i in range(8)))
            else:
                barrier = Barrier(8)

                def evaluate(index: int) -> Any:
                    barrier.wait(timeout=10)
                    return client.system_one(f"document-{index}", QUESTIONS)

                with ThreadPoolExecutor(max_workers=8) as executor:
                    responses = list(executor.map(evaluate, range(8)))
            assert len(lifecycle.providers) == 1
            for index, response in enumerate(responses):
                attempts = response.debug["llm_attempts"]
                assert len(attempts) == 1
                assert f"document-{index}" in attempts[0]["messages"][1]["content"]
                assert attempts[0]["request"]["messages"] == attempts[0]["messages"]
                assert response.usage.input_tokens_total == 11
                assert response.usage.n_retries == 0

    asyncio.run(run())
