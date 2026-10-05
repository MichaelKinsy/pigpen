"""HTTP responses matching the transport used by the installed provider SDK."""

from typing import Any

import httpx
import httpx2


def json_response(request: httpx.Request | httpx2.Request, payload: Any, *, status: int = 200) -> httpx.Response | httpx2.Response:
    if isinstance(request, httpx2.Request):
        return httpx2.Response(status, request=request, json=payload)
    return httpx.Response(status, request=request, json=payload)
