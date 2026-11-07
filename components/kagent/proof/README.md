# Proof tooling and results for the PiG kagent harness

This directory holds everything needed to repeat the measurements and the demo recording behind the claims in
[`../harness/README.md`](../harness/README.md): a hermetic mock model server, an A2A client that runs a fixed task through kagent's gateway,
a cgroup and log collector for per-Actor memory and lifecycle timing, the scripts that run an interleaved schedule, and the recording script. The results of the
run published with this repository are in [`RESULTS.md`](RESULTS.md). Reported times include the mock's fixed per-call cost (about 0.11 s) and it is not subtracted; pi-go makes 2 model calls for the `[basket]` task where the others make 3.

**Scope.** No real model is involved anywhere. All agents (PiG, Claude Code, Codex, pi-go) are pointed at the same deterministic mock server
(`mockllm/`), so the numbers show what the harness and the platform add around a model, not how a model performs. The proof environment is a local Kind cluster
running kagent `v1.0.0-rc3` and Agent Substrate `v1.0.0`; nothing here is a vendor benchmark. kagent, Agent Substrate, Claude Code, Codex and pi-go belong to their
respective owners and are not affiliated with this repository.

## Layout

| Path | What it is |
| --- | --- |
| `mockllm/` | Go mock server (standard library only, about 3 MB static binary): OpenAI Chat Completions, OpenAI Responses and Anthropic Messages (streaming and not) and a Streamable HTTP MCP server with the tools `kagent_probe` and `echo`. `Dockerfile` (`FROM scratch`), `mock.yaml` (Deployment and Service), unit tests. |
| `basket/` | Go A2A client. `-mode bench` (default) runs the task and records timings; `-mode send` / `-mode approve` send one message or one approval and print the events live (used by the demo); `-serve-fake` serves a fake gateway for self-tests. |
| `collect.py`, `test_collect.py` | cgroup v2 sampler for pods and Substrate Actor cgroups, extractor for Substrate log records, report. Standard library only. |
| `reject.sh`, `restart.sh`, `series.py` | The rejection series and the worker-pod-replacement series, and the record/summary logic behind them (`test_series.py`). |
| `perf.sh` | One harness: collector plus basket plus report. |
| `baseline.sh` | The interleaved schedule over several agents with a host-noise gate; streams the Substrate logs. |
| `analyze.py` | Joins the basket runs, the memory samples and the Substrate records per session and writes the tables. |
| `images.sh`, `coldstart.sh` | Image sizes and registry pull time; Agent apply to Ready time. |
| `demo.sh`, `record.sh`, `mem_table.py` | The terminal walk-through, its headless recording (asciinema, agg, ffmpeg) and the last screen. |
| `manifests/` | The baseline agents (Claude Code, Codex, pi-go and approval-only variants) and the Helm values for the two PiG agents. |
| `RESULTS.md`, `data/` | Published results with method and caveats; per-session data of that run (compressed JSON). |

## The task the mock scripts

The mock plans from the conversation, never from the model name. The prompt is the last user message that contains a marker; a plain follow-up such as the `Continue.`
the PiG adapter appends after an injected tool result is ignored while a scenario is pending.

| Marker in the prompt | Behaviour |
| --- | --- |
| `[basket]` | A shell tool call (`echo basket-ok > /tmp/basket.txt && cat /tmp/basket.txt`, using whichever of `bash`, `Bash`, `exec_command`, `shell_command`, `shell` the client offers), then a call to the MCP tool whose name ends in `kagent_probe`, then `BASKET-DONE <sha256 of the tool results>`. |
| `[approve]` | One call to the approval-gated `kagent_probe` (a tool whose name or namespace contains `approval`), then `APPROVE-DONE <sha256>`. |
| `[recall]` | `RECALL <first [marker] of the history>`, proving a restored session remembers earlier turns. |
| anything else | `ECHO <prompt>`. |

Token usage is deterministic (about one token per four bytes), the first token arrives after `MOCK_TTFT_MS` (50 ms) and each chunk after `MOCK_CHUNK_DELAY_MS` (5 ms).
Every request is logged as one JSON line (path, which auth header was present but never its value, body size, a hash of the messages, the scenario step).

## Reproduce

Requirements: a Linux host with cgroup v2 (the sampler reads `/sys/fs/cgroup`; the measurements were taken on amd64, other architectures are untested; Docker Desktop is not covered), Docker, `kind`, `kubectl`, Helm 3.13 or newer,
Python 3, `jq`, `bc`, `crane` (for `images.sh`), and either Go 1.22 or newer or the `golang` Docker image.

1. **Cluster, registry, kagent and Substrate, PiG harness image.** Follow sections 1 to 5 of [`../harness/docs/kind.md`](../harness/docs/kind.md). Because no model is used, no real
   model key is needed (the proof cluster was installed without one; every agent below points at the mock). After this the PiG harness image exists as
   `$IMAGE` (digest-pinned) and the WorkerPool `kagent-default` is ready.

2. **Build and deploy the mock.**

   ```sh
   cd components/kagent/proof/mockllm
   docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.26-bookworm sh -c 'go vet ./... && go test ./...'
   docker build -t localhost:5001/kagent-mock:dev .
   docker push localhost:5001/kagent-mock:dev        # prints: digest: sha256:<64 hex>
   # put that digest into mock.yaml in place of REPLACE_WITH_THE_DIGEST_FROM_DOCKER_PUSH
   kubectl apply -f mock.yaml
   kubectl -n kagent rollout status deploy/kagent-mock
   ```

   Inside the cluster the mock is `http://kagent-mock.kagent.svc.cluster.local:8080` (model APIs under `/v1`) and `...:8081/mcp` (MCP). Substrate's egress allows DNS names only, which is why the
   manifests use the Service DNS name. A quick check from your machine: `kubectl -n kagent port-forward svc/kagent-mock 8080:8080` and
   `curl -s localhost:8080/v1/models`.

3. **The agents.**

   ```sh
   kubectl apply -f ../manifests/baselines.yaml        # edit the pi-go image placeholder first, or delete the pi-go objects
   cd ../../harness
   ./pig-kagent.sh install -n kagent -r pig-basket  -f ../proof/manifests/pig-basket-values.yaml  --set image="$IMAGE"
   ./pig-kagent.sh install -n kagent -r pig-approve -f ../proof/manifests/pig-approve-values.yaml --set image="$IMAGE"
   kubectl -n kagent get agent        # every agent Ready=True
   ```

   `claude-basket`, `claude-approve`, `codex-basket` and `codex-approve` use kagent's built-in harness images. `pigo-basket` needs a pi-go image you build yourself (see the comment
   in `baselines.yaml`); skip it if you only want PiG against Claude Code and Codex.

4. **The client.** `docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 golang:1.26-bookworm go build -trimpath -ldflags='-s -w' -o basket .` in `basket/`, then use it in place (the scripts default to `basket/basket`; set `BASKET` to use another path).
   Tests: `go test ./...` in `basket/`. Forward the gateway and try one session:

   ```sh
   kubectl -n kagent port-forward svc/kagent-controller 8083:8083 &
   basket/basket -mode send -live -agent kagent/pig-basket -header 'x-user-id: admin@kagent.dev' -text '[basket] hello'
   ```

   With kagent's default insecure authentication the user is a header; set `BASKET_TOKEN` for a bearer token instead.

5. **Run the schedule** on the Linux host that runs the Kind node (so `/sys/fs/cgroup` shows the pod cgroups), with `kubectl` pointed at the cluster:

   ```sh
   AGENTS='claude-basket:basket codex-basket:basket pigo-basket:basket pig-basket:basket claude-approve:approve codex-approve:approve pig-approve:approve' \
   ROUNDS=10 WARM=2 ./baseline.sh                      # results/baseline-<date>/ (about 20 minutes)
   ./images.sh results/baseline-<date>/images.json
   ./coldstart.sh results/baseline-<date>/coldstart.json 5
   python3 analyze.py --dir results/baseline-<date> --images results/baseline-<date>/images.json \
     --coldstart results/baseline-<date>/coldstart.json --out-json results/baseline-<date>/analysis.json --out-md results/baseline-<date>/analysis.md
   ```

   `baseline.sh` waits, before each session, until the host's CPU pressure is at most `MAX_PSI` (default 35 percent), for up to `GATE_WAIT` seconds. After that it runs the session anyway and records it
   (`gate.log`, `pressure.tsv`; `analyze.py` lists such sessions) unless `GATE_MODE=refuse`, which stops the schedule with exit status 3. The namespace, worker pool, mock and controller selectors, A2A namespace and
   user header are variables (`AGENT_NS`, `POOL`, `MOCK_SEL`, `CONTROLLER_SEL`, `AGENT_GROUP`, `USER_HEADER`; see the header of the script); the defaults are the proof cluster's. Kind runs the Substrate namespace as
   `ate-system` by default; set `SUBSTRATE_NS` if yours differs. The collector finds each pod's cgroup by its UID, so it works with the systemd and cgroupfs drivers.
   The raw 0.25 s samples are `mem.jsonl` in the results directory; add `--raw-out data/<run>-mem.jsonl.gz` to `analyze.py` to ship them gzip-compressed (budget `--raw-budget`, default 2.8 MB, falling back to the first
   activation of every measured run, noted in the file). `analyze.py --mem <file>[.gz]` re-derives the tables from it. CPU per activation is the sum over the cgroup leaves of each leaf's maximum `cpu_usec` in the activation.
   Run one harness at a time with `perf.sh` (see its header). The cgroup tests need no cluster: `python3 test_collect.py`.

6. **Two series for the claims that need one** (they need the gateway and kubectl, not cgroup access):

   ```sh
   AGENT=pig-approve N=10 ./reject.sh                                   # reject with a reason; checks the reason reaches the mock's log and the answer sha is the rejected path's
   AGENT=pig-basket APPROVE_AGENT=pig-approve N=10 ./restart.sh          # DELETES the worker pod 2 x N times; first-attempt result, retries and time to success
   ```

   Both write `runs.jsonl` (one record per run), `summary.json` and `summary.md`; the restart summary reports first-attempt success separately from eventual success. `test_series_scripts.sh` runs both against the
   fake gateway (`basket -serve-fake`, with `-fake-faults` to inject gateway errors) and a stub `kubectl`.

7. **Record the demo.** Download the asciinema 3.x static Linux binary into `demo/bin/asciinema` (nothing is installed), pull the images `ghcr.io/asciinema/agg` and
   `jrottenberg/ffmpeg:7-alpine`, then `./record.sh final` (it runs `demo.sh` against the cluster, including one deletion of the worker pod while an approval is pending, renders a 1280x720
   H.264 file and reports its size). `demo.sh` needs the `analysis.json` from step 5 for its last screen. Delete the two images afterwards.

## Licence

MIT, the licence of this repository (see [`../LICENSE`](../LICENSE)). The Go modules here use only the standard library. `manifests/baselines.yaml`
mentions the pi-go project (MIT) only as something you build separately.
