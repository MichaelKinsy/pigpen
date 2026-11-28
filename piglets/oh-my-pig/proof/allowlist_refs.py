#!/usr/bin/env python3
"""Shared line-independent reference resolver for all proof allowlists.

Every allowlist entry replaces brittle `file:LINE` references with:
  - `ref_file`: relative path under the repo root, the extension root
    (`components/oh-my-pig/extensions/oh-my-pig/`), or `PiG <path>` under the
    external PiG checkout (`$PIG_SRC`, or alias `$PIG_CHECKOUT_DIR`; when
    neither is set, `PiG <path>` refs skip resolution with a visible note).
  - `ref_term`: a distinctive phrase (string) or non-empty list of phrases that
    must ALL occur on the SAME line of `ref_file`.
  - optional `ref_subject`: string or list of strings where at least ONE must
    occur on the resolved line (case-insensitive) to verify the cited row names
    the differing field/feature rather than an unrelated row. Callers may also
    pass a `subject_extractor` callback to `resolve_allowlist_entries`.

At load time, `resolve_entry_ref` resolves `(ref_file, ref_term)` to a single
unique line number:
  - 0 matching lines  -> raises `AllowlistRefError`
  - >1 matching lines -> raises `AllowlistRefError` (ambiguous; add a disambiguating term)
  - 1 matching line   -> populates `entry["ref"] = f"{ref_file}:{line_no}"`

For `PiG <path>` entries when `$PIG_SRC`/`$PIG_CHECKOUT_DIR` is unset or does not
exist on the machine, resolution skips with a visible note
(`entry["ref"] = f"{ref_file} (PiG checkout absent)"` and
`entry["ref_unverified"] = True`), printing a one-time notice to stderr.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Any, Callable

PROOF_DIR = Path(__file__).resolve().parent
REPO_ROOT = PROOF_DIR.parent.parent.parent
EXT_ROOT = REPO_ROOT / "components" / "oh-my-pig" / "extensions" / "oh-my-pig"


def default_pig_root() -> Path | None:
    """Resolve external PiG checkout from PIG_SRC (or alias PIG_CHECKOUT_DIR); None when unset."""
    raw = (os.environ.get("PIG_SRC") or os.environ.get("PIG_CHECKOUT_DIR") or "").strip()
    return Path(raw) if raw else None


# Backward-compatible module attribute (None when PIG_SRC/PIG_CHECKOUT_DIR are unset).
DEFAULT_PIG_ROOT: Path | None = default_pig_root()

_WARNED_MISSING_PIG = False

class AllowlistRefError(ValueError):
    """Raised when an allowlist entry's (ref_file, ref_term) cannot be resolved uniquely."""


def _normalize_terms(ref_term: Any, ctx: str) -> list[str]:
    if isinstance(ref_term, str):
        t = ref_term.strip()
        if not t:
            raise AllowlistRefError(f"{ctx}: ref_term must be a non-empty string or list of non-empty strings")
        return [t]
    if isinstance(ref_term, list) and ref_term and all(isinstance(x, str) and x.strip() for x in ref_term):
        return [x.strip() for x in ref_term]
    raise AllowlistRefError(f"{ctx}: ref_term must be a non-empty string or list of non-empty strings, got {ref_term!r}")


def _locate_ref_file(
    ref_file: str, *, repo_root: Path, ext_root: Path, pig_root: Path | None
) -> tuple[Path | None, bool]:
    """Return `(resolved_path, is_external_pig_missing)`."""
    if ref_file.startswith("PiG "):
        rel = ref_file[4:].strip()
        if pig_root is None or not pig_root.is_dir():
            return None, True
        return pig_root / rel, False
    p_repo = repo_root / ref_file
    if p_repo.is_file():
        return p_repo, False
    p_ext = ext_root / ref_file
    if p_ext.is_file():
        return p_ext, False
    return p_repo, False

def _normalize_subject_candidates(subject: Any, ctx: str) -> list[str]:
    if subject is None:
        return []
    if isinstance(subject, str):
        t = subject.strip()
        if not t:
            raise AllowlistRefError(f"{ctx}: ref_subject must not be empty")
        return [t]
    if isinstance(subject, list) and subject and all(isinstance(x, str) and x.strip() for x in subject):
        return [x.strip() for x in subject]
    raise AllowlistRefError(
        f"{ctx}: ref_subject must be a non-empty string or list of non-empty strings, got {subject!r}"
    )


def resolve_ref(
    ref_file: str,
    ref_term: str | list[str],
    *,
    context: str = "allowlist",
    repo_root: Path | None = None,
    ext_root: Path | None = None,
    pig_root: Path | None = None,
    line_cache: dict[Path, list[str]] | None = None,
    required_subject: str | list[str] | None = None,
) -> tuple[str, int | None, bool]:
    """Resolve `(ref_file, ref_term)` -> `(formatted_ref, line_no_or_None, unverified)`.

    Raises `AllowlistRefError` on missing non-external files, 0 matches, >1 matches,
    or when `required_subject` is given and none of its candidate terms appear on the
    resolved line.
    """
    global _WARNED_MISSING_PIG
    if not isinstance(ref_file, str) or not ref_file.strip():
        raise AllowlistRefError(f"{context}: ref_file must be a non-empty string")
    if ":" in ref_file:
        raise AllowlistRefError(f"{context}: ref_file must not hardcode line numbers: {ref_file!r}")

    terms = _normalize_terms(ref_term, context)
    subjects = _normalize_subject_candidates(required_subject, context)
    r_root = repo_root or REPO_ROOT
    e_root = ext_root or EXT_ROOT
    p_root = pig_root if pig_root is not None else default_pig_root()

    fpath, pig_missing = _locate_ref_file(ref_file, repo_root=r_root, ext_root=e_root, pig_root=p_root)
    if pig_missing:
        if not _WARNED_MISSING_PIG:
            where = str(p_root) if p_root is not None else "PIG_SRC unset"
            sys.stderr.write(
                f"note: PiG checkout not found ({where}); skipping ref_term resolution for PiG host refs\n"
            )
            _WARNED_MISSING_PIG = True
        return f"{ref_file} (PiG checkout absent)", None, True
    if fpath is None or not fpath.is_file():
        raise AllowlistRefError(f"{context}: ref_file not found: {ref_file!r} (looked in {r_root} and {e_root})")

    if line_cache is not None and fpath in line_cache:
        lines = line_cache[fpath]
    else:
        lines = fpath.read_text(encoding="utf-8").splitlines()
        if line_cache is not None:
            line_cache[fpath] = lines

    lower_terms = [t.lower() for t in terms]
    hits: list[int] = []
    for idx, line in enumerate(lines, start=1):
        low = line.lower()
        if all(t in low for t in lower_terms):
            hits.append(idx)

    if not hits:
        raise AllowlistRefError(
            f"{context}: no line in {ref_file!r} contains all ref_term(s) {terms!r}"
        )
    if len(hits) > 1:
        preview = ", ".join(f"L{n}" for n in hits[:6])
        raise AllowlistRefError(
            f"{context}: ambiguous ref_term {terms!r} matched {len(hits)} lines in {ref_file!r} ({preview}); "
            "add a disambiguating term to ref_term"
        )

    line_no = hits[0]
    resolved_line = lines[line_no - 1]
    if subjects:
        low_line = resolved_line.lower()
        if not any(s.lower() in low_line for s in subjects):
            raise AllowlistRefError(
                f"{context}: resolved line {ref_file}:{line_no} does not name the differing "
                f"field/subject {subjects!r}; line was: {resolved_line.strip()!r}"
            )
    return f"{ref_file}:{line_no}", line_no, False


def resolve_allowlist_entries(
    entries: list[dict[str, Any]],
    *,
    source_name: str,
    repo_root: Path | None = None,
    ext_root: Path | None = None,
    pig_root: Path | None = None,
    subject_extractor: Callable[[dict[str, Any]], str | list[str] | None] | None = None,
) -> list[dict[str, Any]]:
    """Validate and resolve `ref_file` + `ref_term` on every entry in a list, attaching `ref` and `ref_line`.

    Rejects legacy hardcoded `ref` entries that omit `ref_file`/`ref_term`.
    When an entry specifies `ref_subject` or `subject_extractor` returns non-None,
    verifies that the resolved line names the differing field/subject.
    """
    cache: dict[Path, list[str]] = {}
    out: list[dict[str, Any]] = []
    for i, raw in enumerate(entries):
        if not isinstance(raw, dict):
            raise AllowlistRefError(f"{source_name}[{i}]: entry must be a JSON object")
        ctx = f"{source_name}[{i}]"
        if "ref" in raw and ("ref_file" not in raw or "ref_term" not in raw):
            raise AllowlistRefError(
                f"{ctx}: hardcoded 'ref' is no longer allowed; use 'ref_file' and 'ref_term'"
            )
        ref_file = raw.get("ref_file")
        ref_term = raw.get("ref_term")
        req_subj = raw.get("ref_subject")
        if req_subj is None and subject_extractor is not None:
            req_subj = subject_extractor(raw)
        formatted_ref, line_no, unverified = resolve_ref(
            ref_file,
            ref_term,
            context=ctx,
            repo_root=repo_root,
            ext_root=ext_root,
            pig_root=pig_root,
            line_cache=cache,
            required_subject=req_subj,
        )
        resolved = dict(raw)
        resolved["ref"] = formatted_ref
        resolved["ref_line"] = line_no
        if unverified:
            resolved["ref_unverified"] = True
        out.append(resolved)
    return out
