import { afterEach, describe, expect, it, vi } from "vitest";
import { describeRuntime, isBrowser } from "../src/runtime";

describe("describeRuntime", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reports node with platform and arch here", () => {
    expect(describeRuntime()).toMatch(/^node\/\d+\.\d+\.\d+ \(\w+; \w+\)$/);
  });

  it("prefers Bun over node when both are present", () => {
    vi.stubGlobal("Bun", { version: "1.2.3" });
    expect(describeRuntime()).toMatch(/^bun\/1\.2\.3 \(\w+; \w+\)$/);
  });

  it("recognizes Deno", () => {
    vi.stubGlobal("Deno", { version: { deno: "2.1.0" } });
    expect(describeRuntime()).toMatch(/^deno\/2\.1\.0/);
  });

  it("recognizes edge runtimes by their markers", () => {
    vi.stubGlobal("EdgeRuntime", "edge-runtime");
    expect(describeRuntime()).toBe("vercel-edge");
    vi.unstubAllGlobals();
    vi.stubGlobal("navigator", { userAgent: "Cloudflare-Workers" });
    expect(describeRuntime()).toBe("cloudflare-workers");
  });

  it("falls back to browser, then unknown", () => {
    vi.stubGlobal("process", undefined);
    vi.stubGlobal("window", { document: {} });
    vi.stubGlobal("navigator", { userAgent: "Mozilla/5.0" });
    expect(isBrowser()).toBe(true);
    expect(describeRuntime()).toBe("browser");
    vi.stubGlobal("window", undefined);
    expect(isBrowser()).toBe(false);
    expect(describeRuntime()).toBe("unknown");
  });
});
