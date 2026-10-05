import { describe, expect, it, vi } from "vitest";

import {
  ConfigIssueReporter,
  type ConfigIssueSource,
} from "#src/config/config-issue-reporter";

// ── Helpers ────────────────────────────────────────────────────────────────

/**
 * A source whose answer can be reassigned between reports, modeling a config
 * re-read that finds a different set of issues than the previous turn did.
 */
function makeSource(initial: readonly string[] = []): ConfigIssueSource & {
  setIssues(next: readonly string[]): void;
} {
  let issues = initial;
  return {
    getConfigIssues: () => issues,
    setIssues(next) {
      issues = next;
    },
  };
}

function makeReporter(initial: readonly string[] = []) {
  const source = makeSource(initial);
  const log = { warn: vi.fn<(message: string) => void>() };
  return { reporter: new ConfigIssueReporter(source, log), source, log };
}

const ISSUE_A = "config issue A";
const ISSUE_B = "config issue B";

// ── Tests ──────────────────────────────────────────────────────────────────

describe("ConfigIssueReporter", () => {
  describe("a first report", () => {
    it("warns the one issue the source answers", () => {
      const { reporter, log } = makeReporter([ISSUE_A]);
      reporter.report();
      expect(log.warn).toHaveBeenCalledExactlyOnceWith(ISSUE_A);
    });

    it("warns several issues as one message, newline-joined", () => {
      const { reporter, log } = makeReporter([ISSUE_A, ISSUE_B]);
      reporter.report();
      expect(log.warn).toHaveBeenCalledExactlyOnceWith(
        `${ISSUE_A}\n${ISSUE_B}`,
      );
    });

    it("says nothing when the config has no issues", () => {
      const { reporter, log } = makeReporter([]);
      reporter.report();
      expect(log.warn).not.toHaveBeenCalled();
    });
  });

  describe("a repeated report", () => {
    it("does not re-warn an issue that has not changed", () => {
      const { reporter, log } = makeReporter([ISSUE_A]);
      reporter.report();
      reporter.report();
      expect(log.warn).toHaveBeenCalledExactlyOnceWith(ISSUE_A);
    });

    it("warns only the issue that newly appeared", () => {
      const { reporter, source, log } = makeReporter([ISSUE_A]);
      reporter.report();
      source.setIssues([ISSUE_A, ISSUE_B]);
      reporter.report();
      expect(log.warn).toHaveBeenCalledTimes(2);
      expect(log.warn).toHaveBeenLastCalledWith(ISSUE_B);
    });

    it("warns again an issue that went away and came back", () => {
      const { reporter, source, log } = makeReporter([ISSUE_A]);
      reporter.report();
      source.setIssues([]);
      reporter.report();
      source.setIssues([ISSUE_A]);
      reporter.report();
      expect(log.warn).toHaveBeenCalledTimes(2);
      expect(log.warn).toHaveBeenLastCalledWith(ISSUE_A);
    });
  });
});
