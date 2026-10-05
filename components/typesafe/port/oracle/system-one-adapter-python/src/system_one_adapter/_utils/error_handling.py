"""Retry handling and reusable provider-error mapping.

The retry loop is a thin wrapper over the SDK's own tenacity policy: providers translate
their SDK exceptions to `typesafe_sdk.TypeSafeError` at the boundary, and the
policy's predicate decides which of those to retry. Each provider module owns which of
its SDK exceptions map where; `map_provider_error` gives them the shared
status/timeout/connection mapping so only the exception classes differ.
"""

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Literal, TypeVar

import httpx2
from tenacity import RetryCallState
from typesafe_sdk import (
    RetryPolicy,
    TypeSafeAPIConnectionError,
    TypeSafeAPITimeoutError,
    TypeSafeError,
)

# Reuse the SDK's own retry budgets, status overrides, predicates, and Retry-After.
from typesafe_sdk._core.errors import api_error
from typesafe_sdk._core.retry import build_tenacity, build_tenacity_async

ResultT = TypeVar("ResultT")


@dataclass(frozen=True)
class RetryReasons:
    """Reason for performing one retry.

    Attributes:
        category: Retry mechanism that requested another attempt.
        msg: Detailed retry cause.
    """

    category: Literal["provider_error", "malformed_structure"]
    msg: str


def map_provider_error(
    error: Exception,
    *,
    status_errors: tuple[type[Exception], ...],
    timeout_errors: tuple[type[Exception], ...],
    connection_errors: tuple[type[Exception], ...],
) -> TypeSafeError:
    """Map a provider SDK exception to an SDK error, preserving HTTP status and body.

    Args:
        error: Exception raised by a provider SDK.
        status_errors: Provider HTTP-status error classes carrying `status_code`,
            `body`, and `response`.
        timeout_errors: Provider timeout classes. Matched first because they
            usually subclass the connection error classes.
        connection_errors: Provider transport or connection error classes.

    Returns:
        The mapped SDK error, or the original error if it is already a TypeSafe error.
    """
    if isinstance(error, TypeSafeError):
        return error
    if isinstance(error, timeout_errors):
        return TypeSafeAPITimeoutError(httpx2.Timeout(None))
    if isinstance(error, status_errors):
        headers = httpx2.Headers(dict(error.response.headers))  # type: ignore[attr-defined]
        return api_error(error.status_code, error.body, headers)  # type: ignore[attr-defined]
    if isinstance(error, connection_errors):
        return TypeSafeAPIConnectionError(str(error))
    return TypeSafeError(str(error))


def _record_retry_reason(
    retry_reasons: list[RetryReasons],
) -> Callable[[RetryCallState], None]:
    def before_sleep(state: RetryCallState) -> None:
        error = state.outcome.exception() if state.outcome is not None else None
        retry_reasons.append(RetryReasons(category="provider_error", msg=str(error)))

    return before_sleep


def run_with_retries(
    function: Callable[[], ResultT],
    retry: RetryPolicy,
    retry_reasons: list[RetryReasons] | None = None,
) -> tuple[ResultT, int]:
    """Apply the SDK retry policy to a provider call that raises SDK errors."""
    retrying = build_tenacity(retry)
    if retry_reasons is not None:
        retrying.before_sleep = _record_retry_reason(retry_reasons)
    result = retrying(function)
    return result, retrying.statistics["attempt_number"] - 1


async def run_with_retries_async(
    function: Callable[[], Awaitable[ResultT]],
    retry: RetryPolicy,
    retry_reasons: list[RetryReasons] | None = None,
) -> tuple[ResultT, int]:
    """Apply the same SDK retry policy to asynchronous provider calls."""
    retrying = build_tenacity_async(retry)
    if retry_reasons is not None:
        retrying.before_sleep = _record_retry_reason(retry_reasons)

    # AsyncRetrying awaits a coroutine function; wrap so a plain callable that returns
    # an awaitable is driven to completion inside the retry.
    async def call() -> ResultT:
        return await function()

    result = await retrying(call)
    return result, retrying.statistics["attempt_number"] - 1
