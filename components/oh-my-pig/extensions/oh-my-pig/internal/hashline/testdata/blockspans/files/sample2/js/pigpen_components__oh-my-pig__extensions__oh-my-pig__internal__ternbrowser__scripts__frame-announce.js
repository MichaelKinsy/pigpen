(function () {
"use strict";

// Child frames announce their index path so omp can address them by name.
if (window !== window.parent) {
	const path = [];
	try {
		for (let win = window; win !== win.parent; win = win.parent) {
			const parent = win.parent;
			let found = -1;
			for (let index = 0; index < parent.length; index++) {
				if (parent[index] === win) {
					found = index;
					break;
				}
			}
			if (found < 0) throw new Error("frame not found in parent");
			path.unshift(found);
		}
		const handler = globalThis.webkit && webkit.messageHandlers && webkit.messageHandlers.stencilFrame;
		if (handler) handler.postMessage(path.join("."));
	} catch {
		// Detached or unreachable frame: nothing to register.
	}
}

})();