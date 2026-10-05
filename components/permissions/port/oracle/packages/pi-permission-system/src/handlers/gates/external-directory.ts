import { capabilitySurfaceForTool } from "#src/access-intent/path-surfaces";
import { getToolInputPath } from "#src/access-intent/tool-input-path";
import type { PathNormalizer } from "#src/path/path-normalizer";
import type { InfrastructureReadScope } from "#src/path/pi-infrastructure-read";
import type { ScopedPermissionResolver } from "#src/policy/permission-resolver";
import { buildExternalDirectoryAskPayload } from "#src/presentation/path-ask-payload";
import { SessionApproval } from "#src/session/session-approval";
import type { ToolAccessExtractorLookup } from "#src/tool-input/tool-access-extractor-registry";
import type { PermissionCheckResult } from "#src/types";
import type { GateResult } from "./descriptor";
import { resolveExternalDirectoryPolicy } from "./external-directory-policy";
import {
  accessFactsFromPath,
  buildPathGateLogContext,
  buildPathGatePromptDetails,
} from "./helpers";
import type { ToolCallContext } from "./types";

/**
 * Build a pure descriptor for the external-directory permission gate.
 *
 * Returns `null` when the gate does not apply (no CWD, tool is not
 * path-bearing, or path is inside the working directory).
 * Returns a `GateBypass` for Pi infrastructure reads, unless a deny rule
 * naming the path outranks it.
 * Returns a `GateDescriptor` for external paths needing a permission check.
 */
export function describeExternalDirectoryGate(
  tcc: ToolCallContext,
  infraScope: InfrastructureReadScope,
  resolver: ScopedPermissionResolver,
  normalizer: PathNormalizer,
  extractors?: ToolAccessExtractorLookup,
): GateResult {
  const { path: externalDirectoryPath, source: pathSource } = getToolInputPath(
    tcc.toolName,
    tcc.input,
    extractors,
  );
  if (!externalDirectoryPath) return null;

  // The boundary decision and the infrastructure-read containment check use
  // the canonical, symlink-resolved path; pattern matching uses the typed and
  // resolved aliases (#418). A built-in tool's path is the file Pi's resolver
  // opens, so a `file://` spelling of an outside file is judged outside.
  const accessPath = normalizer.forToolPath(
    tcc.toolName,
    externalDirectoryPath,
  );
  if (
    !normalizer.isBoundaryOutsideWorkingDirectory(accessPath.boundaryValue())
  ) {
    return null;
  }

  // The narrowest `external_directory`-family surface this tool's identity
  // proves; the bare family name folds both directions (ADR 0013 §10).
  const surface = capabilitySurfaceForTool("external_directory", tcc.toolName);

  // Resolved before the infrastructure bypass so a rule can be weighed against
  // it; the runner consumes this preCheck and skips its own resolve.
  const preCheck = resolveExternalDirectoryPolicy(
    accessPath,
    resolver,
    surface,
    tcc.agentName ?? undefined,
  );

  // ── Pi infrastructure read bypass ──────────────────────────────────────
  if (
    normalizer.isInfrastructureRead(tcc.toolName, accessPath, infraScope) &&
    !isTargetedDeny(preCheck)
  ) {
    return {
      action: "allow",
      // Containment allowed this, not a rule the operator wrote.
      decidedBy: { kind: "infrastructure_read" },
      log: {
        event: "permission_request.infrastructure_auto_allowed",
        details: buildPathGateLogContext(
          tcc,
          externalDirectoryPath,
          pathSource,
        ),
      },
      decision: {
        surface: tcc.toolName,
        value: externalDirectoryPath,
        result: "allow",
        resolution: "infrastructure_auto_allowed",
        origin: null,
        agentName: tcc.agentName ?? null,
        matchedPattern: null,
      },
    };
  }

  // ── Build descriptor for permission check ───────────────────────────────
  const resolvedAlias = accessPath.resolvedAlias();
  const patterns = normalizer.approvalPatternsFor(accessPath);

  const payload = buildExternalDirectoryAskPayload({
    toolName: tcc.toolName,
    pathValue: externalDirectoryPath,
    resolvedPath: resolvedAlias,
    cwd: tcc.cwd,
    agentName: tcc.agentName,
    matchedPattern: preCheck.matchedPattern,
    surface,
  });

  return {
    surface,
    input: {},
    preCheck,
    payload,
    sessionApproval: SessionApproval.forPatterns(surface, patterns),
    promptDetails: buildPathGatePromptDetails(
      tcc,
      externalDirectoryPath,
      accessFactsFromPath(surface, accessPath),
    ),
    logContext: buildPathGateLogContext(tcc, externalDirectoryPath, pathSource),
    decision: {
      surface,
      value: externalDirectoryPath,
    },
  };
}

/**
 * True for a deny from a rule naming this path: not the family's catch-all
 * (`"*"`, or `"**"`, which compiles identically), and not the universal
 * fallback (no matched pattern). Only such a deny
 * outranks the Pi infrastructure read bypass, so a deny-by-default policy keeps
 * its skill and package reads.
 */
function isTargetedDeny(check: PermissionCheckResult): boolean {
  return (
    check.state === "deny" &&
    check.matchedPattern !== undefined &&
    !CATCH_ALL_PATTERN.test(check.matchedPattern)
  );
}

/** A pattern of wildcards alone, which matches every path. */
const CATCH_ALL_PATTERN = /^\*+$/;
