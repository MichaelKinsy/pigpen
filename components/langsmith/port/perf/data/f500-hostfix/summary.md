# LangSmith perf summary

Cells are `median ±spread%` over fresh-process samples (spread = (p90-p10)/median; `!` marks more than 15 %). Ratios are of medians (below 1.00: Go is smaller or faster). `nopig`/`nopi` are the same host with no extension. Metric definitions: README.md. Every metric: summary-full.md.

### Workload f500, warm (go n=3, ts n=3, pi n=3)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| startup_ms | 71.3 ±4% | 801.5 ±10% | 492.5 ±14% | 0.09 | 0.14 |
| first_turn_ms | 51.9 ±38%! | 119.7 ±1% | 142.6 ±29%! | 0.43 | 0.36 |
| to_first_request_ms | 15.2 ±23%! | 67.8 ±2% | 73.9 ±64%! | 0.23 | 0.21 |
| to_first_tool_ms | 17.4 ±28%! | 72.6 ±2% | 92.8 ±51%! | 0.24 | 0.19 |
| turn_ms | 51.9 ±38%! | 119.7 ±1% | 142.6 ±29%! | 0.43 | 0.36 |
| work_ms | 51.9 ±38%! | 119.7 ±1% | 142.6 ±29%! | 0.43 | 0.36 |
| shutdown_ms | 20606.7 ±32%! | 22250.3 ±17%! | 21281.2 ±13% | 0.93 | 0.97 |
| ext_gone_ms | 20602.5 ±32%! | 22237.5 ±17%! | 21281.2 ±13% | 0.93 | 0.97 |
| cpu_total_ms_ready | 50 ±0% | 960 ±9% | 620 ±11% | 0.05 | 0.08 |
| shutdown_cpu_ext_ms | 20.0 ±0% | 280.0 ±29%! | - | 0.07 | - |
| ext_rss_ready | 9.43 MiB ±2% | 173.25 MiB ±2% | 129.03 MiB ±4% | 0.05 | 0.07 |
| ext_pss_ready | 9.42 MiB ±2% | 143.83 MiB ±19%! | 101.94 MiB ±20%! | 0.07 | 0.09 |
| ext_anon_ready | 1.87 MiB ±6% | 118.37 MiB ±3% | 76.91 MiB ±6% | 0.02 | 0.02 |
| peak_ext_rss | 13.02 MiB ±1% | 191.08 MiB ±5% | 151.64 MiB ±16%! | 0.07 | 0.09 |
| peak_ext_pss | 13.02 MiB ±1% | 159.85 MiB ±22%! | 133.65 MiB ±27%! | 0.08 | 0.10 |
| peak_ext_anonymous | 4.96 MiB ±2% | 132.77 MiB ±8% | 97.09 MiB ±25%! | 0.04 | 0.05 |
| peak_tree_rss | 72.43 MiB ±1% | 278.34 MiB ±3% | 151.64 MiB ±16%! | 0.26 | 0.48 |
| peak_tree_pss | 72.43 MiB ±1% | 247.35 MiB ±14% | 133.65 MiB ±27%! | 0.29 | 0.54 |
| llm_requests | 4 ±0% | 4 ±0% | 4 ±0% | 1.00 | 1.00 |
| ls_requests | 6 ±0% | 6 ±0% | 6 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 5 ±0% | 5 ±0% | 5 ±0% | 1.00 | 1.00 |
| ls_multipart | 5 ±0% | 5 ±0% | 5 ±0% | 1.00 | 1.00 |
| ls_bytes | 0.33 MiB ±0% | 0.33 MiB ±0% | 0.33 MiB ±0% | 1.00 | 1.00 |
| ls_decoded_bytes | 0.33 MiB ±0% | 0.33 MiB ±0% | 0.33 MiB ±0% | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_requests_before_close | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| cpu_wait4_ms | 101.8 ±14% | 225.3 ±4% | 1144.3 ±16%! | 0.45 | 0.09 |
| wall_ms | 21238.1 ±31%! | 23750.6 ±16%! | 22400.5 ±12% | 0.89 | 0.95 |
| shutdown_cpu_host_ms | 101.8 ±14% | 225.3 ±4% | 1144.3 ±16%! | 0.45 | 0.09 |
| tool_ms:bash | 4.3 ±75%! | 5.2 ±4% | 9.2 ±75%! | 0.83 | 0.47 |
| tool_ms:read | 0.8 ±80%! | 1.7 ±6% | 5.7 ±25%! | 0.44 | 0.13 |
| tool_ms:write | 0.9 ±55%! | 5.7 ±101%! | 7.7 ±113%! | 0.17 | 0.12 |

## Equal work across sides (model requests, LangSmith requests and bytes)

identical model request counts; LangSmith ingest requests and bytes within 5 %.

## LangSmith requests per workload (median over samples; the timer-leak divergence D3 shows as more, smaller ingest requests under ts/pi)

| Workload | go ingest req | ts ingest req | pi ingest req | go decoded bytes | ts decoded bytes | pi decoded bytes | go/ts bytes |
|---|---:|---:|---:|---:|---:|---:|---:|
| f500/warm | 5 | 5 | 5 | 0.33 MiB | 0.33 MiB | 0.33 MiB | 1.001 |

## Metrics where go is worse than ts (same pig host, Node extension runtime)

| Workload | Metric | go | ts | go/ts | separated |
|---|---|---:|---:|---:|---|

Metrics worse: 0.
