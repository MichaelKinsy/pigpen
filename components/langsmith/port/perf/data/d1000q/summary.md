# LangSmith perf summary

Cells are `median ±spread%` over fresh-process samples (spread = (p90-p10)/median; `!` marks more than 15 %). Ratios are of medians (below 1.00: Go is smaller or faster). `nopig`/`nopi` are the same host with no extension. Metric definitions: README.md. Every metric: summary-full.md.

### Workload d1000, warm (go n=20, ts n=20, pi n=20)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| startup_ms | 50.9 ±85%! | 507.7 ±44%! | 315.0 ±44%! | 0.10 | 0.16 |
| first_turn_ms | 66.5 ±72%! | 121.5 ±32%! | 128.2 ±34%! | 0.55 | 0.52 |
| to_first_request_ms | 12.2 ±27%! | 46.8 ±41%! | 46.2 ±46%! | 0.26 | 0.26 |
| to_first_tool_ms | 13.6 ±26%! | 49.9 ±41%! | 58.5 ±50%! | 0.27 | 0.23 |
| turn_ms | 76141.4 ±21%! | 47668.4 ±16%! | 19490.2 ±31%! | 1.60 | 3.91 |
| work_ms | 76184.6 ±21%! | 47728.3 ±16%! | 19548.8 ±31%! | 1.60 | 3.90 |
| flush_tail_ms | 307.6 ±18%! | 53.6 ±192%! | 52.3 ±101%! | 5.74 | 5.88 |
| cpu_total_ms_ready | 30 ±67%! | 590 ±44%! | 385 ±39%! | 0.05 | 0.08 |
| cpu_total_ms_end | 89995 ±22%! | 56790 ±17%! | 22030 ±32%! | 1.58 | 4.09 |
| cpu_total_workload_ms | 89965.0 ±22%! | 56195.0 ±17%! | 21645.0 ±32%! | 1.60 | 4.16 |
| cpu_ext_workload_ms | 74605.0 ±21%! | 41130.0 ±16%! | 20460.0 ±31%! | 1.81 | 3.65 |
| ext_rss_ready | 9.49 MiB ±2% | 172.71 MiB ±2% | 128.05 MiB ±6% | 0.05 | 0.07 |
| ext_pss_ready | 9.49 MiB ±2% | 167.43 MiB ±21%! | 123.05 MiB ±39%! | 0.06 | 0.08 |
| ext_anon_ready | 1.95 MiB ±7% | 117.78 MiB ±3% | 75.90 MiB ±10% | 0.02 | 0.03 |
| ext_rss_end | 73.63 MiB ±52%! | 763.14 MiB ±8% | 608.11 MiB ±8% | 0.10 | 0.12 |
| ext_pss_end | 73.62 MiB ±52%! | 743.31 MiB ±11% | 588.85 MiB ±13% | 0.10 | 0.13 |
| ext_anon_end | 65.47 MiB ±59%! | 704.68 MiB ±9% | 553.43 MiB ±9% | 0.09 | 0.12 |
| ext_peak_rss_end | 170.90 MiB ±10% | 795.05 MiB ±7% | 625.61 MiB ±9% | 0.21 | 0.27 |
| peak_ext_rss | 170.42 MiB ±8% | 796.20 MiB ±7% | 624.67 MiB ±9% | 0.21 | 0.27 |
| peak_ext_pss | 170.42 MiB ±8% | 784.87 MiB ±9% | 609.36 MiB ±12% | 0.22 | 0.28 |
| peak_ext_anonymous | 162.29 MiB ±9% | 737.86 MiB ±7% | 569.94 MiB ±10% | 0.22 | 0.28 |
| tree_rss_end | 265.49 MiB ±27%! | 975.33 MiB ±8% | 608.11 MiB ±8% | 0.27 | 0.44 |
| tree_pss_end | 263.98 MiB ±27%! | 963.86 MiB ±11% | 588.86 MiB ±13% | 0.27 | 0.45 |
| peak_tree_rss | 362.95 MiB ±4% | 1013.46 MiB ±5% | 624.67 MiB ±9% | 0.36 | 0.58 |
| peak_tree_pss | 361.49 MiB ±4% | 1002.81 MiB ±5% | 609.36 MiB ±12% | 0.36 | 0.59 |
| ext_rss_growth | 64.17 MiB ±60%! | 590.15 MiB ±11% | 478.88 MiB ±12% | 0.11 | 0.13 |
| ext_threads_end | 6 ±17%! | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_fds_end | 8 ±12% | 26 ±4% | 25 ±12% | 0.31 | 0.32 |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| llm_requests | 1100 ±0% | 1100 ±0% | 1100 ±0% | 1.00 | 1.00 |
| ls_requests | 154 ±0% | 1079 ±11% | 716.5 ±15% | 0.14 | 0.21 |
| ls_ingest_requests | 153 ±0% | 1078 ±11% | 715.5 ±15% | 0.14 | 0.21 |
| ls_multipart | 153 ±0% | 1078 ±11% | 715.5 ±15% | 0.14 | 0.21 |
| ls_bytes | 480.53 MiB ±0% | 483.08 MiB ±0% | 483.54 MiB ±0% | 0.99 | 0.99 |
| ls_decoded_bytes | 480.53 MiB ±0% | 483.08 MiB ±0% | 483.54 MiB ±0% | 0.99 | 0.99 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_anon_growth | 63.55 MiB ±61%! | 586.62 MiB ±11% | 476.44 MiB ±12% | 0.11 | 0.13 |
| cpu_wait4_ms | 15760.0 ±29%! | 15208.2 ±19%! | 22086.7 ±32%! | 1.04 | 0.71 |
| wall_ms | 77688.3 ±21%! | 49494.2 ±15%! | 21128.9 ±29%! | 1.57 | 3.68 |
| host_rss_end | 195.03 MiB ±28%! | 212.09 MiB ±26%! | 608.11 MiB ±8% | 0.92 | 0.32 |
| host_pss_end | 193.50 MiB ±29%! | 210.56 MiB ±26%! | 588.86 MiB ±13% | 0.92 | 0.33 |
| host_cpu_ms_end | 15085 ±24%! | 14865 ±19%! | 20845 ±31%! | 1.01 | 0.72 |
| tool_ms:bash | 2.3 ±52%! | 2.7 ±92%! | 10.5 ±109%! | 0.84 | 0.21 |
| tool_ms:read | 0.4 ±53%! | 0.5 ±63%! | 1.4 ±328%! | 0.70 | 0.25 |
| tool_ms:write | 0.4 ±53%! | 0.6 ±58%! | 1.9 ±247%! | 0.72 | 0.23 |

## Equal work across sides (model requests, LangSmith requests and bytes)

- NOTE d1000/warm ls_ingest_requests differs by more than 5%: go=153, ts=1078, pi=716

## LangSmith requests per workload (median over samples; the timer-leak divergence D3 shows as more, smaller ingest requests under ts/pi)

| Workload | go ingest req | ts ingest req | pi ingest req | go decoded bytes | ts decoded bytes | pi decoded bytes | go/ts bytes |
|---|---:|---:|---:|---:|---:|---:|---:|
| d1000/warm | 153 | 1078 | 716 | 480.53 MiB | 483.08 MiB | 483.54 MiB | 0.995 |

## Metrics where go is worse than ts (same pig host, Node extension runtime)

| Workload | Metric | go | ts | go/ts | separated |
|---|---|---:|---:|---:|---|
| d1000/warm | turn_ms | 76141.4 | 47668.4 | 1.60 | yes (p10 of go above p90 of ts) |
| d1000/warm | work_ms | 76184.6 | 47728.3 | 1.60 | yes (p10 of go above p90 of ts) |
| d1000/warm | flush_tail_ms | 307.6 | 53.6 | 5.74 | yes (p10 of go above p90 of ts) |
| d1000/warm | cpu_total_ms_end | 89995 | 56790 | 1.58 | yes (p10 of go above p90 of ts) |
| d1000/warm | cpu_total_workload_ms | 89965.0 | 56195.0 | 1.60 | yes (p10 of go above p90 of ts) |
| d1000/warm | cpu_ext_workload_ms | 74605.0 | 41130.0 | 1.81 | yes (p10 of go above p90 of ts) |

Metrics worse: 6.
