# LangSmith perf summary

Cells are `median ±spread%` over fresh-process samples (spread = (p90-p10)/median; `!` marks more than 15 %). Ratios are of medians (below 1.00: Go is smaller or faster). `nopig`/`nopi` are the same host with no extension. Metric definitions: README.md. Every metric: summary-full.md.

### Workload d1000, warm (go n=11, ts n=8, pi n=9)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| startup_ms | 48.5 ±48%! | 491.6 ±13% | 299.7 ±26%! | 0.10 | 0.16 |
| first_turn_ms | 64.7 ±22%! | 121.5 ±7% | 127.7 ±27%! | 0.53 | 0.51 |
| to_first_request_ms | 12.4 ±12% | 46.7 ±10% | 44.4 ±36%! | 0.27 | 0.28 |
| to_first_tool_ms | 13.8 ±11% | 49.8 ±9% | 55.7 ±39%! | 0.28 | 0.25 |
| turn_ms | 74594.1 ±5% | 46637.6 ±5% | 18886.2 ±4% | 1.60 | 3.95 |
| work_ms | 74640.6 ±5% | 46688.7 ±5% | 18917.7 ±4% | 1.60 | 3.95 |
| flush_tail_ms | 263.2 ±20%! | 53.7 ±191%! | 52.4 ±104%! | 4.90 | 5.03 |
| cpu_total_ms_ready | 30 ±33%! | 570 ±18%! | 370 ±24%! | 0.05 | 0.08 |
| cpu_total_ms_end | 88040 ±5% | 55685 ±5% | 21320 ±4% | 1.58 | 4.13 |
| cpu_total_workload_ms | 88020.0 ±5% | 55090.0 ±5% | 20960.0 ±4% | 1.60 | 4.20 |
| cpu_ext_workload_ms | 73050.0 ±5% | 40410.0 ±4% | 19740.0 ±4% | 1.81 | 3.70 |
| ext_rss_ready | 9.52 MiB ±1% | 172.19 MiB ±2% | 129.39 MiB ±7% | 0.06 | 0.07 |
| ext_pss_ready | 9.52 MiB ±1% | 168.19 MiB ±15%! | 127.34 MiB ±26%! | 0.06 | 0.07 |
| ext_anon_ready | 1.96 MiB ±5% | 117.30 MiB ±3% | 77.21 MiB ±11% | 0.02 | 0.03 |
| ext_rss_end | 73.27 MiB ±37%! | 764.11 MiB ±8% | 611.98 MiB ±14% | 0.10 | 0.12 |
| ext_pss_end | 73.27 MiB ±37%! | 758.80 MiB ±8% | 598.00 MiB ±14% | 0.10 | 0.12 |
| ext_anon_end | 65.08 MiB ±41%! | 705.70 MiB ±9% | 557.32 MiB ±15%! | 0.09 | 0.12 |
| ext_peak_rss_end | 168.61 MiB ±8% | 807.36 MiB ±6% | 618.61 MiB ±9% | 0.21 | 0.27 |
| peak_ext_rss | 170.08 MiB ±6% | 809.79 MiB ±6% | 611.98 MiB ±10% | 0.21 | 0.28 |
| peak_ext_pss | 170.08 MiB ±6% | 801.05 MiB ±6% | 609.11 MiB ±11% | 0.21 | 0.28 |
| peak_ext_anonymous | 161.89 MiB ±6% | 751.38 MiB ±6% | 557.35 MiB ±11% | 0.22 | 0.29 |
| tree_rss_end | 258.43 MiB ±15% | 984.51 MiB ±7% | 611.98 MiB ±14% | 0.26 | 0.42 |
| tree_pss_end | 256.96 MiB ±15%! | 960.97 MiB ±10% | 598.00 MiB ±14% | 0.27 | 0.43 |
| peak_tree_rss | 362.24 MiB ±3% | 1026.51 MiB ±3% | 611.98 MiB ±10% | 0.35 | 0.59 |
| peak_tree_pss | 360.69 MiB ±3% | 1012.83 MiB ±3% | 609.11 MiB ±11% | 0.36 | 0.59 |
| ext_rss_growth | 63.71 MiB ±42%! | 592.28 MiB ±11% | 480.12 MiB ±18%! | 0.11 | 0.13 |
| ext_threads_end | 6 ±17%! | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_fds_end | 8 ±12% | 26 ±4% | 25 ±12% | 0.31 | 0.32 |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| llm_requests | 1100 ±0% | 1100 ±0% | 1100 ±0% | 1.00 | 1.00 |
| ls_requests | 154 ±0% | 1087.5 ±11% | 725 ±18% | 0.14 | 0.21 |
| ls_ingest_requests | 153 ±0% | 1086.5 ±11% | 724 ±18% | 0.14 | 0.21 |
| ls_multipart | 153 ±0% | 1086.5 ±11% | 724 ±18% | 0.14 | 0.21 |
| ls_bytes | 480.53 MiB ±0% | 483.07 MiB ±0% | 483.57 MiB ±0% | 0.99 | 0.99 |
| ls_decoded_bytes | 480.53 MiB ±0% | 483.07 MiB ±0% | 483.57 MiB ±0% | 0.99 | 0.99 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_anon_growth | 63.09 MiB ±42%! | 588.78 MiB ±11% | 477.69 MiB ±18%! | 0.11 | 0.13 |
| cpu_wait4_ms | 14945.9 ±7% | 14765.0 ±6% | 21364.0 ±4% | 1.01 | 0.70 |
| wall_ms | 76105.5 ±5% | 48431.6 ±4% | 20451.2 ±3% | 1.57 | 3.72 |
| host_rss_end | 194.35 MiB ±26%! | 213.19 MiB ±32%! | 611.98 MiB ±14% | 0.91 | 0.32 |
| host_pss_end | 192.83 MiB ±26%! | 211.68 MiB ±33%! | 598.00 MiB ±14% | 0.91 | 0.32 |
| host_cpu_ms_end | 14610 ±5% | 14440 ±6% | 20100 ±4% | 1.01 | 0.73 |
| tool_ms:bash | 2.2 ±40%! | 2.6 ±96%! | 9.7 ±104%! | 0.84 | 0.22 |
| tool_ms:read | 0.3 ±37%! | 0.5 ±47%! | 1.3 ±336%! | 0.70 | 0.26 |
| tool_ms:write | 0.4 ±39%! | 0.6 ±44%! | 1.8 ±250%! | 0.71 | 0.23 |

## Equal work across sides (model requests, LangSmith requests and bytes)

- NOTE d1000/warm ls_ingest_requests differs by more than 5%: go=153, ts=1086, pi=724

## LangSmith requests per workload (median over samples; the timer-leak divergence D3 shows as more, smaller ingest requests under ts/pi)

| Workload | go ingest req | ts ingest req | pi ingest req | go decoded bytes | ts decoded bytes | pi decoded bytes | go/ts bytes |
|---|---:|---:|---:|---:|---:|---:|---:|
| d1000/warm | 153 | 1086 | 724 | 480.53 MiB | 483.07 MiB | 483.57 MiB | 0.995 |

## Metrics where go is worse than ts (same pig host, Node extension runtime)

| Workload | Metric | go | ts | go/ts | separated |
|---|---|---:|---:|---:|---|
| d1000/warm | turn_ms | 74594.1 | 46637.6 | 1.60 | yes (p10 of go above p90 of ts) |
| d1000/warm | work_ms | 74640.6 | 46688.7 | 1.60 | yes (p10 of go above p90 of ts) |
| d1000/warm | flush_tail_ms | 263.2 | 53.7 | 4.90 | yes (p10 of go above p90 of ts) |
| d1000/warm | cpu_total_ms_end | 88040 | 55685 | 1.58 | yes (p10 of go above p90 of ts) |
| d1000/warm | cpu_total_workload_ms | 88020.0 | 55090.0 | 1.60 | yes (p10 of go above p90 of ts) |
| d1000/warm | cpu_ext_workload_ms | 73050.0 | 40410.0 | 1.81 | yes (p10 of go above p90 of ts) |

Metrics worse: 6.
