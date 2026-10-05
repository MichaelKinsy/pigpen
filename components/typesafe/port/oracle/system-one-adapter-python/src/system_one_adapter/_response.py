"""SDK response types extended with LLM accounting and diagnostics.

Both models subclass the Pydantic SDK types, so a caller serializes them the same way
as any SDK response: `response.model_dump()` or `response.model_dump_json()`. The
`debug` field holds plain JSON-compatible builtins, so no custom serialization is
needed.
"""

from typing import Any

from typesafe_sdk import SystemOneResponse as SDKSystemOneResponse
from typesafe_sdk import Usage as SDKUsage


class Usage(SDKUsage):
    """Keep final-attempt usage alongside cumulative retry accounting."""

    input_tokens_total: int | None
    output_tokens_total: int | None
    n_retries: int
    n_retries_malformed_structure: int
    latency: float


class SystemOneResponse(SDKSystemOneResponse):
    """SDK answers and typed views with retry and probability diagnostics."""

    usage: Usage
    debug: dict[str, Any]
