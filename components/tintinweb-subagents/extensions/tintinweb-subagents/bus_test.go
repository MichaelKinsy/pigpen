package tintinweb_subagents

import (
	"testing"
	"time"
)

func TestBusProtocol(t *testing.T) {
	r := startRig(t)
	t.Run("ping replies with the protocol version", func(t *testing.T) {
		eq(t, r.request("subagents:rpc:ping", obj{}), obj{"success": true, "data": obj{"version": float64(2)}})
	})
	t.Run("a reply reaches only its requester", func(t *testing.T) {
		got := make(chan struct{}, 1)
		r.onBus("subagents:rpc:ping:reply:other", func(any) { got <- struct{}{} })
		r.request("subagents:rpc:ping", obj{})
		select {
		case <-got:
			t.Fatal("another requester's channel got the reply")
		case <-time.After(50 * time.Millisecond):
		}
	})
	t.Run("spawn returns the agent id and starts a background agent", func(t *testing.T) {
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "do stuff", "options": obj{"description": "search", "maxTurns": float64(5)}})
		eq(t, reply["success"], true)
		id, _ := reply["data"].(map[string]any)["id"].(string)
		eq(t, id != "", true)
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Prompt, "do stuff")
		eq(t, c.spec.MaxTurns, 5)
		rec := r.app.mgr.get(id)
		eq(t, rec.Background, true)
		eq(t, rec.Description, "search")
		t.Run("consume refuses a running agent and accepts a finished one", func(t *testing.T) {
			eq(t, r.request("subagents:rpc:consume", obj{"agentId": id}), obj{"success": false, "error": "Agent not found or still running"})
			c.finish("r")
			r.waitEmitted("subagents:completed")
			eq(t, r.request("subagents:rpc:consume", obj{"agentId": id}), obj{"success": true})
			eq(t, r.app.mgr.get(id).Consumed, true)
		})
	})
	t.Run("stop aborts a running agent, and says why it cannot otherwise", func(t *testing.T) {
		reply := r.request("subagents:rpc:spawn", obj{"type": "Explore", "prompt": "x"})
		id := reply["data"].(map[string]any)["id"].(string)
		r.fleet.wait(t, 2)
		eq(t, r.request("subagents:rpc:stop", obj{"agentId": id}), obj{"success": true})
		<-r.app.mgr.get(id).done
		eq(t, r.app.mgr.get(id).Status, statusStopped)
		eq(t, r.request("subagents:rpc:stop", obj{"agentId": id}), obj{"success": false, "error": "Agent is not running"})
		eq(t, r.request("subagents:rpc:stop", obj{"agentId": "nope"}), obj{"success": false, "error": "Agent not found"})
	})
	t.Run("spawn reports a spawn failure as an error envelope", func(t *testing.T) {
		setFallbackSubagent(ptr(noFallback))
		defer setFallbackSubagent(nil)
		reply := r.request("subagents:rpc:spawn", obj{"type": "bad-type", "prompt": "x"})
		eq(t, reply["success"], false)
		eq(t, reply["error"].(string)[:34], `Unknown or disabled agent type: "b`)
	})
	t.Run("a failed start is reported on the failed channel", func(t *testing.T) {
		r.fleet.setFail(errBoom)
		defer r.fleet.setFail(nil)
		got := make(chan obj, 1)
		r.onBus("subagents:failed", func(d any) { got <- d.(map[string]any) })
		r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "x"})
		d := <-got
		eq(t, d["status"], statusError)
		eq(t, d["error"], "boom")
	})
}

func TestSpawnWithoutASession(t *testing.T) {
	r := startRig(t)
	r.app.mu.Lock()
	r.app.latest = nil
	r.app.mu.Unlock()
	reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "x"})
	eq(t, reply, obj{"success": false, "error": "No active session"})
	eq(t, r.fleet.count(), 0)
}
