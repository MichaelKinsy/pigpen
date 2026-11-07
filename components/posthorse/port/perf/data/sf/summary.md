# Posthorse perf summary

Medians and p90 over fresh-process samples; n in parentheses. Metric definitions: README.md. Every metric: summary-full.md.

### Workload a, cold

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 736.5 / 762.1 (25) | 2293.2 / 2485.4 (25) | 739.3 / 794.9 (25) | 2324.2 / 2526.1 (25) |
| cpu_total_ms_end | 1140 / 1190 (25) | 3740 / 4070 (25) | 1130 / 1200 (25) | 3810 / 4170 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 10 / 10 (25) | 3090 / 3420 (25) | 10 / 10 (25) | 3150 / 3490 (25) |
| ext_rss_end | 9.34 MiB / 9.59 MiB (25) | 311.43 MiB / 314.92 MiB (25) | 9.37 MiB / 9.58 MiB (25) | 311.56 MiB / 314.60 MiB (25) |
| ext_pss_end | 9.33 MiB / 9.59 MiB (25) | 307.50 MiB / 311.01 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 307.60 MiB / 310.63 MiB (25) |
| tree_rss_end | 82.64 MiB / 85.45 MiB (25) | 463.91 MiB / 473.21 MiB (25) | 83.06 MiB / 86.72 MiB (25) | 460.76 MiB / 473.34 MiB (25) |
| tree_pss_end | 81.28 MiB / 84.00 MiB (25) | 458.49 MiB / 467.87 MiB (25) | 81.67 MiB / 85.31 MiB (25) | 455.43 MiB / 468.04 MiB (25) |
| ext_peak_rss_end | 9.34 MiB / 9.59 MiB (25) | 311.43 MiB / 314.92 MiB (25) | 9.37 MiB / 9.58 MiB (25) | 311.56 MiB / 314.60 MiB (25) |
| ext_anon_end | 2.42 MiB / 2.61 MiB (25) | 255.71 MiB / 259.29 MiB (25) | 2.45 MiB / 2.71 MiB (25) | 255.86 MiB / 258.83 MiB (25) |
| ext_rss_ready | 9.34 MiB / 9.59 MiB (25) | 311.43 MiB / 314.92 MiB (25) | 9.37 MiB / 9.58 MiB (25) | 311.56 MiB / 314.60 MiB (25) |
| ext_pss_ready | 9.33 MiB / 9.59 MiB (25) | 307.50 MiB / 311.01 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 307.60 MiB / 310.63 MiB (25) |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB (25) | 47.18 MiB / 47.19 MiB (25) | 0.51 MiB / 0.51 MiB (25) | 47.19 MiB / 47.19 MiB (25) |
| heap_used_asis_end | 0.52 MiB / 0.52 MiB (25) | 48.61 MiB / 48.62 MiB (25) | 0.52 MiB / 0.52 MiB (25) | 48.62 MiB / 48.63 MiB (25) |
| heap_inuse_gc_end | 1.55 MiB / 1.59 MiB (25) | 181.25 MiB / 182.00 MiB (25) | 1.51 MiB / 1.60 MiB (25) | 181.25 MiB / 182.00 MiB (25) |
| heap_inuse_asis_end | 1.55 MiB / 1.60 MiB (25) | 181.25 MiB / 182.00 MiB (25) | 1.52 MiB / 1.61 MiB (25) | 181.25 MiB / 182.00 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.60 MiB (25) | 301.98 MiB / 304.50 MiB (25) | 13.40 MiB / 13.66 MiB (25) | 302.02 MiB / 304.50 MiB (25) |
| sys_gc_end | 13.42 MiB / 13.68 MiB (25) | 301.98 MiB / 304.50 MiB (25) | 13.42 MiB / 13.68 MiB (25) | 302.02 MiB / 304.50 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| mallocs_workload | 188 / 194 (25) | - | 187 / 194 (25) | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) | - | 0.04 MiB / 0.04 MiB (25) | - |

### Workload a, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 117.1 / 126.5 (25) | 855.7 / 885.6 (25) | 116.0 / 127.4 (25) | 849.6 / 880.7 (25) |
| cpu_total_ms_end | 90 / 100 (25) | 1180 / 1220 (25) | 90 / 100 (25) | 1160 / 1210 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 10 / 10 (25) | 1000 / 1020 (25) | 10 / 10 (25) | 970 / 1020 (25) |
| ext_rss_end | 9.40 MiB / 9.59 MiB (25) | 161.08 MiB / 161.93 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 160.73 MiB / 161.91 MiB (25) |
| ext_pss_end | 9.40 MiB / 9.59 MiB (25) | 157.22 MiB / 158.02 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 156.81 MiB / 158.04 MiB (25) |
| tree_rss_end | 70.00 MiB / 70.67 MiB (25) | 245.41 MiB / 246.10 MiB (25) | 70.17 MiB / 71.06 MiB (25) | 245.02 MiB / 246.19 MiB (25) |
| tree_pss_end | 68.55 MiB / 69.34 MiB (25) | 240.09 MiB / 240.77 MiB (25) | 68.82 MiB / 69.63 MiB (25) | 239.67 MiB / 240.93 MiB (25) |
| ext_peak_rss_end | 9.40 MiB / 9.59 MiB (25) | 161.08 MiB / 161.93 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 160.73 MiB / 161.91 MiB (25) |
| ext_anon_end | 2.48 MiB / 2.67 MiB (25) | 105.54 MiB / 106.34 MiB (25) | 2.50 MiB / 2.71 MiB (25) | 104.99 MiB / 106.43 MiB (25) |
| ext_rss_ready | 9.40 MiB / 9.59 MiB (25) | 161.08 MiB / 161.93 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 160.73 MiB / 161.91 MiB (25) |
| ext_pss_ready | 9.40 MiB / 9.59 MiB (25) | 157.22 MiB / 158.02 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 156.81 MiB / 158.04 MiB (25) |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB (25) | 34.94 MiB / 34.94 MiB (25) | 0.51 MiB / 0.51 MiB (25) | 34.95 MiB / 34.95 MiB (25) |
| heap_used_asis_end | 0.52 MiB / 0.52 MiB (25) | 35.20 MiB / 35.20 MiB (25) | 0.52 MiB / 0.52 MiB (25) | 35.20 MiB / 35.21 MiB (25) |
| heap_inuse_gc_end | 1.48 MiB / 1.60 MiB (25) | 69.84 MiB / 101.59 MiB (25) | 1.53 MiB / 1.57 MiB (25) | 69.84 MiB / 101.09 MiB (25) |
| heap_inuse_asis_end | 1.48 MiB / 1.61 MiB (25) | 70.09 MiB / 101.59 MiB (25) | 1.54 MiB / 1.58 MiB (25) | 70.09 MiB / 101.09 MiB (25) |
| sys_asis_end | 13.40 MiB / 13.66 MiB (25) | 149.09 MiB / 154.30 MiB (25) | 13.41 MiB / 13.66 MiB (25) | 149.61 MiB / 152.32 MiB (25) |
| sys_gc_end | 13.42 MiB / 13.68 MiB (25) | 149.09 MiB / 154.30 MiB (25) | 13.43 MiB / 13.68 MiB (25) | 149.61 MiB / 152.32 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| mallocs_workload | 187 / 190 (25) | - | 183 / 189 (25) | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) | - | 0.04 MiB / 0.04 MiB (25) | - |

### Workload b, cold

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 753.7 / 790.7 (25) | 2382.2 / 2548.0 (25) | 774.1 / 787.1 (25) | 2351.1 / 2542.0 (25) |
| first_turn_ms | 47.0 / 50.6 (25) | 65.8 / 71.6 (25) | 40.7 / 46.3 (25) | 62.2 / 71.3 (25) |
| to_first_tool_ms | 20.8 / 25.0 (25) | 34.4 / 37.3 (25) | 19.8 / 22.3 (25) | 33.5 / 36.7 (25) |
| turn_ms | 47.0 / 50.6 (25) | 65.8 / 71.6 (25) | 40.7 / 46.3 (25) | 62.2 / 71.3 (25) |
| cpu_total_ms_end | 1210 / 1250 (25) | 3980 / 4270 (25) | 1230 / 1250 (25) | 3920 / 4260 (25) |
| cpu_total_workload_ms | 50.0 / 60.0 (25) | 90.0 / 110.0 (25) | 40.0 / 60.0 (25) | 90.0 / 110.0 (25) |
| cpu_ext_workload_ms | 20.0 / 30.0 (25) | 60.0 / 80.0 (25) | 20.0 / 20.0 (25) | 70.0 / 80.0 (25) |
| ext_cpu_ms_end | 30 / 40 (25) | 3280 / 3560 (25) | 20 / 30 (25) | 3240 / 3550 (25) |
| ext_rss_end | 11.06 MiB / 12.18 MiB (25) | 316.81 MiB / 319.59 MiB (25) | 10.71 MiB / 10.89 MiB (25) | 316.57 MiB / 319.12 MiB (25) |
| ext_pss_end | 11.05 MiB / 12.18 MiB (25) | 312.89 MiB / 315.62 MiB (25) | 10.71 MiB / 10.89 MiB (25) | 312.64 MiB / 315.20 MiB (25) |
| tree_rss_end | 85.83 MiB / 87.69 MiB (25) | 473.58 MiB / 483.43 MiB (25) | 84.25 MiB / 87.10 MiB (25) | 480.62 MiB / 484.38 MiB (25) |
| tree_pss_end | 84.43 MiB / 86.28 MiB (25) | 468.35 MiB / 478.04 MiB (25) | 82.92 MiB / 85.69 MiB (25) | 475.35 MiB / 479.08 MiB (25) |
| ext_peak_rss_end | 11.06 MiB / 12.18 MiB (25) | 316.81 MiB / 319.59 MiB (25) | 10.71 MiB / 10.89 MiB (25) | 316.57 MiB / 319.12 MiB (25) |
| ext_anon_end | 4.08 MiB / 5.13 MiB (25) | 256.34 MiB / 259.21 MiB (25) | 3.78 MiB / 3.96 MiB (25) | 256.14 MiB / 258.83 MiB (25) |
| ext_rss_ready | 9.37 MiB / 9.57 MiB (25) | 311.09 MiB / 313.46 MiB (25) | 9.27 MiB / 9.45 MiB (25) | 311.45 MiB / 313.80 MiB (25) |
| ext_pss_ready | 9.36 MiB / 9.57 MiB (25) | 307.18 MiB / 309.50 MiB (25) | 9.26 MiB / 9.45 MiB (25) | 307.52 MiB / 309.89 MiB (25) |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB (25) | 47.42 MiB / 47.43 MiB (25) | 0.54 MiB / 0.54 MiB (25) | 47.43 MiB / 47.44 MiB (25) |
| heap_used_asis_end | 1.78 MiB / 1.83 MiB (25) | 49.99 MiB / 49.99 MiB (25) | 1.60 MiB / 1.66 MiB (25) | 50.00 MiB / 50.00 MiB (25) |
| heap_inuse_gc_end | 1.61 MiB / 1.71 MiB (25) | 181.25 MiB / 181.50 MiB (25) | 1.63 MiB / 1.76 MiB (25) | 181.50 MiB / 182.00 MiB (25) |
| heap_inuse_asis_end | 3.20 MiB / 3.35 MiB (25) | 181.50 MiB / 181.50 MiB (25) | 3.09 MiB / 3.25 MiB (25) | 181.50 MiB / 182.25 MiB (25) |
| sys_asis_end | 13.41 MiB / 17.40 MiB (25) | 307.50 MiB / 309.00 MiB (25) | 13.42 MiB / 17.41 MiB (25) | 307.12 MiB / 310.00 MiB (25) |
| sys_gc_end | 13.68 MiB / 17.44 MiB (25) | 307.50 MiB / 309.00 MiB (25) | 13.44 MiB / 17.44 MiB (25) | 307.12 MiB / 310.00 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) |
| mallocs_workload | 13866 / 14374 (25) | - | 9617 / 10144 (25) | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| tool_ms:get_context_remaining | 4.9 / 6.5 (25) | 10.9 / 14.2 (25) | 4.6 / 6.0 (25) | 10.4 / 13.9 (25) |
| total_alloc_workload | 1.31 MiB / 1.35 MiB (25) | - | 1.12 MiB / 1.17 MiB (25) | - |

### Workload b, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 116.2 / 126.3 (25) | 876.4 / 893.9 (25) | 116.7 / 135.8 (25) | 861.2 / 878.5 (25) |
| first_turn_ms | 42.8 / 48.8 (25) | 69.4 / 74.3 (25) | 40.7 / 43.6 (25) | 68.5 / 72.6 (25) |
| to_first_tool_ms | 19.9 / 21.9 (25) | 35.3 / 37.3 (25) | 19.0 / 21.2 (25) | 34.9 / 37.9 (25) |
| turn_ms | 42.8 / 48.8 (25) | 69.4 / 74.3 (25) | 40.7 / 43.6 (25) | 68.5 / 72.6 (25) |
| cpu_total_ms_end | 140 / 160 (25) | 1300 / 1320 (25) | 140 / 150 (25) | 1280 / 1320 (25) |
| cpu_total_workload_ms | 40.0 / 60.0 (25) | 90.0 / 120.0 (25) | 50.0 / 60.0 (25) | 100.0 / 110.0 (25) |
| cpu_ext_workload_ms | 20.0 / 30.0 (25) | 60.0 / 80.0 (25) | 20.0 / 30.0 (25) | 70.0 / 80.0 (25) |
| ext_cpu_ms_end | 30 / 40 (25) | 1070 / 1090 (25) | 20 / 30 (25) | 1060 / 1100 (25) |
| ext_rss_end | 10.99 MiB / 12.04 MiB (25) | 167.53 MiB / 169.23 MiB (25) | 10.71 MiB / 11.95 MiB (25) | 167.82 MiB / 169.37 MiB (25) |
| ext_pss_end | 10.98 MiB / 12.04 MiB (25) | 163.66 MiB / 165.44 MiB (25) | 10.70 MiB / 11.95 MiB (25) | 163.97 MiB / 165.58 MiB (25) |
| tree_rss_end | 73.94 MiB / 74.91 MiB (25) | 257.04 MiB / 258.77 MiB (25) | 73.59 MiB / 74.59 MiB (25) | 257.28 MiB / 259.01 MiB (25) |
| tree_pss_end | 72.57 MiB / 73.55 MiB (25) | 251.77 MiB / 253.46 MiB (25) | 72.22 MiB / 73.16 MiB (25) | 251.98 MiB / 253.75 MiB (25) |
| ext_peak_rss_end | 10.99 MiB / 12.04 MiB (25) | 167.53 MiB / 169.23 MiB (25) | 10.71 MiB / 11.95 MiB (25) | 167.82 MiB / 169.37 MiB (25) |
| ext_anon_end | 4.01 MiB / 5.00 MiB (25) | 107.22 MiB / 109.08 MiB (25) | 3.78 MiB / 4.96 MiB (25) | 107.57 MiB / 109.24 MiB (25) |
| ext_rss_ready | 9.32 MiB / 9.55 MiB (25) | 160.94 MiB / 161.52 MiB (25) | 9.24 MiB / 9.55 MiB (25) | 161.05 MiB / 162.48 MiB (25) |
| ext_pss_ready | 9.31 MiB / 9.55 MiB (25) | 157.10 MiB / 157.68 MiB (25) | 9.24 MiB / 9.55 MiB (25) | 157.17 MiB / 158.58 MiB (25) |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB (25) | 35.17 MiB / 35.17 MiB (25) | 0.54 MiB / 0.55 MiB (25) | 35.18 MiB / 35.18 MiB (25) |
| heap_used_asis_end | 1.79 MiB / 1.83 MiB (25) | 36.54 MiB / 36.55 MiB (25) | 1.60 MiB / 1.69 MiB (25) | 36.55 MiB / 36.56 MiB (25) |
| heap_inuse_gc_end | 1.66 MiB / 1.75 MiB (25) | 70.34 MiB / 101.59 MiB (25) | 1.63 MiB / 1.72 MiB (25) | 70.09 MiB / 101.59 MiB (25) |
| heap_inuse_asis_end | 3.14 MiB / 3.41 MiB (25) | 70.34 MiB / 101.59 MiB (25) | 3.05 MiB / 3.30 MiB (25) | 70.09 MiB / 101.59 MiB (25) |
| sys_asis_end | 13.66 MiB / 13.91 MiB (25) | 158.67 MiB / 162.00 MiB (25) | 13.41 MiB / 13.92 MiB (25) | 156.00 MiB / 160.07 MiB (25) |
| sys_gc_end | 13.68 MiB / 17.61 MiB (25) | 158.67 MiB / 162.00 MiB (25) | 13.68 MiB / 13.94 MiB (25) | 156.00 MiB / 160.07 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) |
| mallocs_workload | 13881 / 14368 (25) | - | 9599 / 10541 (25) | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| tool_ms:get_context_remaining | 4.8 / 6.6 (25) | 12.9 / 14.4 (25) | 4.6 / 6.5 (25) | 12.1 / 14.8 (25) |
| total_alloc_workload | 1.32 MiB / 1.36 MiB (25) | - | 1.12 MiB / 1.21 MiB (25) | - |

### Workload c1, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 116.5 / 126.8 (25) | 801.3 / 884.8 (25) | 116.0 / 125.0 (25) | 823.0 / 881.1 (25) |
| to_first_tool_ms | 20.5 / 22.3 (25) | 34.3 / 37.3 (25) | 18.2 / 20.4 (25) | 34.5 / 37.7 (25) |
| turn_ms | 844.8 / 867.8 (25) | 785.9 / 847.6 (25) | 653.9 / 726.3 (25) | 721.7 / 818.7 (25) |
| cpu_total_ms_end | 1110 / 1130 (25) | 2070 / 2270 (25) | 850 / 990 (25) | 2030 / 2280 (25) |
| cpu_total_workload_ms | 1010.0 / 1050.0 (25) | 1030.0 / 1090.0 (25) | 770.0 / 900.0 (25) | 950.0 / 1070.0 (25) |
| cpu_ext_workload_ms | 530.0 / 550.0 (25) | 620.0 / 670.0 (25) | 360.0 / 420.0 (25) | 590.0 / 670.0 (25) |
| ext_cpu_ms_end | 540 / 560 (25) | 1510 / 1670 (25) | 370 / 430 (25) | 1500 / 1680 (25) |
| ext_rss_end | 14.42 MiB / 14.64 MiB (25) | 208.82 MiB / 210.01 MiB (25) | 14.24 MiB / 14.39 MiB (25) | 209.09 MiB / 210.13 MiB (25) |
| ext_pss_end | 14.42 MiB / 14.64 MiB (25) | 205.03 MiB / 206.10 MiB (25) | 14.23 MiB / 14.39 MiB (25) | 205.18 MiB / 206.22 MiB (25) |
| tree_rss_end | 85.35 MiB / 86.51 MiB (25) | 319.34 MiB / 323.37 MiB (25) | 86.04 MiB / 87.70 MiB (25) | 321.11 MiB / 323.86 MiB (25) |
| tree_pss_end | 83.92 MiB / 85.08 MiB (25) | 314.12 MiB / 318.11 MiB (25) | 84.61 MiB / 86.30 MiB (25) | 315.85 MiB / 318.60 MiB (25) |
| ext_peak_rss_end | 14.42 MiB / 14.64 MiB (25) | 208.82 MiB / 210.01 MiB (25) | 14.24 MiB / 14.39 MiB (25) | 209.09 MiB / 210.13 MiB (25) |
| ext_anon_end | 7.31 MiB / 7.47 MiB (25) | 148.70 MiB / 149.79 MiB (25) | 7.13 MiB / 7.27 MiB (25) | 148.77 MiB / 149.91 MiB (25) |
| ext_rss_ready | 9.36 MiB / 9.57 MiB (25) | 160.83 MiB / 162.06 MiB (25) | 9.36 MiB / 9.48 MiB (25) | 161.22 MiB / 162.24 MiB (25) |
| ext_pss_ready | 9.36 MiB / 9.57 MiB (25) | 156.94 MiB / 158.15 MiB (25) | 9.35 MiB / 9.48 MiB (25) | 157.36 MiB / 158.44 MiB (25) |
| heap_used_gc_end | 0.57 MiB / 0.57 MiB (25) | 35.89 MiB / 35.89 MiB (25) | 0.57 MiB / 0.58 MiB (25) | 35.89 MiB / 35.90 MiB (25) |
| heap_used_asis_end | 1.97 MiB / 2.77 MiB (25) | 60.37 MiB / 60.65 MiB (25) | 2.24 MiB / 2.53 MiB (25) | 60.37 MiB / 60.66 MiB (25) |
| heap_inuse_gc_end | 1.71 MiB / 1.83 MiB (25) | 102.59 MiB / 103.09 MiB (25) | 1.70 MiB / 1.79 MiB (25) | 102.59 MiB / 103.09 MiB (25) |
| heap_inuse_asis_end | 3.17 MiB / 3.88 MiB (25) | 107.29 MiB / 107.77 MiB (25) | 3.51 MiB / 3.92 MiB (25) | 107.05 MiB / 107.54 MiB (25) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (25) | 188.50 MiB / 198.81 MiB (25) | 17.69 MiB / 17.94 MiB (25) | 187.50 MiB / 197.18 MiB (25) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (25) | 185.31 MiB / 198.81 MiB (25) | 17.69 MiB / 17.94 MiB (25) | 184.30 MiB / 197.18 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) |
| mallocs_workload | 835118 / 836747 (25) | - | 496264 / 498033 (25) | - |
| num_gc_workload | 32 / 34 (25) | 4 / 4 (25) | 28 / 30 (25) | 4 / 4 (25) |
| tool_ms:notes.append | 5.8 / 7.6 (150) | 7.8 / 11.3 (150) | 5.0 / 6.9 (150) | 7.4 / 11.2 (150) |
| tool_ms:notes.list | 5.8 / 7.5 (150) | 9.0 / 14.8 (150) | 4.9 / 7.4 (150) | 8.8 / 15.0 (150) |
| tool_ms:notes.read | 5.4 / 7.5 (150) | 7.9 / 12.1 (150) | 4.2 / 6.4 (150) | 8.0 / 12.0 (150) |
| tool_ms:notes.search | 5.5 / 7.5 (150) | 8.9 / 12.4 (150) | 4.7 / 7.0 (150) | 8.7 / 12.2 (150) |
| tool_ms:notes.write | 6.1 / 7.8 (150) | 10.1 / 14.0 (150) | 5.1 / 6.6 (150) | 9.7 / 13.9 (150) |
| total_alloc_workload | 57.29 MiB / 57.70 MiB (25) | - | 47.73 MiB / 48.13 MiB (25) | - |

### Workload c100, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 116.6 / 123.3 (25) | 831.3 / 886.3 (25) | 116.5 / 129.1 (25) | 825.3 / 877.7 (25) |
| to_first_tool_ms | 20.3 / 23.1 (25) | 34.3 / 38.0 (25) | 18.6 / 20.9 (25) | 34.5 / 36.5 (25) |
| turn_ms | 1054.1 / 1132.0 (25) | 1058.9 / 1206.3 (25) | 801.7 / 859.9 (25) | 994.3 / 1130.1 (25) |
| cpu_total_ms_end | 1350 / 1470 (25) | 2420 / 2710 (25) | 1080 / 1170 (25) | 2440 / 2610 (25) |
| cpu_total_workload_ms | 1260.0 / 1390.0 (25) | 1280.0 / 1490.0 (25) | 1000.0 / 1070.0 (25) | 1240.0 / 1420.0 (25) |
| cpu_ext_workload_ms | 730.0 / 800.0 (25) | 850.0 / 1000.0 (25) | 510.0 / 560.0 (25) | 830.0 / 980.0 (25) |
| ext_cpu_ms_end | 740 / 800 (25) | 1810 / 2040 (25) | 510 / 570 (25) | 1830 / 1980 (25) |
| ext_rss_end | 14.88 MiB / 15.16 MiB (25) | 223.55 MiB / 226.24 MiB (25) | 14.61 MiB / 15.01 MiB (25) | 224.95 MiB / 226.31 MiB (25) |
| ext_pss_end | 14.88 MiB / 15.15 MiB (25) | 219.67 MiB / 222.36 MiB (25) | 14.61 MiB / 15.01 MiB (25) | 221.08 MiB / 222.47 MiB (25) |
| tree_rss_end | 87.14 MiB / 88.12 MiB (25) | 332.32 MiB / 338.35 MiB (25) | 87.70 MiB / 88.73 MiB (25) | 333.23 MiB / 339.05 MiB (25) |
| tree_pss_end | 85.80 MiB / 86.77 MiB (25) | 327.10 MiB / 333.11 MiB (25) | 86.29 MiB / 87.37 MiB (25) | 327.94 MiB / 333.79 MiB (25) |
| ext_peak_rss_end | 14.91 MiB / 15.16 MiB (25) | 223.55 MiB / 226.24 MiB (25) | 14.61 MiB / 15.01 MiB (25) | 224.95 MiB / 226.31 MiB (25) |
| ext_anon_end | 7.70 MiB / 7.98 MiB (25) | 163.34 MiB / 166.02 MiB (25) | 7.48 MiB / 7.88 MiB (25) | 164.78 MiB / 166.06 MiB (25) |
| ext_rss_ready | 9.36 MiB / 9.55 MiB (25) | 160.69 MiB / 161.78 MiB (25) | 9.30 MiB / 9.49 MiB (25) | 160.91 MiB / 161.77 MiB (25) |
| ext_pss_ready | 9.36 MiB / 9.54 MiB (25) | 156.88 MiB / 157.90 MiB (25) | 9.29 MiB / 9.49 MiB (25) | 157.03 MiB / 157.92 MiB (25) |
| heap_used_gc_end | 0.62 MiB / 0.64 MiB (25) | 36.07 MiB / 36.09 MiB (25) | 0.62 MiB / 0.64 MiB (25) | 36.08 MiB / 36.10 MiB (25) |
| heap_used_asis_end | 2.37 MiB / 2.41 MiB (25) | 58.24 MiB / 64.77 MiB (25) | 1.83 MiB / 2.80 MiB (25) | 58.25 MiB / 64.78 MiB (25) |
| heap_inuse_gc_end | 1.77 MiB / 1.88 MiB (25) | 103.59 MiB / 104.34 MiB (25) | 1.77 MiB / 1.85 MiB (25) | 103.59 MiB / 104.34 MiB (25) |
| heap_inuse_asis_end | 3.45 MiB / 3.54 MiB (25) | 110.09 MiB / 113.10 MiB (25) | 3.17 MiB / 4.04 MiB (25) | 110.09 MiB / 113.35 MiB (25) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (25) | 206.28 MiB / 218.06 MiB (25) | 17.76 MiB / 17.94 MiB (25) | 205.31 MiB / 217.72 MiB (25) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (25) | 201.14 MiB / 212.29 MiB (25) | 17.76 MiB / 17.94 MiB (25) | 200.62 MiB / 212.76 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) |
| mallocs_workload | 915027 / 917012 (25) | - | 578618 / 580872 (25) | - |
| num_gc_workload | 48 / 49 (25) | 6 / 6 (25) | 41 / 42 (25) | 6 / 6 (25) |
| tool_ms:notes.append | 5.9 / 7.6 (150) | 7.7 / 11.2 (150) | 5.5 / 7.2 (150) | 7.5 / 11.1 (150) |
| tool_ms:notes.list | 10.4 / 13.2 (150) | 21.0 / 30.6 (150) | 9.1 / 11.9 (150) | 20.6 / 29.1 (150) |
| tool_ms:notes.read | 5.9 / 7.7 (150) | 7.9 / 11.2 (150) | 5.0 / 6.9 (150) | 7.8 / 11.7 (150) |
| tool_ms:notes.search | 11.0 / 13.4 (150) | 31.0 / 41.5 (150) | 9.9 / 13.0 (150) | 29.2 / 39.1 (150) |
| tool_ms:notes.write | 6.3 / 8.2 (150) | 9.7 / 14.1 (150) | 5.7 / 7.0 (150) | 9.5 / 13.5 (150) |
| total_alloc_workload | 84.39 MiB / 84.67 MiB (25) | - | 67.16 MiB / 67.92 MiB (25) | - |

### Workload e, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | go-sf median / p90 (n) | ts-sf median / p90 (n) |
|---|---:|---:|---:|---:|
| startup_ms | 113.5 / 128.2 (25) | 813.1 / 887.1 (25) | 116.1 / 135.6 (25) | 870.8 / 884.3 (25) |
| to_first_tool_ms | 21.4 / 23.9 (25) | 36.6 / 40.5 (25) | 19.6 / 23.0 (25) | 36.0 / 38.8 (25) |
| turn_ms | 21590.2 / 21962.6 (25) | 16413.8 / 16877.1 (25) | 13147.6 / 13603.5 (25) | 13632.6 / 14213.6 (25) |
| cpu_total_ms_end | 26170 / 26920 (25) | 19880 / 20410 (25) | 16640 / 17360 (25) | 17250 / 18320 (25) |
| cpu_total_workload_ms | 26080.0 / 26820.0 (25) | 18760.0 / 19370.0 (25) | 16570.0 / 17260.0 (25) | 16110.0 / 17120.0 (25) |
| cpu_ext_workload_ms | 15530.0 / 15900.0 (25) | 9480.0 / 9950.0 (25) | 8680.0 / 9100.0 (25) | 9360.0 / 10060.0 (25) |
| ext_cpu_ms_end | 15530 / 15910 (25) | 10450 / 10820 (25) | 8690 / 9110 (25) | 10310 / 11060 (25) |
| ext_rss_end | 15.56 MiB / 22.21 MiB (25) | 484.84 MiB / 514.46 MiB (25) | 19.70 MiB / 24.93 MiB (25) | 454.79 MiB / 495.83 MiB (25) |
| ext_pss_end | 15.56 MiB / 22.21 MiB (25) | 480.96 MiB / 510.55 MiB (25) | 19.69 MiB / 24.93 MiB (25) | 450.86 MiB / 491.91 MiB (25) |
| tree_rss_end | 130.51 MiB / 135.41 MiB (25) | 615.47 MiB / 640.92 MiB (25) | 132.29 MiB / 139.77 MiB (25) | 590.32 MiB / 628.05 MiB (25) |
| tree_pss_end | 129.15 MiB / 134.00 MiB (25) | 610.23 MiB / 635.60 MiB (25) | 130.89 MiB / 138.40 MiB (25) | 585.01 MiB / 622.76 MiB (25) |
| ext_peak_rss_end | 21.43 MiB / 24.06 MiB (25) | 534.12 MiB / 546.41 MiB (25) | 20.24 MiB / 24.93 MiB (25) | 516.20 MiB / 539.96 MiB (25) |
| ext_anon_end | 8.39 MiB / 15.04 MiB (25) | 424.49 MiB / 454.18 MiB (25) | 12.56 MiB / 17.80 MiB (25) | 394.29 MiB / 435.34 MiB (25) |
| ext_rss_ready | 9.37 MiB / 9.55 MiB (25) | 161.29 MiB / 162.00 MiB (25) | 9.33 MiB / 9.47 MiB (25) | 161.27 MiB / 161.82 MiB (25) |
| ext_pss_ready | 9.36 MiB / 9.55 MiB (25) | 157.41 MiB / 158.20 MiB (25) | 9.32 MiB / 9.46 MiB (25) | 157.43 MiB / 157.90 MiB (25) |
| heap_used_gc_end | 0.71 MiB / 0.72 MiB (25) | 35.94 MiB / 36.81 MiB (25) | 0.71 MiB / 0.72 MiB (25) | 35.94 MiB / 36.86 MiB (25) |
| heap_used_asis_end | 2.35 MiB / 2.77 MiB (25) | 55.71 MiB / 79.34 MiB (25) | 2.02 MiB / 2.42 MiB (25) | 47.60 MiB / 110.53 MiB (25) |
| heap_inuse_gc_end | 2.41 MiB / 2.68 MiB (25) | 169.09 MiB / 169.84 MiB (25) | 2.46 MiB / 2.77 MiB (25) | 169.09 MiB / 169.59 MiB (25) |
| heap_inuse_asis_end | 4.06 MiB / 4.44 MiB (25) | 177.58 MiB / 199.22 MiB (25) | 3.91 MiB / 4.72 MiB (25) | 174.74 MiB / 229.86 MiB (25) |
| sys_asis_end | 25.94 MiB / 26.26 MiB (25) | 470.60 MiB / 492.88 MiB (25) | 25.94 MiB / 26.19 MiB (25) | 423.32 MiB / 522.68 MiB (25) |
| sys_gc_end | 25.94 MiB / 26.26 MiB (25) | 456.83 MiB / 471.07 MiB (25) | 25.94 MiB / 26.19 MiB (25) | 419.15 MiB / 484.84 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 200 / 200 (25) | 200 / 200 (25) | 200 / 200 (25) | 200 / 200 (25) |
| mallocs_workload | 12183835 / 12186583 (25) | - | 7319241 / 7328816 (25) | - |
| num_gc_workload | 1006 / 1014 (25) | 29 / 46 (25) | 768 / 776 (25) | 44 / 59 (25) |
| session_compaction | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) |
| session_context_window | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| session_custom_message | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) |
| session_entries | 531 / 531 (25) | 531 / 531 (25) | 531 / 531 (25) | 531 / 531 (25) |
| session_tool_results | 180 / 180 (25) | 180 / 180 (25) | 180 / 180 (25) | 180 / 180 (25) |
| tool_ms:get_context_remaining | 5.5 / 6.9 (750) | 7.5 / 9.4 (750) | 5.1 / 6.8 (750) | 5.9 / 9.1 (750) |
| tool_ms:notes.append | 6.9 / 8.4 (1500) | 9.8 / 12.3 (1500) | 6.6 / 8.1 (1500) | 8.7 / 11.8 (1500) |
| tool_ms:notes.write | 7.3 / 9.0 (1500) | 12.1 / 15.3 (1500) | 7.0 / 8.6 (1500) | 11.2 / 14.6 (1500) |
| tool_ms:read | 11.1 / 13.1 (750) | 12.4 / 14.3 (750) | 7.7 / 9.2 (750) | 11.3 / 13.8 (750) |
| total_alloc_workload | 2009.56 MiB / 2013.18 MiB (25) | - | 1425.69 MiB / 1428.61 MiB (25) | - |

## Behaviour parity of the workloads (model requests, session records)

identical across sides.

## Ratchet: go <= ts (Go <= TS on the same pig), every metric

| Workload | Metric | go median | ts median | go p90 | ts p90 | verdict |
|---|---|---:|---:|---:|---:|---|
| a/cold | startup_ms | 736.5 | 2293.2 | 762.1 | 2485.4 | pass |
| a/cold | cpu_total_ms_end | 1140 | 3740 | 1190 | 4070 | pass |
| a/cold | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/cold | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/cold | ext_cpu_ms_end | 10 | 3090 | 10 | 3420 | pass |
| a/cold | ext_rss_end | 9.34 MiB | 311.43 MiB | 9.59 MiB | 314.92 MiB | pass |
| a/cold | ext_pss_end | 9.33 MiB | 307.50 MiB | 9.59 MiB | 311.01 MiB | pass |
| a/cold | tree_rss_end | 82.64 MiB | 463.91 MiB | 85.45 MiB | 473.21 MiB | pass |
| a/cold | tree_pss_end | 81.28 MiB | 458.49 MiB | 84.00 MiB | 467.87 MiB | pass |
| a/cold | ext_peak_rss_end | 9.34 MiB | 311.43 MiB | 9.59 MiB | 314.92 MiB | pass |
| a/cold | ext_anon_end | 2.42 MiB | 255.71 MiB | 2.61 MiB | 259.29 MiB | pass |
| a/cold | ext_rss_ready | 9.34 MiB | 311.43 MiB | 9.59 MiB | 314.92 MiB | pass |
| a/cold | ext_pss_ready | 9.33 MiB | 307.50 MiB | 9.59 MiB | 311.01 MiB | pass |
| a/cold | heap_used_gc_end | 0.51 MiB | 47.18 MiB | 0.51 MiB | 47.19 MiB | pass |
| a/cold | heap_used_asis_end | 0.52 MiB | 48.61 MiB | 0.52 MiB | 48.62 MiB | pass |
| a/cold | heap_inuse_gc_end | 1.55 MiB | 181.25 MiB | 1.59 MiB | 182.00 MiB | pass |
| a/cold | heap_inuse_asis_end | 1.55 MiB | 181.25 MiB | 1.60 MiB | 182.00 MiB | pass |
| a/cold | sys_asis_end | 13.41 MiB | 301.98 MiB | 13.60 MiB | 304.50 MiB | pass |
| a/cold | sys_gc_end | 13.42 MiB | 301.98 MiB | 13.68 MiB | 304.50 MiB | pass |
| a/warm | startup_ms | 117.1 | 855.7 | 126.5 | 885.6 | pass |
| a/warm | cpu_total_ms_end | 90 | 1180 | 100 | 1220 | pass |
| a/warm | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/warm | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/warm | ext_cpu_ms_end | 10 | 1000 | 10 | 1020 | pass |
| a/warm | ext_rss_end | 9.40 MiB | 161.08 MiB | 9.59 MiB | 161.93 MiB | pass |
| a/warm | ext_pss_end | 9.40 MiB | 157.22 MiB | 9.59 MiB | 158.02 MiB | pass |
| a/warm | tree_rss_end | 70.00 MiB | 245.41 MiB | 70.67 MiB | 246.10 MiB | pass |
| a/warm | tree_pss_end | 68.55 MiB | 240.09 MiB | 69.34 MiB | 240.77 MiB | pass |
| a/warm | ext_peak_rss_end | 9.40 MiB | 161.08 MiB | 9.59 MiB | 161.93 MiB | pass |
| a/warm | ext_anon_end | 2.48 MiB | 105.54 MiB | 2.67 MiB | 106.34 MiB | pass |
| a/warm | ext_rss_ready | 9.40 MiB | 161.08 MiB | 9.59 MiB | 161.93 MiB | pass |
| a/warm | ext_pss_ready | 9.40 MiB | 157.22 MiB | 9.59 MiB | 158.02 MiB | pass |
| a/warm | heap_used_gc_end | 0.51 MiB | 34.94 MiB | 0.51 MiB | 34.94 MiB | pass |
| a/warm | heap_used_asis_end | 0.52 MiB | 35.20 MiB | 0.52 MiB | 35.20 MiB | pass |
| a/warm | heap_inuse_gc_end | 1.48 MiB | 69.84 MiB | 1.60 MiB | 101.59 MiB | pass |
| a/warm | heap_inuse_asis_end | 1.48 MiB | 70.09 MiB | 1.61 MiB | 101.59 MiB | pass |
| a/warm | sys_asis_end | 13.40 MiB | 149.09 MiB | 13.66 MiB | 154.30 MiB | pass |
| a/warm | sys_gc_end | 13.42 MiB | 149.09 MiB | 13.68 MiB | 154.30 MiB | pass |
| b/cold | startup_ms | 753.7 | 2382.2 | 790.7 | 2548.0 | pass |
| b/cold | first_turn_ms | 47.0 | 65.8 | 50.6 | 71.6 | pass |
| b/cold | to_first_tool_ms | 20.8 | 34.4 | 25.0 | 37.3 | pass |
| b/cold | turn_ms | 47.0 | 65.8 | 50.6 | 71.6 | pass |
| b/cold | cpu_total_ms_end | 1210 | 3980 | 1250 | 4270 | pass |
| b/cold | cpu_total_workload_ms | 50.0 | 90.0 | 60.0 | 110.0 | pass |
| b/cold | cpu_ext_workload_ms | 20.0 | 60.0 | 30.0 | 80.0 | pass |
| b/cold | ext_cpu_ms_end | 30 | 3280 | 40 | 3560 | pass |
| b/cold | ext_rss_end | 11.06 MiB | 316.81 MiB | 12.18 MiB | 319.59 MiB | pass |
| b/cold | ext_pss_end | 11.05 MiB | 312.89 MiB | 12.18 MiB | 315.62 MiB | pass |
| b/cold | tree_rss_end | 85.83 MiB | 473.58 MiB | 87.69 MiB | 483.43 MiB | pass |
| b/cold | tree_pss_end | 84.43 MiB | 468.35 MiB | 86.28 MiB | 478.04 MiB | pass |
| b/cold | ext_peak_rss_end | 11.06 MiB | 316.81 MiB | 12.18 MiB | 319.59 MiB | pass |
| b/cold | ext_anon_end | 4.08 MiB | 256.34 MiB | 5.13 MiB | 259.21 MiB | pass |
| b/cold | ext_rss_ready | 9.37 MiB | 311.09 MiB | 9.57 MiB | 313.46 MiB | pass |
| b/cold | ext_pss_ready | 9.36 MiB | 307.18 MiB | 9.57 MiB | 309.50 MiB | pass |
| b/cold | heap_used_gc_end | 0.54 MiB | 47.42 MiB | 0.54 MiB | 47.43 MiB | pass |
| b/cold | heap_used_asis_end | 1.78 MiB | 49.99 MiB | 1.83 MiB | 49.99 MiB | pass |
| b/cold | heap_inuse_gc_end | 1.61 MiB | 181.25 MiB | 1.71 MiB | 181.50 MiB | pass |
| b/cold | heap_inuse_asis_end | 3.20 MiB | 181.50 MiB | 3.35 MiB | 181.50 MiB | pass |
| b/cold | sys_asis_end | 13.41 MiB | 307.50 MiB | 17.40 MiB | 309.00 MiB | pass |
| b/cold | sys_gc_end | 13.68 MiB | 307.50 MiB | 17.44 MiB | 309.00 MiB | pass |
| b/cold | tool_ms:get_context_remaining | 4.9 | 10.9 | 6.5 | 14.2 | pass |
| b/warm | startup_ms | 116.2 | 876.4 | 126.3 | 893.9 | pass |
| b/warm | first_turn_ms | 42.8 | 69.4 | 48.8 | 74.3 | pass |
| b/warm | to_first_tool_ms | 19.9 | 35.3 | 21.9 | 37.3 | pass |
| b/warm | turn_ms | 42.8 | 69.4 | 48.8 | 74.3 | pass |
| b/warm | cpu_total_ms_end | 140 | 1300 | 160 | 1320 | pass |
| b/warm | cpu_total_workload_ms | 40.0 | 90.0 | 60.0 | 120.0 | pass |
| b/warm | cpu_ext_workload_ms | 20.0 | 60.0 | 30.0 | 80.0 | pass |
| b/warm | ext_cpu_ms_end | 30 | 1070 | 40 | 1090 | pass |
| b/warm | ext_rss_end | 10.99 MiB | 167.53 MiB | 12.04 MiB | 169.23 MiB | pass |
| b/warm | ext_pss_end | 10.98 MiB | 163.66 MiB | 12.04 MiB | 165.44 MiB | pass |
| b/warm | tree_rss_end | 73.94 MiB | 257.04 MiB | 74.91 MiB | 258.77 MiB | pass |
| b/warm | tree_pss_end | 72.57 MiB | 251.77 MiB | 73.55 MiB | 253.46 MiB | pass |
| b/warm | ext_peak_rss_end | 10.99 MiB | 167.53 MiB | 12.04 MiB | 169.23 MiB | pass |
| b/warm | ext_anon_end | 4.01 MiB | 107.22 MiB | 5.00 MiB | 109.08 MiB | pass |
| b/warm | ext_rss_ready | 9.32 MiB | 160.94 MiB | 9.55 MiB | 161.52 MiB | pass |
| b/warm | ext_pss_ready | 9.31 MiB | 157.10 MiB | 9.55 MiB | 157.68 MiB | pass |
| b/warm | heap_used_gc_end | 0.54 MiB | 35.17 MiB | 0.54 MiB | 35.17 MiB | pass |
| b/warm | heap_used_asis_end | 1.79 MiB | 36.54 MiB | 1.83 MiB | 36.55 MiB | pass |
| b/warm | heap_inuse_gc_end | 1.66 MiB | 70.34 MiB | 1.75 MiB | 101.59 MiB | pass |
| b/warm | heap_inuse_asis_end | 3.14 MiB | 70.34 MiB | 3.41 MiB | 101.59 MiB | pass |
| b/warm | sys_asis_end | 13.66 MiB | 158.67 MiB | 13.91 MiB | 162.00 MiB | pass |
| b/warm | sys_gc_end | 13.68 MiB | 158.67 MiB | 17.61 MiB | 162.00 MiB | pass |
| b/warm | tool_ms:get_context_remaining | 4.8 | 12.9 | 6.6 | 14.4 | pass |
| c1/warm | startup_ms | 116.5 | 801.3 | 126.8 | 884.8 | pass |
| c1/warm | to_first_tool_ms | 20.5 | 34.3 | 22.3 | 37.3 | pass |
| c1/warm | turn_ms | 844.8 | 785.9 | 867.8 | 847.6 | FAIL (median) |
| c1/warm | cpu_total_ms_end | 1110 | 2070 | 1130 | 2270 | pass |
| c1/warm | cpu_total_workload_ms | 1010.0 | 1030.0 | 1050.0 | 1090.0 | pass |
| c1/warm | cpu_ext_workload_ms | 530.0 | 620.0 | 550.0 | 670.0 | pass |
| c1/warm | ext_cpu_ms_end | 540 | 1510 | 560 | 1670 | pass |
| c1/warm | ext_rss_end | 14.42 MiB | 208.82 MiB | 14.64 MiB | 210.01 MiB | pass |
| c1/warm | ext_pss_end | 14.42 MiB | 205.03 MiB | 14.64 MiB | 206.10 MiB | pass |
| c1/warm | tree_rss_end | 85.35 MiB | 319.34 MiB | 86.51 MiB | 323.37 MiB | pass |
| c1/warm | tree_pss_end | 83.92 MiB | 314.12 MiB | 85.08 MiB | 318.11 MiB | pass |
| c1/warm | ext_peak_rss_end | 14.42 MiB | 208.82 MiB | 14.64 MiB | 210.01 MiB | pass |
| c1/warm | ext_anon_end | 7.31 MiB | 148.70 MiB | 7.47 MiB | 149.79 MiB | pass |
| c1/warm | ext_rss_ready | 9.36 MiB | 160.83 MiB | 9.57 MiB | 162.06 MiB | pass |
| c1/warm | ext_pss_ready | 9.36 MiB | 156.94 MiB | 9.57 MiB | 158.15 MiB | pass |
| c1/warm | heap_used_gc_end | 0.57 MiB | 35.89 MiB | 0.57 MiB | 35.89 MiB | pass |
| c1/warm | heap_used_asis_end | 1.97 MiB | 60.37 MiB | 2.77 MiB | 60.65 MiB | pass |
| c1/warm | heap_inuse_gc_end | 1.71 MiB | 102.59 MiB | 1.83 MiB | 103.09 MiB | pass |
| c1/warm | heap_inuse_asis_end | 3.17 MiB | 107.29 MiB | 3.88 MiB | 107.77 MiB | pass |
| c1/warm | sys_asis_end | 17.69 MiB | 188.50 MiB | 17.94 MiB | 198.81 MiB | pass |
| c1/warm | sys_gc_end | 17.69 MiB | 185.31 MiB | 17.94 MiB | 198.81 MiB | pass |
| c1/warm | tool_ms:notes.append | 5.8 | 7.8 | 7.6 | 11.3 | pass |
| c1/warm | tool_ms:notes.list | 5.8 | 9.0 | 7.5 | 14.8 | pass |
| c1/warm | tool_ms:notes.read | 5.4 | 7.9 | 7.5 | 12.1 | pass |
| c1/warm | tool_ms:notes.search | 5.5 | 8.9 | 7.5 | 12.4 | pass |
| c1/warm | tool_ms:notes.write | 6.1 | 10.1 | 7.8 | 14.0 | pass |
| c100/warm | startup_ms | 116.6 | 831.3 | 123.3 | 886.3 | pass |
| c100/warm | to_first_tool_ms | 20.3 | 34.3 | 23.1 | 38.0 | pass |
| c100/warm | turn_ms | 1054.1 | 1058.9 | 1132.0 | 1206.3 | pass |
| c100/warm | cpu_total_ms_end | 1350 | 2420 | 1470 | 2710 | pass |
| c100/warm | cpu_total_workload_ms | 1260.0 | 1280.0 | 1390.0 | 1490.0 | pass |
| c100/warm | cpu_ext_workload_ms | 730.0 | 850.0 | 800.0 | 1000.0 | pass |
| c100/warm | ext_cpu_ms_end | 740 | 1810 | 800 | 2040 | pass |
| c100/warm | ext_rss_end | 14.88 MiB | 223.55 MiB | 15.16 MiB | 226.24 MiB | pass |
| c100/warm | ext_pss_end | 14.88 MiB | 219.67 MiB | 15.15 MiB | 222.36 MiB | pass |
| c100/warm | tree_rss_end | 87.14 MiB | 332.32 MiB | 88.12 MiB | 338.35 MiB | pass |
| c100/warm | tree_pss_end | 85.80 MiB | 327.10 MiB | 86.77 MiB | 333.11 MiB | pass |
| c100/warm | ext_peak_rss_end | 14.91 MiB | 223.55 MiB | 15.16 MiB | 226.24 MiB | pass |
| c100/warm | ext_anon_end | 7.70 MiB | 163.34 MiB | 7.98 MiB | 166.02 MiB | pass |
| c100/warm | ext_rss_ready | 9.36 MiB | 160.69 MiB | 9.55 MiB | 161.78 MiB | pass |
| c100/warm | ext_pss_ready | 9.36 MiB | 156.88 MiB | 9.54 MiB | 157.90 MiB | pass |
| c100/warm | heap_used_gc_end | 0.62 MiB | 36.07 MiB | 0.64 MiB | 36.09 MiB | pass |
| c100/warm | heap_used_asis_end | 2.37 MiB | 58.24 MiB | 2.41 MiB | 64.77 MiB | pass |
| c100/warm | heap_inuse_gc_end | 1.77 MiB | 103.59 MiB | 1.88 MiB | 104.34 MiB | pass |
| c100/warm | heap_inuse_asis_end | 3.45 MiB | 110.09 MiB | 3.54 MiB | 113.10 MiB | pass |
| c100/warm | sys_asis_end | 17.69 MiB | 206.28 MiB | 17.94 MiB | 218.06 MiB | pass |
| c100/warm | sys_gc_end | 17.69 MiB | 201.14 MiB | 17.94 MiB | 212.29 MiB | pass |
| c100/warm | tool_ms:notes.append | 5.9 | 7.7 | 7.6 | 11.2 | pass |
| c100/warm | tool_ms:notes.list | 10.4 | 21.0 | 13.2 | 30.6 | pass |
| c100/warm | tool_ms:notes.read | 5.9 | 7.9 | 7.7 | 11.2 | pass |
| c100/warm | tool_ms:notes.search | 11.0 | 31.0 | 13.4 | 41.5 | pass |
| c100/warm | tool_ms:notes.write | 6.3 | 9.7 | 8.2 | 14.1 | pass |
| e/warm | startup_ms | 113.5 | 813.1 | 128.2 | 887.1 | pass |
| e/warm | to_first_tool_ms | 21.4 | 36.6 | 23.9 | 40.5 | pass |
| e/warm | turn_ms | 21590.2 | 16413.8 | 21962.6 | 16877.1 | FAIL (median) |
| e/warm | cpu_total_ms_end | 26170 | 19880 | 26920 | 20410 | FAIL (median) |
| e/warm | cpu_total_workload_ms | 26080.0 | 18760.0 | 26820.0 | 19370.0 | FAIL (median) |
| e/warm | cpu_ext_workload_ms | 15530.0 | 9480.0 | 15900.0 | 9950.0 | FAIL (median) |
| e/warm | ext_cpu_ms_end | 15530 | 10450 | 15910 | 10820 | FAIL (median) |
| e/warm | ext_rss_end | 15.56 MiB | 484.84 MiB | 22.21 MiB | 514.46 MiB | pass |
| e/warm | ext_pss_end | 15.56 MiB | 480.96 MiB | 22.21 MiB | 510.55 MiB | pass |
| e/warm | tree_rss_end | 130.51 MiB | 615.47 MiB | 135.41 MiB | 640.92 MiB | pass |
| e/warm | tree_pss_end | 129.15 MiB | 610.23 MiB | 134.00 MiB | 635.60 MiB | pass |
| e/warm | ext_peak_rss_end | 21.43 MiB | 534.12 MiB | 24.06 MiB | 546.41 MiB | pass |
| e/warm | ext_anon_end | 8.39 MiB | 424.49 MiB | 15.04 MiB | 454.18 MiB | pass |
| e/warm | ext_rss_ready | 9.37 MiB | 161.29 MiB | 9.55 MiB | 162.00 MiB | pass |
| e/warm | ext_pss_ready | 9.36 MiB | 157.41 MiB | 9.55 MiB | 158.20 MiB | pass |
| e/warm | heap_used_gc_end | 0.71 MiB | 35.94 MiB | 0.72 MiB | 36.81 MiB | pass |
| e/warm | heap_used_asis_end | 2.35 MiB | 55.71 MiB | 2.77 MiB | 79.34 MiB | pass |
| e/warm | heap_inuse_gc_end | 2.41 MiB | 169.09 MiB | 2.68 MiB | 169.84 MiB | pass |
| e/warm | heap_inuse_asis_end | 4.06 MiB | 177.58 MiB | 4.44 MiB | 199.22 MiB | pass |
| e/warm | sys_asis_end | 25.94 MiB | 470.60 MiB | 26.26 MiB | 492.88 MiB | pass |
| e/warm | sys_gc_end | 25.94 MiB | 456.83 MiB | 26.26 MiB | 471.07 MiB | pass |
| e/warm | tool_ms:get_context_remaining | 5.5 | 7.5 | 6.9 | 9.4 | pass |
| e/warm | tool_ms:notes.append | 6.9 | 9.8 | 8.4 | 12.3 | pass |
| e/warm | tool_ms:notes.write | 7.3 | 12.1 | 9.0 | 15.3 | pass |
| e/warm | tool_ms:read | 11.1 | 12.4 | 13.1 | 14.3 | pass |

Median misses (go vs ts): 6.

## Ratchet: go-sf <= ts-sf (Go <= TS on the same pig), every metric

| Workload | Metric | go-sf median | ts-sf median | go-sf p90 | ts-sf p90 | verdict |
|---|---|---:|---:|---:|---:|---|
| a/cold | startup_ms | 739.3 | 2324.2 | 794.9 | 2526.1 | pass |
| a/cold | cpu_total_ms_end | 1130 | 3810 | 1200 | 4170 | pass |
| a/cold | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/cold | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/cold | ext_cpu_ms_end | 10 | 3150 | 10 | 3490 | pass |
| a/cold | ext_rss_end | 9.37 MiB | 311.56 MiB | 9.58 MiB | 314.60 MiB | pass |
| a/cold | ext_pss_end | 9.36 MiB | 307.60 MiB | 9.58 MiB | 310.63 MiB | pass |
| a/cold | tree_rss_end | 83.06 MiB | 460.76 MiB | 86.72 MiB | 473.34 MiB | pass |
| a/cold | tree_pss_end | 81.67 MiB | 455.43 MiB | 85.31 MiB | 468.04 MiB | pass |
| a/cold | ext_peak_rss_end | 9.37 MiB | 311.56 MiB | 9.58 MiB | 314.60 MiB | pass |
| a/cold | ext_anon_end | 2.45 MiB | 255.86 MiB | 2.71 MiB | 258.83 MiB | pass |
| a/cold | ext_rss_ready | 9.37 MiB | 311.56 MiB | 9.58 MiB | 314.60 MiB | pass |
| a/cold | ext_pss_ready | 9.36 MiB | 307.60 MiB | 9.58 MiB | 310.63 MiB | pass |
| a/cold | heap_used_gc_end | 0.51 MiB | 47.19 MiB | 0.51 MiB | 47.19 MiB | pass |
| a/cold | heap_used_asis_end | 0.52 MiB | 48.62 MiB | 0.52 MiB | 48.63 MiB | pass |
| a/cold | heap_inuse_gc_end | 1.51 MiB | 181.25 MiB | 1.60 MiB | 182.00 MiB | pass |
| a/cold | heap_inuse_asis_end | 1.52 MiB | 181.25 MiB | 1.61 MiB | 182.00 MiB | pass |
| a/cold | sys_asis_end | 13.40 MiB | 302.02 MiB | 13.66 MiB | 304.50 MiB | pass |
| a/cold | sys_gc_end | 13.42 MiB | 302.02 MiB | 13.68 MiB | 304.50 MiB | pass |
| a/warm | startup_ms | 116.0 | 849.6 | 127.4 | 880.7 | pass |
| a/warm | cpu_total_ms_end | 90 | 1160 | 100 | 1210 | pass |
| a/warm | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/warm | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/warm | ext_cpu_ms_end | 10 | 970 | 10 | 1020 | pass |
| a/warm | ext_rss_end | 9.36 MiB | 160.73 MiB | 9.58 MiB | 161.91 MiB | pass |
| a/warm | ext_pss_end | 9.36 MiB | 156.81 MiB | 9.58 MiB | 158.04 MiB | pass |
| a/warm | tree_rss_end | 70.17 MiB | 245.02 MiB | 71.06 MiB | 246.19 MiB | pass |
| a/warm | tree_pss_end | 68.82 MiB | 239.67 MiB | 69.63 MiB | 240.93 MiB | pass |
| a/warm | ext_peak_rss_end | 9.36 MiB | 160.73 MiB | 9.58 MiB | 161.91 MiB | pass |
| a/warm | ext_anon_end | 2.50 MiB | 104.99 MiB | 2.71 MiB | 106.43 MiB | pass |
| a/warm | ext_rss_ready | 9.36 MiB | 160.73 MiB | 9.58 MiB | 161.91 MiB | pass |
| a/warm | ext_pss_ready | 9.36 MiB | 156.81 MiB | 9.58 MiB | 158.04 MiB | pass |
| a/warm | heap_used_gc_end | 0.51 MiB | 34.95 MiB | 0.51 MiB | 34.95 MiB | pass |
| a/warm | heap_used_asis_end | 0.52 MiB | 35.20 MiB | 0.52 MiB | 35.21 MiB | pass |
| a/warm | heap_inuse_gc_end | 1.53 MiB | 69.84 MiB | 1.57 MiB | 101.09 MiB | pass |
| a/warm | heap_inuse_asis_end | 1.54 MiB | 70.09 MiB | 1.58 MiB | 101.09 MiB | pass |
| a/warm | sys_asis_end | 13.41 MiB | 149.61 MiB | 13.66 MiB | 152.32 MiB | pass |
| a/warm | sys_gc_end | 13.43 MiB | 149.61 MiB | 13.68 MiB | 152.32 MiB | pass |
| b/cold | startup_ms | 774.1 | 2351.1 | 787.1 | 2542.0 | pass |
| b/cold | first_turn_ms | 40.7 | 62.2 | 46.3 | 71.3 | pass |
| b/cold | to_first_tool_ms | 19.8 | 33.5 | 22.3 | 36.7 | pass |
| b/cold | turn_ms | 40.7 | 62.2 | 46.3 | 71.3 | pass |
| b/cold | cpu_total_ms_end | 1230 | 3920 | 1250 | 4260 | pass |
| b/cold | cpu_total_workload_ms | 40.0 | 90.0 | 60.0 | 110.0 | pass |
| b/cold | cpu_ext_workload_ms | 20.0 | 70.0 | 20.0 | 80.0 | pass |
| b/cold | ext_cpu_ms_end | 20 | 3240 | 30 | 3550 | pass |
| b/cold | ext_rss_end | 10.71 MiB | 316.57 MiB | 10.89 MiB | 319.12 MiB | pass |
| b/cold | ext_pss_end | 10.71 MiB | 312.64 MiB | 10.89 MiB | 315.20 MiB | pass |
| b/cold | tree_rss_end | 84.25 MiB | 480.62 MiB | 87.10 MiB | 484.38 MiB | pass |
| b/cold | tree_pss_end | 82.92 MiB | 475.35 MiB | 85.69 MiB | 479.08 MiB | pass |
| b/cold | ext_peak_rss_end | 10.71 MiB | 316.57 MiB | 10.89 MiB | 319.12 MiB | pass |
| b/cold | ext_anon_end | 3.78 MiB | 256.14 MiB | 3.96 MiB | 258.83 MiB | pass |
| b/cold | ext_rss_ready | 9.27 MiB | 311.45 MiB | 9.45 MiB | 313.80 MiB | pass |
| b/cold | ext_pss_ready | 9.26 MiB | 307.52 MiB | 9.45 MiB | 309.89 MiB | pass |
| b/cold | heap_used_gc_end | 0.54 MiB | 47.43 MiB | 0.54 MiB | 47.44 MiB | pass |
| b/cold | heap_used_asis_end | 1.60 MiB | 50.00 MiB | 1.66 MiB | 50.00 MiB | pass |
| b/cold | heap_inuse_gc_end | 1.63 MiB | 181.50 MiB | 1.76 MiB | 182.00 MiB | pass |
| b/cold | heap_inuse_asis_end | 3.09 MiB | 181.50 MiB | 3.25 MiB | 182.25 MiB | pass |
| b/cold | sys_asis_end | 13.42 MiB | 307.12 MiB | 17.41 MiB | 310.00 MiB | pass |
| b/cold | sys_gc_end | 13.44 MiB | 307.12 MiB | 17.44 MiB | 310.00 MiB | pass |
| b/cold | tool_ms:get_context_remaining | 4.6 | 10.4 | 6.0 | 13.9 | pass |
| b/warm | startup_ms | 116.7 | 861.2 | 135.8 | 878.5 | pass |
| b/warm | first_turn_ms | 40.7 | 68.5 | 43.6 | 72.6 | pass |
| b/warm | to_first_tool_ms | 19.0 | 34.9 | 21.2 | 37.9 | pass |
| b/warm | turn_ms | 40.7 | 68.5 | 43.6 | 72.6 | pass |
| b/warm | cpu_total_ms_end | 140 | 1280 | 150 | 1320 | pass |
| b/warm | cpu_total_workload_ms | 50.0 | 100.0 | 60.0 | 110.0 | pass |
| b/warm | cpu_ext_workload_ms | 20.0 | 70.0 | 30.0 | 80.0 | pass |
| b/warm | ext_cpu_ms_end | 20 | 1060 | 30 | 1100 | pass |
| b/warm | ext_rss_end | 10.71 MiB | 167.82 MiB | 11.95 MiB | 169.37 MiB | pass |
| b/warm | ext_pss_end | 10.70 MiB | 163.97 MiB | 11.95 MiB | 165.58 MiB | pass |
| b/warm | tree_rss_end | 73.59 MiB | 257.28 MiB | 74.59 MiB | 259.01 MiB | pass |
| b/warm | tree_pss_end | 72.22 MiB | 251.98 MiB | 73.16 MiB | 253.75 MiB | pass |
| b/warm | ext_peak_rss_end | 10.71 MiB | 167.82 MiB | 11.95 MiB | 169.37 MiB | pass |
| b/warm | ext_anon_end | 3.78 MiB | 107.57 MiB | 4.96 MiB | 109.24 MiB | pass |
| b/warm | ext_rss_ready | 9.24 MiB | 161.05 MiB | 9.55 MiB | 162.48 MiB | pass |
| b/warm | ext_pss_ready | 9.24 MiB | 157.17 MiB | 9.55 MiB | 158.58 MiB | pass |
| b/warm | heap_used_gc_end | 0.54 MiB | 35.18 MiB | 0.55 MiB | 35.18 MiB | pass |
| b/warm | heap_used_asis_end | 1.60 MiB | 36.55 MiB | 1.69 MiB | 36.56 MiB | pass |
| b/warm | heap_inuse_gc_end | 1.63 MiB | 70.09 MiB | 1.72 MiB | 101.59 MiB | pass |
| b/warm | heap_inuse_asis_end | 3.05 MiB | 70.09 MiB | 3.30 MiB | 101.59 MiB | pass |
| b/warm | sys_asis_end | 13.41 MiB | 156.00 MiB | 13.92 MiB | 160.07 MiB | pass |
| b/warm | sys_gc_end | 13.68 MiB | 156.00 MiB | 13.94 MiB | 160.07 MiB | pass |
| b/warm | tool_ms:get_context_remaining | 4.6 | 12.1 | 6.5 | 14.8 | pass |
| c1/warm | startup_ms | 116.0 | 823.0 | 125.0 | 881.1 | pass |
| c1/warm | to_first_tool_ms | 18.2 | 34.5 | 20.4 | 37.7 | pass |
| c1/warm | turn_ms | 653.9 | 721.7 | 726.3 | 818.7 | pass |
| c1/warm | cpu_total_ms_end | 850 | 2030 | 990 | 2280 | pass |
| c1/warm | cpu_total_workload_ms | 770.0 | 950.0 | 900.0 | 1070.0 | pass |
| c1/warm | cpu_ext_workload_ms | 360.0 | 590.0 | 420.0 | 670.0 | pass |
| c1/warm | ext_cpu_ms_end | 370 | 1500 | 430 | 1680 | pass |
| c1/warm | ext_rss_end | 14.24 MiB | 209.09 MiB | 14.39 MiB | 210.13 MiB | pass |
| c1/warm | ext_pss_end | 14.23 MiB | 205.18 MiB | 14.39 MiB | 206.22 MiB | pass |
| c1/warm | tree_rss_end | 86.04 MiB | 321.11 MiB | 87.70 MiB | 323.86 MiB | pass |
| c1/warm | tree_pss_end | 84.61 MiB | 315.85 MiB | 86.30 MiB | 318.60 MiB | pass |
| c1/warm | ext_peak_rss_end | 14.24 MiB | 209.09 MiB | 14.39 MiB | 210.13 MiB | pass |
| c1/warm | ext_anon_end | 7.13 MiB | 148.77 MiB | 7.27 MiB | 149.91 MiB | pass |
| c1/warm | ext_rss_ready | 9.36 MiB | 161.22 MiB | 9.48 MiB | 162.24 MiB | pass |
| c1/warm | ext_pss_ready | 9.35 MiB | 157.36 MiB | 9.48 MiB | 158.44 MiB | pass |
| c1/warm | heap_used_gc_end | 0.57 MiB | 35.89 MiB | 0.58 MiB | 35.90 MiB | pass |
| c1/warm | heap_used_asis_end | 2.24 MiB | 60.37 MiB | 2.53 MiB | 60.66 MiB | pass |
| c1/warm | heap_inuse_gc_end | 1.70 MiB | 102.59 MiB | 1.79 MiB | 103.09 MiB | pass |
| c1/warm | heap_inuse_asis_end | 3.51 MiB | 107.05 MiB | 3.92 MiB | 107.54 MiB | pass |
| c1/warm | sys_asis_end | 17.69 MiB | 187.50 MiB | 17.94 MiB | 197.18 MiB | pass |
| c1/warm | sys_gc_end | 17.69 MiB | 184.30 MiB | 17.94 MiB | 197.18 MiB | pass |
| c1/warm | tool_ms:notes.append | 5.0 | 7.4 | 6.9 | 11.2 | pass |
| c1/warm | tool_ms:notes.list | 4.9 | 8.8 | 7.4 | 15.0 | pass |
| c1/warm | tool_ms:notes.read | 4.2 | 8.0 | 6.4 | 12.0 | pass |
| c1/warm | tool_ms:notes.search | 4.7 | 8.7 | 7.0 | 12.2 | pass |
| c1/warm | tool_ms:notes.write | 5.1 | 9.7 | 6.6 | 13.9 | pass |
| c100/warm | startup_ms | 116.5 | 825.3 | 129.1 | 877.7 | pass |
| c100/warm | to_first_tool_ms | 18.6 | 34.5 | 20.9 | 36.5 | pass |
| c100/warm | turn_ms | 801.7 | 994.3 | 859.9 | 1130.1 | pass |
| c100/warm | cpu_total_ms_end | 1080 | 2440 | 1170 | 2610 | pass |
| c100/warm | cpu_total_workload_ms | 1000.0 | 1240.0 | 1070.0 | 1420.0 | pass |
| c100/warm | cpu_ext_workload_ms | 510.0 | 830.0 | 560.0 | 980.0 | pass |
| c100/warm | ext_cpu_ms_end | 510 | 1830 | 570 | 1980 | pass |
| c100/warm | ext_rss_end | 14.61 MiB | 224.95 MiB | 15.01 MiB | 226.31 MiB | pass |
| c100/warm | ext_pss_end | 14.61 MiB | 221.08 MiB | 15.01 MiB | 222.47 MiB | pass |
| c100/warm | tree_rss_end | 87.70 MiB | 333.23 MiB | 88.73 MiB | 339.05 MiB | pass |
| c100/warm | tree_pss_end | 86.29 MiB | 327.94 MiB | 87.37 MiB | 333.79 MiB | pass |
| c100/warm | ext_peak_rss_end | 14.61 MiB | 224.95 MiB | 15.01 MiB | 226.31 MiB | pass |
| c100/warm | ext_anon_end | 7.48 MiB | 164.78 MiB | 7.88 MiB | 166.06 MiB | pass |
| c100/warm | ext_rss_ready | 9.30 MiB | 160.91 MiB | 9.49 MiB | 161.77 MiB | pass |
| c100/warm | ext_pss_ready | 9.29 MiB | 157.03 MiB | 9.49 MiB | 157.92 MiB | pass |
| c100/warm | heap_used_gc_end | 0.62 MiB | 36.08 MiB | 0.64 MiB | 36.10 MiB | pass |
| c100/warm | heap_used_asis_end | 1.83 MiB | 58.25 MiB | 2.80 MiB | 64.78 MiB | pass |
| c100/warm | heap_inuse_gc_end | 1.77 MiB | 103.59 MiB | 1.85 MiB | 104.34 MiB | pass |
| c100/warm | heap_inuse_asis_end | 3.17 MiB | 110.09 MiB | 4.04 MiB | 113.35 MiB | pass |
| c100/warm | sys_asis_end | 17.76 MiB | 205.31 MiB | 17.94 MiB | 217.72 MiB | pass |
| c100/warm | sys_gc_end | 17.76 MiB | 200.62 MiB | 17.94 MiB | 212.76 MiB | pass |
| c100/warm | tool_ms:notes.append | 5.5 | 7.5 | 7.2 | 11.1 | pass |
| c100/warm | tool_ms:notes.list | 9.1 | 20.6 | 11.9 | 29.1 | pass |
| c100/warm | tool_ms:notes.read | 5.0 | 7.8 | 6.9 | 11.7 | pass |
| c100/warm | tool_ms:notes.search | 9.9 | 29.2 | 13.0 | 39.1 | pass |
| c100/warm | tool_ms:notes.write | 5.7 | 9.5 | 7.0 | 13.5 | pass |
| e/warm | startup_ms | 116.1 | 870.8 | 135.6 | 884.3 | pass |
| e/warm | to_first_tool_ms | 19.6 | 36.0 | 23.0 | 38.8 | pass |
| e/warm | turn_ms | 13147.6 | 13632.6 | 13603.5 | 14213.6 | pass |
| e/warm | cpu_total_ms_end | 16640 | 17250 | 17360 | 18320 | pass |
| e/warm | cpu_total_workload_ms | 16570.0 | 16110.0 | 17260.0 | 17120.0 | FAIL (median) |
| e/warm | cpu_ext_workload_ms | 8680.0 | 9360.0 | 9100.0 | 10060.0 | pass |
| e/warm | ext_cpu_ms_end | 8690 | 10310 | 9110 | 11060 | pass |
| e/warm | ext_rss_end | 19.70 MiB | 454.79 MiB | 24.93 MiB | 495.83 MiB | pass |
| e/warm | ext_pss_end | 19.69 MiB | 450.86 MiB | 24.93 MiB | 491.91 MiB | pass |
| e/warm | tree_rss_end | 132.29 MiB | 590.32 MiB | 139.77 MiB | 628.05 MiB | pass |
| e/warm | tree_pss_end | 130.89 MiB | 585.01 MiB | 138.40 MiB | 622.76 MiB | pass |
| e/warm | ext_peak_rss_end | 20.24 MiB | 516.20 MiB | 24.93 MiB | 539.96 MiB | pass |
| e/warm | ext_anon_end | 12.56 MiB | 394.29 MiB | 17.80 MiB | 435.34 MiB | pass |
| e/warm | ext_rss_ready | 9.33 MiB | 161.27 MiB | 9.47 MiB | 161.82 MiB | pass |
| e/warm | ext_pss_ready | 9.32 MiB | 157.43 MiB | 9.46 MiB | 157.90 MiB | pass |
| e/warm | heap_used_gc_end | 0.71 MiB | 35.94 MiB | 0.72 MiB | 36.86 MiB | pass |
| e/warm | heap_used_asis_end | 2.02 MiB | 47.60 MiB | 2.42 MiB | 110.53 MiB | pass |
| e/warm | heap_inuse_gc_end | 2.46 MiB | 169.09 MiB | 2.77 MiB | 169.59 MiB | pass |
| e/warm | heap_inuse_asis_end | 3.91 MiB | 174.74 MiB | 4.72 MiB | 229.86 MiB | pass |
| e/warm | sys_asis_end | 25.94 MiB | 423.32 MiB | 26.19 MiB | 522.68 MiB | pass |
| e/warm | sys_gc_end | 25.94 MiB | 419.15 MiB | 26.19 MiB | 484.84 MiB | pass |
| e/warm | tool_ms:get_context_remaining | 5.1 | 5.9 | 6.8 | 9.1 | pass |
| e/warm | tool_ms:notes.append | 6.6 | 8.7 | 8.1 | 11.8 | pass |
| e/warm | tool_ms:notes.write | 7.0 | 11.2 | 8.6 | 14.6 | pass |
| e/warm | tool_ms:read | 7.7 | 11.3 | 9.2 | 13.8 | pass |

Median misses (go-sf vs ts-sf): 1.

## Ratchet: go-sf <= go (information: new pig and SDK against old (go-sf <= go)), every metric

| Workload | Metric | go-sf median | go median | go-sf p90 | go p90 | verdict |
|---|---|---:|---:|---:|---:|---|
| a/cold | startup_ms | 739.3 | 736.5 | 794.9 | 762.1 | worse |
| a/cold | cpu_total_ms_end | 1130 | 1140 | 1200 | 1190 | not worse, p90 worse |
| a/cold | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | not worse |
| a/cold | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | not worse |
| a/cold | ext_cpu_ms_end | 10 | 10 | 10 | 10 | not worse |
| a/cold | ext_rss_end | 9.37 MiB | 9.34 MiB | 9.58 MiB | 9.59 MiB | worse |
| a/cold | ext_pss_end | 9.36 MiB | 9.33 MiB | 9.58 MiB | 9.59 MiB | worse |
| a/cold | tree_rss_end | 83.06 MiB | 82.64 MiB | 86.72 MiB | 85.45 MiB | worse |
| a/cold | tree_pss_end | 81.67 MiB | 81.28 MiB | 85.31 MiB | 84.00 MiB | worse |
| a/cold | ext_peak_rss_end | 9.37 MiB | 9.34 MiB | 9.58 MiB | 9.59 MiB | worse |
| a/cold | ext_anon_end | 2.45 MiB | 2.42 MiB | 2.71 MiB | 2.61 MiB | worse |
| a/cold | ext_rss_ready | 9.37 MiB | 9.34 MiB | 9.58 MiB | 9.59 MiB | worse |
| a/cold | ext_pss_ready | 9.36 MiB | 9.33 MiB | 9.58 MiB | 9.59 MiB | worse |
| a/cold | heap_used_gc_end | 0.51 MiB | 0.51 MiB | 0.51 MiB | 0.51 MiB | worse |
| a/cold | heap_used_asis_end | 0.52 MiB | 0.52 MiB | 0.52 MiB | 0.52 MiB | worse |
| a/cold | heap_inuse_gc_end | 1.51 MiB | 1.55 MiB | 1.60 MiB | 1.59 MiB | not worse, p90 worse |
| a/cold | heap_inuse_asis_end | 1.52 MiB | 1.55 MiB | 1.61 MiB | 1.60 MiB | not worse, p90 worse |
| a/cold | sys_asis_end | 13.40 MiB | 13.41 MiB | 13.66 MiB | 13.60 MiB | not worse, p90 worse |
| a/cold | sys_gc_end | 13.42 MiB | 13.42 MiB | 13.68 MiB | 13.68 MiB | not worse |
| a/warm | startup_ms | 116.0 | 117.1 | 127.4 | 126.5 | not worse, p90 worse |
| a/warm | cpu_total_ms_end | 90 | 90 | 100 | 100 | not worse |
| a/warm | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | not worse |
| a/warm | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | not worse |
| a/warm | ext_cpu_ms_end | 10 | 10 | 10 | 10 | not worse |
| a/warm | ext_rss_end | 9.36 MiB | 9.40 MiB | 9.58 MiB | 9.59 MiB | not worse |
| a/warm | ext_pss_end | 9.36 MiB | 9.40 MiB | 9.58 MiB | 9.59 MiB | not worse |
| a/warm | tree_rss_end | 70.17 MiB | 70.00 MiB | 71.06 MiB | 70.67 MiB | worse |
| a/warm | tree_pss_end | 68.82 MiB | 68.55 MiB | 69.63 MiB | 69.34 MiB | worse |
| a/warm | ext_peak_rss_end | 9.36 MiB | 9.40 MiB | 9.58 MiB | 9.59 MiB | not worse |
| a/warm | ext_anon_end | 2.50 MiB | 2.48 MiB | 2.71 MiB | 2.67 MiB | worse |
| a/warm | ext_rss_ready | 9.36 MiB | 9.40 MiB | 9.58 MiB | 9.59 MiB | not worse |
| a/warm | ext_pss_ready | 9.36 MiB | 9.40 MiB | 9.58 MiB | 9.59 MiB | not worse |
| a/warm | heap_used_gc_end | 0.51 MiB | 0.51 MiB | 0.51 MiB | 0.51 MiB | worse |
| a/warm | heap_used_asis_end | 0.52 MiB | 0.52 MiB | 0.52 MiB | 0.52 MiB | worse |
| a/warm | heap_inuse_gc_end | 1.53 MiB | 1.48 MiB | 1.57 MiB | 1.60 MiB | worse |
| a/warm | heap_inuse_asis_end | 1.54 MiB | 1.48 MiB | 1.58 MiB | 1.61 MiB | worse |
| a/warm | sys_asis_end | 13.41 MiB | 13.40 MiB | 13.66 MiB | 13.66 MiB | worse |
| a/warm | sys_gc_end | 13.43 MiB | 13.42 MiB | 13.68 MiB | 13.68 MiB | worse |
| b/cold | startup_ms | 774.1 | 753.7 | 787.1 | 790.7 | worse |
| b/cold | first_turn_ms | 40.7 | 47.0 | 46.3 | 50.6 | not worse |
| b/cold | to_first_tool_ms | 19.8 | 20.8 | 22.3 | 25.0 | not worse |
| b/cold | turn_ms | 40.7 | 47.0 | 46.3 | 50.6 | not worse |
| b/cold | cpu_total_ms_end | 1230 | 1210 | 1250 | 1250 | worse |
| b/cold | cpu_total_workload_ms | 40.0 | 50.0 | 60.0 | 60.0 | not worse |
| b/cold | cpu_ext_workload_ms | 20.0 | 20.0 | 20.0 | 30.0 | not worse |
| b/cold | ext_cpu_ms_end | 20 | 30 | 30 | 40 | not worse |
| b/cold | ext_rss_end | 10.71 MiB | 11.06 MiB | 10.89 MiB | 12.18 MiB | not worse |
| b/cold | ext_pss_end | 10.71 MiB | 11.05 MiB | 10.89 MiB | 12.18 MiB | not worse |
| b/cold | tree_rss_end | 84.25 MiB | 85.83 MiB | 87.10 MiB | 87.69 MiB | not worse |
| b/cold | tree_pss_end | 82.92 MiB | 84.43 MiB | 85.69 MiB | 86.28 MiB | not worse |
| b/cold | ext_peak_rss_end | 10.71 MiB | 11.06 MiB | 10.89 MiB | 12.18 MiB | not worse |
| b/cold | ext_anon_end | 3.78 MiB | 4.08 MiB | 3.96 MiB | 5.13 MiB | not worse |
| b/cold | ext_rss_ready | 9.27 MiB | 9.37 MiB | 9.45 MiB | 9.57 MiB | not worse |
| b/cold | ext_pss_ready | 9.26 MiB | 9.36 MiB | 9.45 MiB | 9.57 MiB | not worse |
| b/cold | heap_used_gc_end | 0.54 MiB | 0.54 MiB | 0.54 MiB | 0.54 MiB | worse |
| b/cold | heap_used_asis_end | 1.60 MiB | 1.78 MiB | 1.66 MiB | 1.83 MiB | not worse |
| b/cold | heap_inuse_gc_end | 1.63 MiB | 1.61 MiB | 1.76 MiB | 1.71 MiB | worse |
| b/cold | heap_inuse_asis_end | 3.09 MiB | 3.20 MiB | 3.25 MiB | 3.35 MiB | not worse |
| b/cold | sys_asis_end | 13.42 MiB | 13.41 MiB | 17.41 MiB | 17.40 MiB | worse |
| b/cold | sys_gc_end | 13.44 MiB | 13.68 MiB | 17.44 MiB | 17.44 MiB | not worse |
| b/cold | tool_ms:get_context_remaining | 4.6 | 4.9 | 6.0 | 6.5 | not worse |
| b/warm | startup_ms | 116.7 | 116.2 | 135.8 | 126.3 | worse |
| b/warm | first_turn_ms | 40.7 | 42.8 | 43.6 | 48.8 | not worse |
| b/warm | to_first_tool_ms | 19.0 | 19.9 | 21.2 | 21.9 | not worse |
| b/warm | turn_ms | 40.7 | 42.8 | 43.6 | 48.8 | not worse |
| b/warm | cpu_total_ms_end | 140 | 140 | 150 | 160 | not worse |
| b/warm | cpu_total_workload_ms | 50.0 | 40.0 | 60.0 | 60.0 | worse |
| b/warm | cpu_ext_workload_ms | 20.0 | 20.0 | 30.0 | 30.0 | not worse |
| b/warm | ext_cpu_ms_end | 20 | 30 | 30 | 40 | not worse |
| b/warm | ext_rss_end | 10.71 MiB | 10.99 MiB | 11.95 MiB | 12.04 MiB | not worse |
| b/warm | ext_pss_end | 10.70 MiB | 10.98 MiB | 11.95 MiB | 12.04 MiB | not worse |
| b/warm | tree_rss_end | 73.59 MiB | 73.94 MiB | 74.59 MiB | 74.91 MiB | not worse |
| b/warm | tree_pss_end | 72.22 MiB | 72.57 MiB | 73.16 MiB | 73.55 MiB | not worse |
| b/warm | ext_peak_rss_end | 10.71 MiB | 10.99 MiB | 11.95 MiB | 12.04 MiB | not worse |
| b/warm | ext_anon_end | 3.78 MiB | 4.01 MiB | 4.96 MiB | 5.00 MiB | not worse |
| b/warm | ext_rss_ready | 9.24 MiB | 9.32 MiB | 9.55 MiB | 9.55 MiB | not worse |
| b/warm | ext_pss_ready | 9.24 MiB | 9.31 MiB | 9.55 MiB | 9.55 MiB | not worse |
| b/warm | heap_used_gc_end | 0.54 MiB | 0.54 MiB | 0.55 MiB | 0.54 MiB | worse |
| b/warm | heap_used_asis_end | 1.60 MiB | 1.79 MiB | 1.69 MiB | 1.83 MiB | not worse |
| b/warm | heap_inuse_gc_end | 1.63 MiB | 1.66 MiB | 1.72 MiB | 1.75 MiB | not worse |
| b/warm | heap_inuse_asis_end | 3.05 MiB | 3.14 MiB | 3.30 MiB | 3.41 MiB | not worse |
| b/warm | sys_asis_end | 13.41 MiB | 13.66 MiB | 13.92 MiB | 13.91 MiB | not worse, p90 worse |
| b/warm | sys_gc_end | 13.68 MiB | 13.68 MiB | 13.94 MiB | 17.61 MiB | not worse |
| b/warm | tool_ms:get_context_remaining | 4.6 | 4.8 | 6.5 | 6.6 | not worse |
| c1/warm | startup_ms | 116.0 | 116.5 | 125.0 | 126.8 | not worse |
| c1/warm | to_first_tool_ms | 18.2 | 20.5 | 20.4 | 22.3 | not worse |
| c1/warm | turn_ms | 653.9 | 844.8 | 726.3 | 867.8 | not worse |
| c1/warm | cpu_total_ms_end | 850 | 1110 | 990 | 1130 | not worse |
| c1/warm | cpu_total_workload_ms | 770.0 | 1010.0 | 900.0 | 1050.0 | not worse |
| c1/warm | cpu_ext_workload_ms | 360.0 | 530.0 | 420.0 | 550.0 | not worse |
| c1/warm | ext_cpu_ms_end | 370 | 540 | 430 | 560 | not worse |
| c1/warm | ext_rss_end | 14.24 MiB | 14.42 MiB | 14.39 MiB | 14.64 MiB | not worse |
| c1/warm | ext_pss_end | 14.23 MiB | 14.42 MiB | 14.39 MiB | 14.64 MiB | not worse |
| c1/warm | tree_rss_end | 86.04 MiB | 85.35 MiB | 87.70 MiB | 86.51 MiB | worse |
| c1/warm | tree_pss_end | 84.61 MiB | 83.92 MiB | 86.30 MiB | 85.08 MiB | worse |
| c1/warm | ext_peak_rss_end | 14.24 MiB | 14.42 MiB | 14.39 MiB | 14.64 MiB | not worse |
| c1/warm | ext_anon_end | 7.13 MiB | 7.31 MiB | 7.27 MiB | 7.47 MiB | not worse |
| c1/warm | ext_rss_ready | 9.36 MiB | 9.36 MiB | 9.48 MiB | 9.57 MiB | not worse |
| c1/warm | ext_pss_ready | 9.35 MiB | 9.36 MiB | 9.48 MiB | 9.57 MiB | not worse |
| c1/warm | heap_used_gc_end | 0.57 MiB | 0.57 MiB | 0.58 MiB | 0.57 MiB | worse |
| c1/warm | heap_used_asis_end | 2.24 MiB | 1.97 MiB | 2.53 MiB | 2.77 MiB | worse |
| c1/warm | heap_inuse_gc_end | 1.70 MiB | 1.71 MiB | 1.79 MiB | 1.83 MiB | not worse |
| c1/warm | heap_inuse_asis_end | 3.51 MiB | 3.17 MiB | 3.92 MiB | 3.88 MiB | worse |
| c1/warm | sys_asis_end | 17.69 MiB | 17.69 MiB | 17.94 MiB | 17.94 MiB | not worse |
| c1/warm | sys_gc_end | 17.69 MiB | 17.69 MiB | 17.94 MiB | 17.94 MiB | not worse |
| c1/warm | tool_ms:notes.append | 5.0 | 5.8 | 6.9 | 7.6 | not worse |
| c1/warm | tool_ms:notes.list | 4.9 | 5.8 | 7.4 | 7.5 | not worse |
| c1/warm | tool_ms:notes.read | 4.2 | 5.4 | 6.4 | 7.5 | not worse |
| c1/warm | tool_ms:notes.search | 4.7 | 5.5 | 7.0 | 7.5 | not worse |
| c1/warm | tool_ms:notes.write | 5.1 | 6.1 | 6.6 | 7.8 | not worse |
| c100/warm | startup_ms | 116.5 | 116.6 | 129.1 | 123.3 | not worse, p90 worse |
| c100/warm | to_first_tool_ms | 18.6 | 20.3 | 20.9 | 23.1 | not worse |
| c100/warm | turn_ms | 801.7 | 1054.1 | 859.9 | 1132.0 | not worse |
| c100/warm | cpu_total_ms_end | 1080 | 1350 | 1170 | 1470 | not worse |
| c100/warm | cpu_total_workload_ms | 1000.0 | 1260.0 | 1070.0 | 1390.0 | not worse |
| c100/warm | cpu_ext_workload_ms | 510.0 | 730.0 | 560.0 | 800.0 | not worse |
| c100/warm | ext_cpu_ms_end | 510 | 740 | 570 | 800 | not worse |
| c100/warm | ext_rss_end | 14.61 MiB | 14.88 MiB | 15.01 MiB | 15.16 MiB | not worse |
| c100/warm | ext_pss_end | 14.61 MiB | 14.88 MiB | 15.01 MiB | 15.15 MiB | not worse |
| c100/warm | tree_rss_end | 87.70 MiB | 87.14 MiB | 88.73 MiB | 88.12 MiB | worse |
| c100/warm | tree_pss_end | 86.29 MiB | 85.80 MiB | 87.37 MiB | 86.77 MiB | worse |
| c100/warm | ext_peak_rss_end | 14.61 MiB | 14.91 MiB | 15.01 MiB | 15.16 MiB | not worse |
| c100/warm | ext_anon_end | 7.48 MiB | 7.70 MiB | 7.88 MiB | 7.98 MiB | not worse |
| c100/warm | ext_rss_ready | 9.30 MiB | 9.36 MiB | 9.49 MiB | 9.55 MiB | not worse |
| c100/warm | ext_pss_ready | 9.29 MiB | 9.36 MiB | 9.49 MiB | 9.54 MiB | not worse |
| c100/warm | heap_used_gc_end | 0.62 MiB | 0.62 MiB | 0.64 MiB | 0.64 MiB | not worse |
| c100/warm | heap_used_asis_end | 1.83 MiB | 2.37 MiB | 2.80 MiB | 2.41 MiB | not worse, p90 worse |
| c100/warm | heap_inuse_gc_end | 1.77 MiB | 1.77 MiB | 1.85 MiB | 1.88 MiB | not worse |
| c100/warm | heap_inuse_asis_end | 3.17 MiB | 3.45 MiB | 4.04 MiB | 3.54 MiB | not worse, p90 worse |
| c100/warm | sys_asis_end | 17.76 MiB | 17.69 MiB | 17.94 MiB | 17.94 MiB | worse |
| c100/warm | sys_gc_end | 17.76 MiB | 17.69 MiB | 17.94 MiB | 17.94 MiB | worse |
| c100/warm | tool_ms:notes.append | 5.5 | 5.9 | 7.2 | 7.6 | not worse |
| c100/warm | tool_ms:notes.list | 9.1 | 10.4 | 11.9 | 13.2 | not worse |
| c100/warm | tool_ms:notes.read | 5.0 | 5.9 | 6.9 | 7.7 | not worse |
| c100/warm | tool_ms:notes.search | 9.9 | 11.0 | 13.0 | 13.4 | not worse |
| c100/warm | tool_ms:notes.write | 5.7 | 6.3 | 7.0 | 8.2 | not worse |
| e/warm | startup_ms | 116.1 | 113.5 | 135.6 | 128.2 | worse |
| e/warm | to_first_tool_ms | 19.6 | 21.4 | 23.0 | 23.9 | not worse |
| e/warm | turn_ms | 13147.6 | 21590.2 | 13603.5 | 21962.6 | not worse |
| e/warm | cpu_total_ms_end | 16640 | 26170 | 17360 | 26920 | not worse |
| e/warm | cpu_total_workload_ms | 16570.0 | 26080.0 | 17260.0 | 26820.0 | not worse |
| e/warm | cpu_ext_workload_ms | 8680.0 | 15530.0 | 9100.0 | 15900.0 | not worse |
| e/warm | ext_cpu_ms_end | 8690 | 15530 | 9110 | 15910 | not worse |
| e/warm | ext_rss_end | 19.70 MiB | 15.56 MiB | 24.93 MiB | 22.21 MiB | worse |
| e/warm | ext_pss_end | 19.69 MiB | 15.56 MiB | 24.93 MiB | 22.21 MiB | worse |
| e/warm | tree_rss_end | 132.29 MiB | 130.51 MiB | 139.77 MiB | 135.41 MiB | worse |
| e/warm | tree_pss_end | 130.89 MiB | 129.15 MiB | 138.40 MiB | 134.00 MiB | worse |
| e/warm | ext_peak_rss_end | 20.24 MiB | 21.43 MiB | 24.93 MiB | 24.06 MiB | not worse, p90 worse |
| e/warm | ext_anon_end | 12.56 MiB | 8.39 MiB | 17.80 MiB | 15.04 MiB | worse |
| e/warm | ext_rss_ready | 9.33 MiB | 9.37 MiB | 9.47 MiB | 9.55 MiB | not worse |
| e/warm | ext_pss_ready | 9.32 MiB | 9.36 MiB | 9.46 MiB | 9.55 MiB | not worse |
| e/warm | heap_used_gc_end | 0.71 MiB | 0.71 MiB | 0.72 MiB | 0.72 MiB | worse |
| e/warm | heap_used_asis_end | 2.02 MiB | 2.35 MiB | 2.42 MiB | 2.77 MiB | not worse |
| e/warm | heap_inuse_gc_end | 2.46 MiB | 2.41 MiB | 2.77 MiB | 2.68 MiB | worse |
| e/warm | heap_inuse_asis_end | 3.91 MiB | 4.06 MiB | 4.72 MiB | 4.44 MiB | not worse, p90 worse |
| e/warm | sys_asis_end | 25.94 MiB | 25.94 MiB | 26.19 MiB | 26.26 MiB | not worse |
| e/warm | sys_gc_end | 25.94 MiB | 25.94 MiB | 26.19 MiB | 26.26 MiB | not worse |
| e/warm | tool_ms:get_context_remaining | 5.1 | 5.5 | 6.8 | 6.9 | not worse |
| e/warm | tool_ms:notes.append | 6.6 | 6.9 | 8.1 | 8.4 | not worse |
| e/warm | tool_ms:notes.write | 7.0 | 7.3 | 8.6 | 9.0 | not worse |
| e/warm | tool_ms:read | 7.7 | 11.1 | 9.2 | 13.1 | not worse |

Metrics where go-sf is worse than go: 45.


## Relative spread (p90-p50)/p50 above 5% (go and ts sides, ratchet metrics)

- a/cold ts startup_ms: 8.4% (n=25, median 2293.2)
- a/cold go-sf startup_ms: 7.5% (n=25, median 739.3)
- a/cold ts-sf startup_ms: 8.7% (n=25, median 2324.2)
- a/cold ts cpu_total_ms_end: 8.8% (n=25, median 3740)
- a/cold go-sf cpu_total_ms_end: 6.2% (n=25, median 1130)
- a/cold ts-sf cpu_total_ms_end: 9.4% (n=25, median 3810)
- a/cold ts ext_cpu_ms_end: 10.7% (n=25, median 3090)
- a/cold ts-sf ext_cpu_ms_end: 10.8% (n=25, median 3150)
- a/cold go ext_anon_end: 7.9% (n=25, median 2.42 MiB)
- a/cold go-sf ext_anon_end: 10.7% (n=25, median 2.45 MiB)
- a/cold go-sf heap_inuse_gc_end: 6.2% (n=25, median 1.51 MiB)
- a/cold go-sf heap_inuse_asis_end: 6.2% (n=25, median 1.52 MiB)
- a/warm go startup_ms: 8.0% (n=25, median 117.1)
- a/warm go-sf startup_ms: 9.8% (n=25, median 116.0)
- a/warm go cpu_total_ms_end: 11.1% (n=25, median 90)
- a/warm go-sf cpu_total_ms_end: 11.1% (n=25, median 90)
- a/warm ts-sf ext_cpu_ms_end: 5.2% (n=25, median 970)
- a/warm go ext_anon_end: 7.5% (n=25, median 2.48 MiB)
- a/warm go-sf ext_anon_end: 8.8% (n=25, median 2.50 MiB)
- a/warm go heap_inuse_gc_end: 8.5% (n=25, median 1.48 MiB)
- a/warm ts heap_inuse_gc_end: 45.5% (n=25, median 69.84 MiB)
- a/warm ts-sf heap_inuse_gc_end: 44.7% (n=25, median 69.84 MiB)
- a/warm go heap_inuse_asis_end: 8.4% (n=25, median 1.48 MiB)
- a/warm ts heap_inuse_asis_end: 44.9% (n=25, median 70.09 MiB)
- a/warm ts-sf heap_inuse_asis_end: 44.2% (n=25, median 70.09 MiB)
- b/cold ts startup_ms: 7.0% (n=25, median 2382.2)
- b/cold ts-sf startup_ms: 8.1% (n=25, median 2351.1)
- b/cold go first_turn_ms: 7.7% (n=25, median 47.0)
- b/cold ts first_turn_ms: 8.8% (n=25, median 65.8)
- b/cold go-sf first_turn_ms: 13.6% (n=25, median 40.7)
- b/cold ts-sf first_turn_ms: 14.5% (n=25, median 62.2)
- b/cold go to_first_tool_ms: 20.0% (n=25, median 20.8)
- b/cold ts to_first_tool_ms: 8.4% (n=25, median 34.4)
- b/cold go-sf to_first_tool_ms: 12.6% (n=25, median 19.8)
- b/cold ts-sf to_first_tool_ms: 9.5% (n=25, median 33.5)
- b/cold go turn_ms: 7.7% (n=25, median 47.0)
- b/cold ts turn_ms: 8.8% (n=25, median 65.8)
- b/cold go-sf turn_ms: 13.6% (n=25, median 40.7)
- b/cold ts-sf turn_ms: 14.5% (n=25, median 62.2)
- b/cold ts cpu_total_ms_end: 7.3% (n=25, median 3980)
- b/cold ts-sf cpu_total_ms_end: 8.7% (n=25, median 3920)
- b/cold go cpu_total_workload_ms: 20.0% (n=25, median 50.0)
- b/cold ts cpu_total_workload_ms: 22.2% (n=25, median 90.0)
- b/cold go-sf cpu_total_workload_ms: 50.0% (n=25, median 40.0)
- b/cold ts-sf cpu_total_workload_ms: 22.2% (n=25, median 90.0)
- b/cold go cpu_ext_workload_ms: 50.0% (n=25, median 20.0)
- b/cold ts cpu_ext_workload_ms: 33.3% (n=25, median 60.0)
- b/cold ts-sf cpu_ext_workload_ms: 14.3% (n=25, median 70.0)
- b/cold go ext_cpu_ms_end: 33.3% (n=25, median 30)
- b/cold ts ext_cpu_ms_end: 8.5% (n=25, median 3280)
- b/cold go-sf ext_cpu_ms_end: 50.0% (n=25, median 20)
- b/cold ts-sf ext_cpu_ms_end: 9.6% (n=25, median 3240)
- b/cold go ext_rss_end: 10.2% (n=25, median 11.06 MiB)
- b/cold go ext_pss_end: 10.2% (n=25, median 11.05 MiB)
- b/cold go ext_peak_rss_end: 10.2% (n=25, median 11.06 MiB)
- b/cold go ext_anon_end: 25.9% (n=25, median 4.08 MiB)
- b/cold go heap_inuse_gc_end: 6.3% (n=25, median 1.61 MiB)
- b/cold go-sf heap_inuse_gc_end: 7.7% (n=25, median 1.63 MiB)
- b/cold go-sf heap_inuse_asis_end: 5.3% (n=25, median 3.09 MiB)
- b/cold go sys_asis_end: 29.8% (n=25, median 13.41 MiB)
- b/cold go-sf sys_asis_end: 29.7% (n=25, median 13.42 MiB)
- b/cold go sys_gc_end: 27.5% (n=25, median 13.68 MiB)
- b/cold go-sf sys_gc_end: 29.8% (n=25, median 13.44 MiB)
- b/cold go tool_ms:get_context_remaining: 31.9% (n=25, median 4.9)
- b/cold ts tool_ms:get_context_remaining: 30.7% (n=25, median 10.9)
- b/cold go-sf tool_ms:get_context_remaining: 29.5% (n=25, median 4.6)
- b/cold ts-sf tool_ms:get_context_remaining: 34.0% (n=25, median 10.4)
- b/warm go startup_ms: 8.7% (n=25, median 116.2)
- b/warm go-sf startup_ms: 16.3% (n=25, median 116.7)
- b/warm go first_turn_ms: 14.0% (n=25, median 42.8)
- b/warm ts first_turn_ms: 7.1% (n=25, median 69.4)
- b/warm go-sf first_turn_ms: 7.1% (n=25, median 40.7)
- b/warm ts-sf first_turn_ms: 6.0% (n=25, median 68.5)
- b/warm go to_first_tool_ms: 10.1% (n=25, median 19.9)
- b/warm ts to_first_tool_ms: 5.7% (n=25, median 35.3)
- b/warm go-sf to_first_tool_ms: 11.7% (n=25, median 19.0)
- b/warm ts-sf to_first_tool_ms: 8.7% (n=25, median 34.9)
- b/warm go turn_ms: 14.0% (n=25, median 42.8)
- b/warm ts turn_ms: 7.1% (n=25, median 69.4)
- b/warm go-sf turn_ms: 7.1% (n=25, median 40.7)
- b/warm ts-sf turn_ms: 6.0% (n=25, median 68.5)
- b/warm go cpu_total_ms_end: 14.3% (n=25, median 140)
- b/warm go-sf cpu_total_ms_end: 7.1% (n=25, median 140)
- b/warm go cpu_total_workload_ms: 50.0% (n=25, median 40.0)
- b/warm ts cpu_total_workload_ms: 33.3% (n=25, median 90.0)
- b/warm go-sf cpu_total_workload_ms: 20.0% (n=25, median 50.0)
- b/warm ts-sf cpu_total_workload_ms: 10.0% (n=25, median 100.0)
- b/warm go cpu_ext_workload_ms: 50.0% (n=25, median 20.0)
- b/warm ts cpu_ext_workload_ms: 33.3% (n=25, median 60.0)
- b/warm go-sf cpu_ext_workload_ms: 50.0% (n=25, median 20.0)
- b/warm ts-sf cpu_ext_workload_ms: 14.3% (n=25, median 70.0)
- b/warm go ext_cpu_ms_end: 33.3% (n=25, median 30)
- b/warm go-sf ext_cpu_ms_end: 50.0% (n=25, median 20)
- b/warm go ext_rss_end: 9.6% (n=25, median 10.99 MiB)
- b/warm go-sf ext_rss_end: 11.6% (n=25, median 10.71 MiB)
- b/warm go ext_pss_end: 9.6% (n=25, median 10.98 MiB)
- b/warm go-sf ext_pss_end: 11.6% (n=25, median 10.70 MiB)
- b/warm go ext_peak_rss_end: 9.6% (n=25, median 10.99 MiB)
- b/warm go-sf ext_peak_rss_end: 11.6% (n=25, median 10.71 MiB)
- b/warm go ext_anon_end: 24.9% (n=25, median 4.01 MiB)
- b/warm go-sf ext_anon_end: 31.3% (n=25, median 3.78 MiB)
- b/warm go-sf heap_used_asis_end: 5.6% (n=25, median 1.60 MiB)
- b/warm go heap_inuse_gc_end: 5.2% (n=25, median 1.66 MiB)
- b/warm ts heap_inuse_gc_end: 44.4% (n=25, median 70.34 MiB)
- b/warm go-sf heap_inuse_gc_end: 5.3% (n=25, median 1.63 MiB)
- b/warm ts-sf heap_inuse_gc_end: 44.9% (n=25, median 70.09 MiB)
- b/warm go heap_inuse_asis_end: 8.5% (n=25, median 3.14 MiB)
- b/warm ts heap_inuse_asis_end: 44.4% (n=25, median 70.34 MiB)
- b/warm go-sf heap_inuse_asis_end: 7.9% (n=25, median 3.05 MiB)
- b/warm ts-sf heap_inuse_asis_end: 44.9% (n=25, median 70.09 MiB)
- b/warm go sys_gc_end: 28.7% (n=25, median 13.68 MiB)
- b/warm go tool_ms:get_context_remaining: 37.5% (n=25, median 4.8)
- b/warm ts tool_ms:get_context_remaining: 11.6% (n=25, median 12.9)
- b/warm go-sf tool_ms:get_context_remaining: 41.6% (n=25, median 4.6)
- b/warm ts-sf tool_ms:get_context_remaining: 22.5% (n=25, median 12.1)
- c1/warm go startup_ms: 8.9% (n=25, median 116.5)
- c1/warm ts startup_ms: 10.4% (n=25, median 801.3)
- c1/warm go-sf startup_ms: 7.8% (n=25, median 116.0)
- c1/warm ts-sf startup_ms: 7.1% (n=25, median 823.0)
- c1/warm go to_first_tool_ms: 8.7% (n=25, median 20.5)
- c1/warm ts to_first_tool_ms: 8.8% (n=25, median 34.3)
- c1/warm go-sf to_first_tool_ms: 11.6% (n=25, median 18.2)
- c1/warm ts-sf to_first_tool_ms: 9.5% (n=25, median 34.5)
- c1/warm ts turn_ms: 7.8% (n=25, median 785.9)
- c1/warm go-sf turn_ms: 11.1% (n=25, median 653.9)
- c1/warm ts-sf turn_ms: 13.4% (n=25, median 721.7)
- c1/warm ts cpu_total_ms_end: 9.7% (n=25, median 2070)
- c1/warm go-sf cpu_total_ms_end: 16.5% (n=25, median 850)
- c1/warm ts-sf cpu_total_ms_end: 12.3% (n=25, median 2030)
- c1/warm ts cpu_total_workload_ms: 5.8% (n=25, median 1030.0)
- c1/warm go-sf cpu_total_workload_ms: 16.9% (n=25, median 770.0)
- c1/warm ts-sf cpu_total_workload_ms: 12.6% (n=25, median 950.0)
- c1/warm ts cpu_ext_workload_ms: 8.1% (n=25, median 620.0)
- c1/warm go-sf cpu_ext_workload_ms: 16.7% (n=25, median 360.0)
- c1/warm ts-sf cpu_ext_workload_ms: 13.6% (n=25, median 590.0)
- c1/warm ts ext_cpu_ms_end: 10.6% (n=25, median 1510)
- c1/warm go-sf ext_cpu_ms_end: 16.2% (n=25, median 370)
- c1/warm ts-sf ext_cpu_ms_end: 12.0% (n=25, median 1500)
- c1/warm go heap_used_asis_end: 40.3% (n=25, median 1.97 MiB)
- c1/warm go-sf heap_used_asis_end: 13.4% (n=25, median 2.24 MiB)
- c1/warm go heap_inuse_gc_end: 6.8% (n=25, median 1.71 MiB)
- c1/warm go-sf heap_inuse_gc_end: 5.5% (n=25, median 1.70 MiB)
- c1/warm go heap_inuse_asis_end: 22.4% (n=25, median 3.17 MiB)
- c1/warm go-sf heap_inuse_asis_end: 11.8% (n=25, median 3.51 MiB)
- c1/warm ts sys_asis_end: 5.5% (n=25, median 188.50 MiB)
- c1/warm ts-sf sys_asis_end: 5.2% (n=25, median 187.50 MiB)
- c1/warm ts sys_gc_end: 7.3% (n=25, median 185.31 MiB)
- c1/warm ts-sf sys_gc_end: 7.0% (n=25, median 184.30 MiB)
- c1/warm go tool_ms:notes.append: 31.4% (n=150, median 5.8)
- c1/warm ts tool_ms:notes.append: 44.4% (n=150, median 7.8)
- c1/warm go-sf tool_ms:notes.append: 39.5% (n=150, median 5.0)
- c1/warm ts-sf tool_ms:notes.append: 52.6% (n=150, median 7.4)
- c1/warm go tool_ms:notes.list: 30.4% (n=150, median 5.8)
- c1/warm ts tool_ms:notes.list: 64.5% (n=150, median 9.0)
- c1/warm go-sf tool_ms:notes.list: 50.6% (n=150, median 4.9)
- c1/warm ts-sf tool_ms:notes.list: 70.4% (n=150, median 8.8)
- c1/warm go tool_ms:notes.read: 39.9% (n=150, median 5.4)
- c1/warm ts tool_ms:notes.read: 53.6% (n=150, median 7.9)
- c1/warm go-sf tool_ms:notes.read: 54.3% (n=150, median 4.2)
- c1/warm ts-sf tool_ms:notes.read: 49.0% (n=150, median 8.0)
- c1/warm go tool_ms:notes.search: 36.3% (n=150, median 5.5)
- c1/warm ts tool_ms:notes.search: 39.3% (n=150, median 8.9)
- c1/warm go-sf tool_ms:notes.search: 50.1% (n=150, median 4.7)
- c1/warm ts-sf tool_ms:notes.search: 39.9% (n=150, median 8.7)
- c1/warm go tool_ms:notes.write: 28.0% (n=150, median 6.1)
- c1/warm ts tool_ms:notes.write: 38.2% (n=150, median 10.1)
- c1/warm go-sf tool_ms:notes.write: 29.9% (n=150, median 5.1)
- c1/warm ts-sf tool_ms:notes.write: 43.5% (n=150, median 9.7)
- c100/warm go startup_ms: 5.7% (n=25, median 116.6)
- c100/warm ts startup_ms: 6.6% (n=25, median 831.3)
- c100/warm go-sf startup_ms: 10.8% (n=25, median 116.5)
- c100/warm ts-sf startup_ms: 6.4% (n=25, median 825.3)
- c100/warm go to_first_tool_ms: 13.8% (n=25, median 20.3)
- c100/warm ts to_first_tool_ms: 11.0% (n=25, median 34.3)
- c100/warm go-sf to_first_tool_ms: 12.4% (n=25, median 18.6)
- c100/warm ts-sf to_first_tool_ms: 5.9% (n=25, median 34.5)
- c100/warm go turn_ms: 7.4% (n=25, median 1054.1)
- c100/warm ts turn_ms: 13.9% (n=25, median 1058.9)
- c100/warm go-sf turn_ms: 7.3% (n=25, median 801.7)
- c100/warm ts-sf turn_ms: 13.7% (n=25, median 994.3)
- c100/warm go cpu_total_ms_end: 8.9% (n=25, median 1350)
- c100/warm ts cpu_total_ms_end: 12.0% (n=25, median 2420)
- c100/warm go-sf cpu_total_ms_end: 8.3% (n=25, median 1080)
- c100/warm ts-sf cpu_total_ms_end: 7.0% (n=25, median 2440)
- c100/warm go cpu_total_workload_ms: 10.3% (n=25, median 1260.0)
- c100/warm ts cpu_total_workload_ms: 16.4% (n=25, median 1280.0)
- c100/warm go-sf cpu_total_workload_ms: 7.0% (n=25, median 1000.0)
- c100/warm ts-sf cpu_total_workload_ms: 14.5% (n=25, median 1240.0)
- c100/warm go cpu_ext_workload_ms: 9.6% (n=25, median 730.0)
- c100/warm ts cpu_ext_workload_ms: 17.6% (n=25, median 850.0)
- c100/warm go-sf cpu_ext_workload_ms: 9.8% (n=25, median 510.0)
- c100/warm ts-sf cpu_ext_workload_ms: 18.1% (n=25, median 830.0)
- c100/warm go ext_cpu_ms_end: 8.1% (n=25, median 740)
- c100/warm ts ext_cpu_ms_end: 12.7% (n=25, median 1810)
- c100/warm go-sf ext_cpu_ms_end: 11.8% (n=25, median 510)
- c100/warm ts-sf ext_cpu_ms_end: 8.2% (n=25, median 1830)
- c100/warm go-sf ext_anon_end: 5.3% (n=25, median 7.48 MiB)
- c100/warm ts heap_used_asis_end: 11.2% (n=25, median 58.24 MiB)
- c100/warm go-sf heap_used_asis_end: 53.4% (n=25, median 1.83 MiB)
- c100/warm ts-sf heap_used_asis_end: 11.2% (n=25, median 58.25 MiB)
- c100/warm go heap_inuse_gc_end: 5.7% (n=25, median 1.77 MiB)
- c100/warm go-sf heap_inuse_asis_end: 27.3% (n=25, median 3.17 MiB)
- c100/warm ts sys_asis_end: 5.7% (n=25, median 206.28 MiB)
- c100/warm ts-sf sys_asis_end: 6.0% (n=25, median 205.31 MiB)
- c100/warm ts sys_gc_end: 5.5% (n=25, median 201.14 MiB)
- c100/warm ts-sf sys_gc_end: 6.0% (n=25, median 200.62 MiB)
- c100/warm go tool_ms:notes.append: 29.0% (n=150, median 5.9)
- c100/warm ts tool_ms:notes.append: 45.2% (n=150, median 7.7)
- c100/warm go-sf tool_ms:notes.append: 30.2% (n=150, median 5.5)
- c100/warm ts-sf tool_ms:notes.append: 48.4% (n=150, median 7.5)
- c100/warm go tool_ms:notes.list: 27.5% (n=150, median 10.4)
- c100/warm ts tool_ms:notes.list: 45.5% (n=150, median 21.0)
- c100/warm go-sf tool_ms:notes.list: 30.6% (n=150, median 9.1)
- c100/warm ts-sf tool_ms:notes.list: 41.4% (n=150, median 20.6)
- c100/warm go tool_ms:notes.read: 30.3% (n=150, median 5.9)
- c100/warm ts tool_ms:notes.read: 42.1% (n=150, median 7.9)
- c100/warm go-sf tool_ms:notes.read: 36.7% (n=150, median 5.0)
- c100/warm ts-sf tool_ms:notes.read: 50.0% (n=150, median 7.8)
- c100/warm go tool_ms:notes.search: 21.5% (n=150, median 11.0)
- c100/warm ts tool_ms:notes.search: 33.6% (n=150, median 31.0)
- c100/warm go-sf tool_ms:notes.search: 31.3% (n=150, median 9.9)
- c100/warm ts-sf tool_ms:notes.search: 33.6% (n=150, median 29.2)
- c100/warm go tool_ms:notes.write: 28.6% (n=150, median 6.3)
- c100/warm ts tool_ms:notes.write: 44.5% (n=150, median 9.7)
- c100/warm go-sf tool_ms:notes.write: 23.0% (n=150, median 5.7)
- c100/warm ts-sf tool_ms:notes.write: 43.0% (n=150, median 9.5)
- e/warm go startup_ms: 13.0% (n=25, median 113.5)
- e/warm ts startup_ms: 9.1% (n=25, median 813.1)
- e/warm go-sf startup_ms: 16.9% (n=25, median 116.1)
- e/warm go to_first_tool_ms: 12.0% (n=25, median 21.4)
- e/warm ts to_first_tool_ms: 10.8% (n=25, median 36.6)
- e/warm go-sf to_first_tool_ms: 17.4% (n=25, median 19.6)
- e/warm ts-sf to_first_tool_ms: 7.8% (n=25, median 36.0)
- e/warm ts-sf cpu_total_ms_end: 6.2% (n=25, median 17250)
- e/warm ts-sf cpu_total_workload_ms: 6.3% (n=25, median 16110.0)
- e/warm ts-sf cpu_ext_workload_ms: 7.5% (n=25, median 9360.0)
- e/warm ts-sf ext_cpu_ms_end: 7.3% (n=25, median 10310)
- e/warm go ext_rss_end: 42.7% (n=25, median 15.56 MiB)
- e/warm ts ext_rss_end: 6.1% (n=25, median 484.84 MiB)
- e/warm go-sf ext_rss_end: 26.6% (n=25, median 19.70 MiB)
- e/warm ts-sf ext_rss_end: 9.0% (n=25, median 454.79 MiB)
- e/warm go ext_pss_end: 42.7% (n=25, median 15.56 MiB)
- e/warm ts ext_pss_end: 6.2% (n=25, median 480.96 MiB)
- e/warm go-sf ext_pss_end: 26.6% (n=25, median 19.69 MiB)
- e/warm ts-sf ext_pss_end: 9.1% (n=25, median 450.86 MiB)
- e/warm go-sf tree_rss_end: 5.7% (n=25, median 132.29 MiB)
- e/warm ts-sf tree_rss_end: 6.4% (n=25, median 590.32 MiB)
- e/warm go-sf tree_pss_end: 5.7% (n=25, median 130.89 MiB)
- e/warm ts-sf tree_pss_end: 6.5% (n=25, median 585.01 MiB)
- e/warm go ext_peak_rss_end: 12.2% (n=25, median 21.43 MiB)
- e/warm go-sf ext_peak_rss_end: 23.2% (n=25, median 20.24 MiB)
- e/warm go ext_anon_end: 79.2% (n=25, median 8.39 MiB)
- e/warm ts ext_anon_end: 7.0% (n=25, median 424.49 MiB)
- e/warm go-sf ext_anon_end: 41.7% (n=25, median 12.56 MiB)
- e/warm ts-sf ext_anon_end: 10.4% (n=25, median 394.29 MiB)
- e/warm go heap_used_asis_end: 17.8% (n=25, median 2.35 MiB)
- e/warm ts heap_used_asis_end: 42.4% (n=25, median 55.71 MiB)
- e/warm go-sf heap_used_asis_end: 20.1% (n=25, median 2.02 MiB)
- e/warm ts-sf heap_used_asis_end: 132.2% (n=25, median 47.60 MiB)
- e/warm go heap_inuse_gc_end: 11.0% (n=25, median 2.41 MiB)
- e/warm go-sf heap_inuse_gc_end: 12.7% (n=25, median 2.46 MiB)
- e/warm go heap_inuse_asis_end: 9.2% (n=25, median 4.06 MiB)
- e/warm ts heap_inuse_asis_end: 12.2% (n=25, median 177.58 MiB)
- e/warm go-sf heap_inuse_asis_end: 20.8% (n=25, median 3.91 MiB)
- e/warm ts-sf heap_inuse_asis_end: 31.5% (n=25, median 174.74 MiB)
- e/warm ts-sf sys_asis_end: 23.5% (n=25, median 423.32 MiB)
- e/warm ts-sf sys_gc_end: 15.7% (n=25, median 419.15 MiB)
- e/warm go tool_ms:get_context_remaining: 27.0% (n=750, median 5.5)
- e/warm ts tool_ms:get_context_remaining: 24.5% (n=750, median 7.5)
- e/warm go-sf tool_ms:get_context_remaining: 32.9% (n=750, median 5.1)
- e/warm ts-sf tool_ms:get_context_remaining: 55.7% (n=750, median 5.9)
- e/warm go tool_ms:notes.append: 22.7% (n=1500, median 6.9)
- e/warm ts tool_ms:notes.append: 25.5% (n=1500, median 9.8)
- e/warm go-sf tool_ms:notes.append: 23.5% (n=1500, median 6.6)
- e/warm ts-sf tool_ms:notes.append: 35.7% (n=1500, median 8.7)
- e/warm go tool_ms:notes.write: 22.3% (n=1500, median 7.3)
- e/warm ts tool_ms:notes.write: 26.8% (n=1500, median 12.1)
- e/warm go-sf tool_ms:notes.write: 22.7% (n=1500, median 7.0)
- e/warm ts-sf tool_ms:notes.write: 30.7% (n=1500, median 11.2)
- e/warm go tool_ms:read: 18.2% (n=750, median 11.1)
- e/warm ts tool_ms:read: 15.2% (n=750, median 12.4)
- e/warm go-sf tool_ms:read: 19.2% (n=750, median 7.7)
- e/warm ts-sf tool_ms:read: 22.0% (n=750, median 11.3)

