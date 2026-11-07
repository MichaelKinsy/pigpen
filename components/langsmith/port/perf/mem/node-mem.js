// Measurement-only wrapper (never shipped): port/perf/perf.py generates a scratch entry that imports this file and
// re-exports the extension under test. On SIGUSR2 it writes process.memoryUsage(), V8 heap statistics, the GC count
// and total GC pause, and the number of active libuv resources (timers, sockets, ...) to $LS_PERF_MEM_DIR/<pid>-<seq>.json:
// once as the process stands ("asis") and once after a forced full GC ("gc"). SIGUSR1 is not used: Node reserves it for
// the inspector.
import { writeFileSync, renameSync } from "node:fs";
import { join } from "node:path";
import v8 from "node:v8";
import vm from "node:vm";
import { PerformanceObserver } from "node:perf_hooks";

const dir = process.env.LS_PERF_MEM_DIR;
let seq = 0;
let gcCount = 0;
let gcPauseMs = 0;
let observer;
function drain(entries) {
	for (const e of entries) {
		gcCount++;
		gcPauseMs += e.duration;
	}
}
function read() {
	drain(observer.takeRecords());
	const m = process.memoryUsage();
	const h = v8.getHeapStatistics();
	const resources = {};
	for (const r of process.getActiveResourcesInfo()) resources[r] = (resources[r] ?? 0) + 1;
	return {
		rss: m.rss, heap_total: m.heapTotal, heap_used: m.heapUsed, external: m.external, array_buffers: m.arrayBuffers,
		v8_used: h.used_heap_size, v8_total: h.total_heap_size, v8_physical: h.total_physical_size, v8_malloced: h.malloced_memory,
		gc_events: gcCount, gc_pause_ms: gcPauseMs, resources,
		active_resources: Object.values(resources).reduce((a, b) => a + b, 0),
	};
}
if (dir !== undefined && dir !== "" && globalThis.__lsPerfInstalled !== true) {
	globalThis.__lsPerfInstalled = true;
	v8.setFlagsFromString("--expose-gc");
	const collect = vm.runInNewContext("gc");
	observer = new PerformanceObserver((list) => drain(list.getEntries()));
	observer.observe({ entryTypes: ["gc"] });
	process.on("SIGUSR2", () => {
		const asis = read();
		collect();
		const gc = read();
		const name = join(dir, `${process.pid}-${seq++}.json`);
		writeFileSync(`${name}.tmp`, JSON.stringify({ runtime: "node", version: process.version, asis, gc }));
		renameSync(`${name}.tmp`, name);
	});
}
