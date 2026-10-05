# Patch 0.31.3

Include PR #54 / issue #53: no-work agent runs must not perpetually continue; preserve kickoff and continuation after useful work earlier in a run.

Fix #52: RPC task approval must show the proposal through native dialogs. Cancelled or unavailable UI preserves tasks. Preserve established headless behavior; UI-capable hosts must receive and explicitly approve a dialog. PI_GOAL_AUTO_CONFIRM retains its existing overrides.

Defer #55: the reported read/bookkeeping loop is credible, but read/search are legitimate research progress. The proposed opt-in cooldown is a new scheduling policy, and background producer detection adds lifecycle integration. Keep the issue open for a separately specified change rather than silently throttle research in a patch. A default-zero setting would not fix existing users by itself.
