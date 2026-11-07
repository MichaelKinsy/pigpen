// Measurement-only wrapper (never shipped): port/perf/run.sh generates a scratch entry that imports this file and
// re-exports the extension under test. On SIGUSR2 it writes process.memoryUsage() and V8 heap statistics to
// $PH_PERF_MEM_DIR/<pid>-<seq>.json: once as the process stands ("asis") and once after a forced full GC ("gc").
// SIGUSR1 is not used: Node reserves it for the inspector.
import { writeFileSync, renameSync } from "node:fs";
import { join } from "node:path";
import v8 from "node:v8";
import vm from "node:vm";
import { PerformanceObserver } from "node:perf_hooks";

const dir = process.env.PH_PERF_MEM_DIR;
let seq = 0;
let gcCount = 0;
function read() {
	const m = process.memoryUsage();
	const h = v8.getHeapStatistics();
	return {
		rss: m.rss, heap_total: m.heapTotal, heap_used: m.heapUsed, external: m.external, array_buffers: m.arrayBuffers,
		v8_used: h.used_heap_size, v8_total: h.total_heap_size, v8_physical: h.total_physical_size, v8_malloced: h.malloced_memory,
		gc_events: gcCount,
	};
}
if (dir !== undefined && dir !== "" && (globalThis as { __phPerfInstalled?: boolean }).__phPerfInstalled !== true) {
	(globalThis as { __phPerfInstalled?: boolean }).__phPerfInstalled = true;
	v8.setFlagsFromString("--expose-gc");
	const collect = vm.runInNewContext("gc") as () => void;
	new PerformanceObserver((list) => {
		gcCount += list.getEntries().length;
	}).observe({ entryTypes: ["gc"] });
	process.on("SIGUSR2", () => {
		const asis = read();
		collect();
		const gc = read();
		const name = join(dir, `${process.pid}-${seq++}.json`);
		writeFileSync(`${name}.tmp`, JSON.stringify({ runtime: "node", version: process.version, asis, gc }));
		renameSync(`${name}.tmp`, name);
	});
}
