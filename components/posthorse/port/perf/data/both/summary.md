# Posthorse perf summary

Medians and p90 over fresh-process samples; n in parentheses. Metric definitions: README.md. Every metric: summary-full.md.

### Workload a, cold

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 634.7 / 681.2 (25) | 2193.8 / 2272.0 (25) | 1542.5 / 1629.0 (25) |
| cpu_total_ms_end | 930 / 1020 (25) | 3530 / 3670 (25) | 2580 / 2730 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 10 / 10 (25) | 2950 / 3080 (25) | 2580 / 2730 (25) |
| ext_rss_end | 9.20 MiB / 9.46 MiB (25) | 311.45 MiB / 313.56 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| ext_pss_end | 9.20 MiB / 9.46 MiB (25) | 307.33 MiB / 309.50 MiB (25) | 268.05 MiB / 271.14 MiB (25) |
| tree_rss_end | 79.00 MiB / 85.05 MiB (25) | 467.43 MiB / 471.72 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| tree_pss_end | 77.54 MiB / 83.62 MiB (25) | 461.96 MiB / 466.17 MiB (25) | 268.05 MiB / 271.14 MiB (25) |
| ext_peak_rss_end | 9.20 MiB / 9.46 MiB (25) | 311.45 MiB / 313.56 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| ext_anon_end | 2.39 MiB / 2.55 MiB (25) | 255.66 MiB / 257.89 MiB (25) | 217.60 MiB / 220.72 MiB (25) |
| ext_rss_ready | 9.20 MiB / 9.46 MiB (25) | 311.45 MiB / 313.56 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| ext_pss_ready | 9.20 MiB / 9.46 MiB (25) | 307.33 MiB / 309.50 MiB (25) | 268.05 MiB / 271.14 MiB (25) |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB (25) | 47.18 MiB / 47.18 MiB (25) | 33.89 MiB / 33.89 MiB (25) |
| heap_used_asis_end | 0.51 MiB / 0.52 MiB (25) | 48.61 MiB / 48.62 MiB (25) | 35.34 MiB / 35.35 MiB (25) |
| heap_inuse_gc_end | 1.55 MiB / 1.60 MiB (25) | 181.25 MiB / 181.50 MiB (25) | 168.07 MiB / 168.57 MiB (25) |
| heap_inuse_asis_end | 1.55 MiB / 1.61 MiB (25) | 181.25 MiB / 181.50 MiB (25) | 168.82 MiB / 169.32 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.41 MiB (25) | 302.14 MiB / 304.50 MiB (25) | 264.38 MiB / 268.88 MiB (25) |
| sys_gc_end | 13.42 MiB / 13.44 MiB (25) | 302.14 MiB / 304.50 MiB (25) | 264.38 MiB / 268.88 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| mallocs_workload | 188 / 190 (25) | - | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 1 / 1 (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) | - | - |

### Workload a, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 113.0 / 119.8 (25) | 768.1 / 788.7 (25) | 408.7 / 427.3 (25) |
| cpu_total_ms_end | 80 / 90 (25) | 1040 / 1070 (25) | 600 / 640 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 10 / 10 (25) | 870 / 900 (25) | 600 / 640 (25) |
| ext_rss_end | 9.21 MiB / 9.58 MiB (25) | 160.74 MiB / 162.50 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| ext_pss_end | 9.21 MiB / 9.58 MiB (25) | 156.74 MiB / 158.50 MiB (25) | 109.17 MiB / 112.08 MiB (25) |
| tree_rss_end | 69.78 MiB / 70.37 MiB (25) | 245.24 MiB / 246.99 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| tree_pss_end | 68.27 MiB / 68.84 MiB (25) | 239.74 MiB / 241.51 MiB (25) | 109.17 MiB / 112.08 MiB (25) |
| ext_peak_rss_end | 9.21 MiB / 9.58 MiB (25) | 160.74 MiB / 162.50 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| ext_anon_end | 2.40 MiB / 2.61 MiB (25) | 105.25 MiB / 106.96 MiB (25) | 59.10 MiB / 61.99 MiB (25) |
| ext_rss_ready | 9.21 MiB / 9.58 MiB (25) | 160.74 MiB / 162.50 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| ext_pss_ready | 9.21 MiB / 9.58 MiB (25) | 156.74 MiB / 158.50 MiB (25) | 109.17 MiB / 112.08 MiB (25) |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB (25) | 34.94 MiB / 34.94 MiB (25) | 21.75 MiB / 21.76 MiB (25) |
| heap_used_asis_end | 0.51 MiB / 0.52 MiB (25) | 35.19 MiB / 35.20 MiB (25) | 21.98 MiB / 21.99 MiB (25) |
| heap_inuse_gc_end | 1.52 MiB / 1.61 MiB (25) | 69.84 MiB / 101.59 MiB (25) | 40.66 MiB / 40.91 MiB (25) |
| heap_inuse_asis_end | 1.53 MiB / 1.62 MiB (25) | 70.09 MiB / 101.59 MiB (25) | 40.91 MiB / 42.41 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.66 MiB (25) | 150.00 MiB / 155.94 MiB (25) | 105.11 MiB / 107.78 MiB (25) |
| sys_gc_end | 13.43 MiB / 13.67 MiB (25) | 150.00 MiB / 155.94 MiB (25) | 105.11 MiB / 107.78 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| mallocs_workload | 188 / 189 (25) | - | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 1 / 1 (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) | - | - |

### Workload b, cold

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 626.9 / 730.5 (25) | 2176.7 / 2267.8 (25) | 1562.9 / 1833.0 (25) |
| first_turn_ms | 34.8 / 44.8 (25) | 53.8 / 60.1 (25) | 100.5 / 111.7 (25) |
| to_first_tool_ms | 17.6 / 21.7 (25) | 28.1 / 34.1 (25) | 85.9 / 96.7 (25) |
| turn_ms | 34.8 / 44.8 (25) | 53.8 / 60.1 (25) | 100.5 / 111.7 (25) |
| cpu_total_ms_end | 950 / 1100 (25) | 3540 / 3740 (25) | 2660 / 2900 (25) |
| cpu_total_workload_ms | 40.0 / 50.0 (25) | 80.0 / 90.0 (25) | 120.0 / 130.0 (25) |
| cpu_ext_workload_ms | 20.0 / 20.0 (25) | 50.0 / 70.0 (25) | 120.0 / 130.0 (25) |
| ext_cpu_ms_end | 20 / 30 (25) | 2950 / 3160 (25) | 2660 / 2900 (25) |
| ext_rss_end | 11.02 MiB / 12.11 MiB (25) | 317.11 MiB / 320.46 MiB (25) | 278.19 MiB / 281.89 MiB (25) |
| ext_pss_end | 11.02 MiB / 12.10 MiB (25) | 313.13 MiB / 316.39 MiB (25) | 275.98 MiB / 279.73 MiB (25) |
| tree_rss_end | 84.84 MiB / 90.24 MiB (25) | 480.90 MiB / 485.38 MiB (25) | 278.19 MiB / 281.89 MiB (25) |
| tree_pss_end | 83.33 MiB / 88.71 MiB (25) | 475.41 MiB / 479.85 MiB (25) | 275.98 MiB / 279.73 MiB (25) |
| ext_peak_rss_end | 11.02 MiB / 12.11 MiB (25) | 317.11 MiB / 320.46 MiB (25) | 278.19 MiB / 281.89 MiB (25) |
| ext_anon_end | 4.09 MiB / 5.07 MiB (25) | 256.87 MiB / 260.14 MiB (25) | 219.77 MiB / 223.52 MiB (25) |
| ext_rss_ready | 9.27 MiB / 9.54 MiB (25) | 311.98 MiB / 315.38 MiB (25) | 271.20 MiB / 274.60 MiB (25) |
| ext_pss_ready | 9.26 MiB / 9.54 MiB (25) | 307.92 MiB / 311.30 MiB (25) | 269.12 MiB / 272.58 MiB (25) |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB (25) | 47.42 MiB / 47.43 MiB (25) | 36.77 MiB / 36.78 MiB (25) |
| heap_used_asis_end | 1.79 MiB / 1.83 MiB (25) | 49.99 MiB / 50.00 MiB (25) | 40.83 MiB / 40.83 MiB (25) |
| heap_inuse_gc_end | 1.63 MiB / 1.71 MiB (25) | 181.25 MiB / 181.75 MiB (25) | 169.25 MiB / 169.75 MiB (25) |
| heap_inuse_asis_end | 3.11 MiB / 3.29 MiB (25) | 181.25 MiB / 181.75 MiB (25) | 169.50 MiB / 170.25 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.66 MiB (25) | 306.83 MiB / 309.00 MiB (25) | 270.72 MiB / 273.38 MiB (25) |
| sys_gc_end | 13.67 MiB / 13.69 MiB (25) | 306.83 MiB / 309.00 MiB (25) | 271.29 MiB / 273.38 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) |
| mallocs_workload | 14324 / 14366 (25) | - | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 1 / 1 (25) |
| tool_ms:get_context_remaining | 2.7 / 5.5 (25) | 9.0 / 10.9 (25) | 4.5 / 4.7 (25) |
| total_alloc_workload | 1.31 MiB / 1.35 MiB (25) | - | - |

### Workload b, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 114.6 / 127.2 (25) | 812.5 / 878.0 (25) | 430.2 / 477.7 (25) |
| first_turn_ms | 41.0 / 47.3 (25) | 62.3 / 75.1 (25) | 91.9 / 96.9 (25) |
| to_first_tool_ms | 19.3 / 21.8 (25) | 33.1 / 38.0 (25) | 75.3 / 78.6 (25) |
| turn_ms | 41.0 / 47.3 (25) | 62.3 / 75.1 (25) | 91.9 / 96.9 (25) |
| cpu_total_ms_end | 130 / 150 (25) | 1180 / 1300 (25) | 750 / 840 (25) |
| cpu_total_workload_ms | 50.0 / 70.0 (25) | 80.0 / 120.0 (25) | 110.0 / 120.0 (25) |
| cpu_ext_workload_ms | 20.0 / 30.0 (25) | 50.0 / 80.0 (25) | 110.0 / 120.0 (25) |
| ext_cpu_ms_end | 30 / 30 (25) | 980 / 1070 (25) | 750 / 840 (25) |
| ext_rss_end | 10.91 MiB / 12.12 MiB (25) | 167.31 MiB / 169.08 MiB (25) | 122.96 MiB / 123.71 MiB (25) |
| ext_pss_end | 10.91 MiB / 12.11 MiB (25) | 163.33 MiB / 165.14 MiB (25) | 120.76 MiB / 121.56 MiB (25) |
| tree_rss_end | 73.92 MiB / 75.27 MiB (25) | 257.10 MiB / 258.81 MiB (25) | 122.96 MiB / 123.71 MiB (25) |
| tree_pss_end | 72.40 MiB / 73.75 MiB (25) | 251.59 MiB / 253.28 MiB (25) | 120.76 MiB / 121.56 MiB (25) |
| ext_peak_rss_end | 10.91 MiB / 12.12 MiB (25) | 167.31 MiB / 169.08 MiB (25) | 122.96 MiB / 123.71 MiB (25) |
| ext_anon_end | 4.06 MiB / 5.08 MiB (25) | 107.14 MiB / 108.89 MiB (25) | 64.80 MiB / 65.46 MiB (25) |
| ext_rss_ready | 9.25 MiB / 9.56 MiB (25) | 160.88 MiB / 161.69 MiB (25) | 111.57 MiB / 114.25 MiB (25) |
| ext_pss_ready | 9.24 MiB / 9.55 MiB (25) | 156.83 MiB / 157.72 MiB (25) | 109.49 MiB / 112.14 MiB (25) |
| heap_used_gc_end | 0.54 MiB / 0.55 MiB (25) | 35.17 MiB / 35.17 MiB (25) | 24.52 MiB / 24.52 MiB (25) |
| heap_used_asis_end | 1.79 MiB / 1.84 MiB (25) | 36.54 MiB / 36.55 MiB (25) | 27.33 MiB / 27.33 MiB (25) |
| heap_inuse_gc_end | 1.64 MiB / 1.71 MiB (25) | 70.09 MiB / 101.84 MiB (25) | 43.10 MiB / 59.60 MiB (25) |
| heap_inuse_asis_end | 3.12 MiB / 3.48 MiB (25) | 69.84 MiB / 101.59 MiB (25) | 42.85 MiB / 44.10 MiB (25) |
| sys_asis_end | 13.66 MiB / 17.36 MiB (25) | 156.00 MiB / 161.62 MiB (25) | 113.16 MiB / 114.68 MiB (25) |
| sys_gc_end | 13.67 MiB / 17.42 MiB (25) | 156.00 MiB / 161.62 MiB (25) | 113.18 MiB / 114.83 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) |
| mallocs_workload | 14339 / 14381 (25) | - | - |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 1 / 1 (25) |
| tool_ms:get_context_remaining | 4.3 / 5.7 (25) | 10.4 / 13.8 (25) | 4.6 / 5.1 (25) |
| total_alloc_workload | 1.32 MiB / 1.36 MiB (25) | - | - |

### Workload c1, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 116.3 / 132.3 (60) | 845.4 / 880.3 (60) | 454.0 / 476.8 (60) |
| to_first_tool_ms | 19.8 / 22.6 (60) | 33.8 / 37.3 (60) | 76.2 / 80.5 (60) |
| turn_ms | 825.8 / 861.1 (60) | 682.0 / 825.2 (60) | 369.1 / 386.1 (60) |
| cpu_total_ms_end | 1080 / 1130 (60) | 2015 / 2250 (60) | 1200 / 1280 (60) |
| cpu_total_workload_ms | 990.0 / 1040.0 (60) | 850.0 / 1060.0 (60) | 550.0 / 570.0 (60) |
| cpu_ext_workload_ms | 520.0 / 540.0 (60) | 515.0 / 650.0 (60) | 550.0 / 570.0 (60) |
| ext_cpu_ms_end | 525 / 550 (60) | 1490 / 1650 (60) | 1200 / 1280 (60) |
| ext_rss_end | 14.51 MiB / 14.73 MiB (60) | 208.80 MiB / 209.95 MiB (60) | 155.10 MiB / 157.31 MiB (60) |
| ext_pss_end | 14.51 MiB / 14.73 MiB (60) | 204.87 MiB / 206.02 MiB (60) | 152.97 MiB / 155.25 MiB (60) |
| tree_rss_end | 85.43 MiB / 86.29 MiB (60) | 318.20 MiB / 323.78 MiB (60) | 155.10 MiB / 157.31 MiB (60) |
| tree_pss_end | 84.02 MiB / 84.92 MiB (60) | 312.90 MiB / 318.61 MiB (60) | 152.97 MiB / 155.25 MiB (60) |
| ext_peak_rss_end | 14.51 MiB / 14.73 MiB (60) | 208.80 MiB / 209.95 MiB (60) | 155.10 MiB / 157.31 MiB (60) |
| ext_anon_end | 7.39 MiB / 7.59 MiB (60) | 148.63 MiB / 149.70 MiB (60) | 96.22 MiB / 98.46 MiB (60) |
| ext_rss_ready | 9.28 MiB / 9.50 MiB (60) | 160.89 MiB / 161.93 MiB (60) | 111.80 MiB / 114.20 MiB (60) |
| ext_pss_ready | 9.27 MiB / 9.50 MiB (60) | 156.94 MiB / 157.93 MiB (60) | 109.84 MiB / 112.30 MiB (60) |
| heap_used_gc_end | 0.57 MiB / 0.57 MiB (60) | 35.89 MiB / 35.89 MiB (60) | 26.95 MiB / 26.96 MiB (60) |
| heap_used_asis_end | 1.77 MiB / 2.76 MiB (60) | 60.36 MiB / 60.66 MiB (60) | 32.39 MiB / 32.46 MiB (60) |
| heap_inuse_gc_end | 1.71 MiB / 1.80 MiB (60) | 102.59 MiB / 102.84 MiB (60) | 63.10 MiB / 63.60 MiB (60) |
| heap_inuse_asis_end | 3.04 MiB / 3.94 MiB (60) | 107.27 MiB / 107.54 MiB (60) | 63.35 MiB / 64.10 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) | 188.18 MiB / 199.11 MiB (60) | 146.03 MiB / 148.50 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) | 184.81 MiB / 199.01 MiB (60) | 146.03 MiB / 148.65 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) | 31 / 31 (60) | 31 / 31 (60) |
| mallocs_workload | 835399 / 837101 (60) | - | - |
| num_gc_workload | 32 / 34 (60) | 4 / 4 (60) | 6 / 6 (60) |
| tool_ms:notes.append | 5.2 / 7.2 (360) | 7.1 / 10.7 (360) | 1.5 / 2.1 (360) |
| tool_ms:notes.list | 5.5 / 7.5 (360) | 8.0 / 14.1 (360) | 3.7 / 11.1 (360) |
| tool_ms:notes.read | 4.9 / 7.3 (360) | 7.2 / 10.4 (360) | 1.8 / 5.2 (360) |
| tool_ms:notes.search | 5.1 / 7.2 (360) | 7.7 / 11.4 (360) | 4.4 / 5.6 (360) |
| tool_ms:notes.write | 5.9 / 7.4 (360) | 9.1 / 13.0 (360) | 3.3 / 4.7 (360) |
| total_alloc_workload | 57.41 MiB / 57.78 MiB (60) | - | - |

### Workload c100, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 115.6 / 131.6 (60) | 858.5 / 887.4 (60) | 460.8 / 478.5 (60) |
| to_first_tool_ms | 20.0 / 22.5 (60) | 34.4 / 38.0 (60) | 78.4 / 81.0 (60) |
| turn_ms | 1080.3 / 1114.9 (60) | 1108.2 / 1215.3 (60) | 561.4 / 598.8 (60) |
| cpu_total_ms_end | 1415 / 1470 (60) | 2505 / 2710 (60) | 1415 / 1510 (60) |
| cpu_total_workload_ms | 1320.0 / 1370.0 (60) | 1355.0 / 1500.0 (60) | 775.0 / 800.0 (60) |
| cpu_ext_workload_ms | 755.0 / 790.0 (60) | 895.0 / 1020.0 (60) | 775.0 / 800.0 (60) |
| ext_cpu_ms_end | 760 / 800 (60) | 1845 / 2030 (60) | 1415 / 1510 (60) |
| ext_rss_end | 14.81 MiB / 15.11 MiB (60) | 224.54 MiB / 226.93 MiB (60) | 158.99 MiB / 160.80 MiB (60) |
| ext_pss_end | 14.81 MiB / 15.10 MiB (60) | 220.61 MiB / 222.96 MiB (60) | 156.81 MiB / 158.65 MiB (60) |
| tree_rss_end | 87.72 MiB / 88.68 MiB (60) | 334.80 MiB / 340.58 MiB (60) | 158.99 MiB / 160.80 MiB (60) |
| tree_pss_end | 86.30 MiB / 87.25 MiB (60) | 329.27 MiB / 335.28 MiB (60) | 156.81 MiB / 158.65 MiB (60) |
| ext_peak_rss_end | 14.84 MiB / 15.11 MiB (60) | 224.54 MiB / 226.93 MiB (60) | 158.99 MiB / 160.80 MiB (60) |
| ext_anon_end | 7.65 MiB / 7.95 MiB (60) | 164.36 MiB / 166.60 MiB (60) | 100.14 MiB / 101.88 MiB (60) |
| ext_rss_ready | 9.27 MiB / 9.50 MiB (60) | 160.97 MiB / 161.95 MiB (60) | 111.81 MiB / 114.29 MiB (60) |
| ext_pss_ready | 9.27 MiB / 9.50 MiB (60) | 157.06 MiB / 157.97 MiB (60) | 109.90 MiB / 112.38 MiB (60) |
| heap_used_gc_end | 0.62 MiB / 0.63 MiB (60) | 36.07 MiB / 36.09 MiB (60) | 27.15 MiB / 27.16 MiB (60) |
| heap_used_asis_end | 2.22 MiB / 2.39 MiB (60) | 58.24 MiB / 64.77 MiB (60) | 34.81 MiB / 38.70 MiB (60) |
| heap_inuse_gc_end | 1.80 MiB / 1.88 MiB (60) | 103.34 MiB / 104.84 MiB (60) | 64.85 MiB / 65.35 MiB (60) |
| heap_inuse_asis_end | 3.31 MiB / 3.56 MiB (60) | 110.09 MiB / 113.60 MiB (60) | 65.50 MiB / 65.88 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) | 205.40 MiB / 217.65 MiB (60) | 149.70 MiB / 151.87 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) | 200.39 MiB / 211.52 MiB (60) | 149.70 MiB / 151.87 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) | 31 / 31 (60) | 31 / 31 (60) |
| mallocs_workload | 915169 / 917046 (60) | - | - |
| num_gc_workload | 48 / 49 (60) | 6 / 6 (60) | 8 / 8 (60) |
| tool_ms:notes.append | 6.2 / 7.6 (360) | 8.0 / 11.3 (360) | 1.5 / 2.2 (360) |
| tool_ms:notes.list | 10.5 / 13.0 (360) | 21.4 / 28.8 (360) | 13.4 / 21.0 (360) |
| tool_ms:notes.read | 5.7 / 7.6 (360) | 8.2 / 11.3 (360) | 1.8 / 3.1 (360) |
| tool_ms:notes.search | 11.0 / 13.5 (360) | 31.2 / 41.8 (360) | 23.1 / 31.8 (360) |
| tool_ms:notes.write | 6.3 / 8.0 (360) | 10.0 / 13.9 (360) | 3.2 / 5.0 (360) |
| total_alloc_workload | 84.33 MiB / 84.72 MiB (60) | - | - |

### Workload c1000, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 115.4 / 131.6 (60) | 824.3 / 881.1 (60) | 437.4 / 483.6 (60) |
| to_first_tool_ms | 18.8 / 22.0 (60) | 33.0 / 38.3 (60) | 74.5 / 79.9 (60) |
| turn_ms | 1242.9 / 1335.4 (60) | 1645.1 / 2083.1 (60) | 1142.2 / 1410.8 (60) |
| cpu_total_ms_end | 1705 / 1890 (60) | 3205 / 3920 (60) | 2295 / 2660 (60) |
| cpu_total_workload_ms | 1615.0 / 1800.0 (60) | 2080.0 / 2710.0 (60) | 1600.0 / 1960.0 (60) |
| cpu_ext_workload_ms | 1090.0 / 1210.0 (60) | 1670.0 / 2210.0 (60) | 1600.0 / 1960.0 (60) |
| ext_cpu_ms_end | 1100 / 1210 (60) | 2610 / 3210 (60) | 2295 / 2660 (60) |
| ext_rss_end | 15.22 MiB / 15.41 MiB (60) | 231.21 MiB / 233.26 MiB (60) | 167.76 MiB / 170.29 MiB (60) |
| ext_pss_end | 15.21 MiB / 15.41 MiB (60) | 227.34 MiB / 229.33 MiB (60) | 165.67 MiB / 168.22 MiB (60) |
| tree_rss_end | 88.06 MiB / 89.16 MiB (60) | 338.65 MiB / 346.00 MiB (60) | 167.76 MiB / 170.29 MiB (60) |
| tree_pss_end | 86.57 MiB / 87.77 MiB (60) | 333.39 MiB / 340.69 MiB (60) | 165.67 MiB / 168.22 MiB (60) |
| ext_peak_rss_end | 15.30 MiB / 16.50 MiB (60) | 231.21 MiB / 233.26 MiB (60) | 167.76 MiB / 170.29 MiB (60) |
| ext_anon_end | 8.06 MiB / 8.25 MiB (60) | 170.95 MiB / 173.08 MiB (60) | 108.80 MiB / 111.38 MiB (60) |
| ext_rss_ready | 9.19 MiB / 9.46 MiB (60) | 160.87 MiB / 161.84 MiB (60) | 111.67 MiB / 113.93 MiB (60) |
| ext_pss_ready | 9.19 MiB / 9.46 MiB (60) | 156.84 MiB / 157.81 MiB (60) | 109.76 MiB / 111.98 MiB (60) |
| heap_used_gc_end | 0.71 MiB / 0.71 MiB (60) | 36.16 MiB / 36.17 MiB (60) | 27.20 MiB / 27.21 MiB (60) |
| heap_used_asis_end | 2.46 MiB / 2.46 MiB (60) | 53.80 MiB / 65.20 MiB (60) | 51.45 MiB / 51.88 MiB (60) |
| heap_inuse_gc_end | 1.88 MiB / 1.96 MiB (60) | 104.09 MiB / 109.09 MiB (60) | 95.10 MiB / 95.85 MiB (60) |
| heap_inuse_asis_end | 3.55 MiB / 3.62 MiB (60) | 113.24 MiB / 117.49 MiB (60) | 102.13 MiB / 103.13 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) | 219.95 MiB / 223.60 MiB (60) | 177.49 MiB / 180.39 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) | 217.96 MiB / 220.25 MiB (60) | 177.80 MiB / 180.39 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) | 31 / 31 (60) | 31 / 31 (60) |
| mallocs_workload | 1346482 / 1348176 (60) | - | - |
| num_gc_workload | 79 / 80 (60) | 13 / 15 (60) | 19 / 20 (60) |
| tool_ms:notes.append | 5.9 / 7.7 (360) | 6.8 / 10.6 (360) | 1.4 / 2.1 (360) |
| tool_ms:notes.list | 20.8 / 24.0 (360) | 42.9 / 61.9 (360) | 33.2 / 48.4 (360) |
| tool_ms:notes.read | 5.6 / 7.6 (360) | 7.3 / 11.4 (360) | 1.6 / 2.8 (360) |
| tool_ms:notes.search | 32.0 / 36.5 (360) | 114.1 / 176.4 (360) | 99.0 / 155.4 (360) |
| tool_ms:notes.write | 6.0 / 7.9 (360) | 9.0 / 13.2 (360) | 3.1 / 4.8 (360) |
| total_alloc_workload | 140.61 MiB / 140.92 MiB (60) | - | - |

### Workload d50, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 112.5 / 129.1 (60) | 804.9 / 881.8 (60) | 429.7 / 480.1 (60) |
| to_first_tool_ms | 19.3 / 22.8 (60) | 35.7 / 39.5 (60) | 76.5 / 82.2 (60) |
| turn_ms | 151.2 / 171.3 (60) | 367.8 / 402.9 (60) | 349.5 / 380.1 (60) |
| cpu_total_ms_end | 335 / 380 (60) | 1715 / 1870 (60) | 1250 / 1400 (60) |
| cpu_total_workload_ms | 250.0 / 290.0 (60) | 610.0 / 670.0 (60) | 605.0 / 680.0 (60) |
| cpu_ext_workload_ms | 80.0 / 100.0 (60) | 420.0 / 470.0 (60) | 480.0 / 530.0 (60) |
| ext_cpu_ms_end | 90 / 110 (60) | 1330 / 1470 (60) | 1115 / 1250 (60) |
| ext_rss_end | 14.45 MiB / 14.70 MiB (60) | 208.75 MiB / 210.27 MiB (60) | 168.01 MiB / 169.22 MiB (60) |
| ext_pss_end | 14.45 MiB / 14.69 MiB (60) | 204.81 MiB / 206.42 MiB (60) | 165.87 MiB / 167.09 MiB (60) |
| tree_rss_end | 77.61 MiB / 78.06 MiB (60) | 301.36 MiB / 303.11 MiB (60) | 168.01 MiB / 169.22 MiB (60) |
| tree_pss_end | 76.16 MiB / 76.71 MiB (60) | 296.03 MiB / 297.63 MiB (60) | 165.87 MiB / 167.09 MiB (60) |
| ext_peak_rss_end | 14.45 MiB / 14.71 MiB (60) | 208.75 MiB / 210.27 MiB (60) | 168.27 MiB / 169.22 MiB (60) |
| ext_anon_end | 7.23 MiB / 7.47 MiB (60) | 148.40 MiB / 150.04 MiB (60) | 108.89 MiB / 110.18 MiB (60) |
| ext_rss_ready | 9.37 MiB / 9.58 MiB (60) | 161.11 MiB / 161.89 MiB (60) | 111.81 MiB / 114.59 MiB (60) |
| ext_pss_ready | 9.37 MiB / 9.58 MiB (60) | 157.12 MiB / 157.90 MiB (60) | 109.85 MiB / 112.67 MiB (60) |
| heap_used_gc_end | 0.55 MiB / 0.56 MiB (60) | 36.26 MiB / 36.27 MiB (60) | 25.70 MiB / 25.71 MiB (60) |
| heap_used_asis_end | 1.17 MiB / 3.09 MiB (60) | 60.11 MiB / 60.56 MiB (60) | 33.36 MiB / 44.55 MiB (60) |
| heap_inuse_gc_end | 1.65 MiB / 1.73 MiB (60) | 104.09 MiB / 104.59 MiB (60) | 61.60 MiB / 63.10 MiB (60) |
| heap_inuse_asis_end | 2.48 MiB / 4.37 MiB (60) | 105.36 MiB / 105.86 MiB (60) | 64.82 MiB / 66.82 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) | 208.38 MiB / 210.00 MiB (60) | 162.56 MiB / 167.00 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) | 208.38 MiB / 210.00 MiB (60) | 162.56 MiB / 167.00 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 4 / 4 (60) | 4 / 4 (60) | 4 / 4 (60) |
| mallocs_workload | 46455.5 / 47108 (60) | - | - |
| num_gc_workload | 8 / 8 (60) | 5 / 5 (60) | 9 / 11 (60) |
| tool_ms:history.read | 20.0 / 23.5 (60) | 34.1 / 39.0 (60) | 25.7 / 32.2 (60) |
| tool_ms:history.search:common | 19.3 / 23.3 (60) | 30.5 / 36.7 (60) | 23.4 / 29.0 (60) |
| tool_ms:history.search:rare | 49.7 / 57.2 (60) | 231.9 / 245.0 (60) | 188.6 / 212.9 (60) |
| total_alloc_workload | 13.00 MiB / 13.08 MiB (60) | - | - |

### Workload d200, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 114.6 / 126.1 (60) | 785.5 / 879.4 (60) | 435.2 / 483.0 (60) |
| to_first_tool_ms | 19.4 / 22.0 (60) | 32.5 / 38.7 (60) | 73.2 / 81.8 (60) |
| turn_ms | 221.2 / 246.6 (60) | 465.5 / 529.7 (60) | 437.2 / 505.5 (60) |
| cpu_total_ms_end | 655 / 740 (60) | 2000 / 2310 (60) | 1575 / 1820 (60) |
| cpu_total_workload_ms | 570.0 / 650.0 (60) | 940.0 / 1100.0 (60) | 945.0 / 1110.0 (60) |
| cpu_ext_workload_ms | 100.0 / 120.0 (60) | 455.0 / 540.0 (60) | 520.0 / 610.0 (60) |
| ext_cpu_ms_end | 100 / 120 (60) | 1355 / 1560 (60) | 1160 / 1310 (60) |
| ext_rss_end | 14.51 MiB / 14.87 MiB (60) | 212.39 MiB / 214.01 MiB (60) | 168.72 MiB / 170.13 MiB (60) |
| ext_pss_end | 14.51 MiB / 14.86 MiB (60) | 208.41 MiB / 210.02 MiB (60) | 166.63 MiB / 167.96 MiB (60) |
| tree_rss_end | 77.59 MiB / 78.48 MiB (60) | 305.02 MiB / 306.48 MiB (60) | 168.72 MiB / 170.13 MiB (60) |
| tree_pss_end | 76.18 MiB / 77.03 MiB (60) | 299.67 MiB / 301.15 MiB (60) | 166.63 MiB / 167.96 MiB (60) |
| ext_peak_rss_end | 14.52 MiB / 14.96 MiB (60) | 212.39 MiB / 214.01 MiB (60) | 168.72 MiB / 170.13 MiB (60) |
| ext_anon_end | 7.29 MiB / 7.64 MiB (60) | 152.07 MiB / 153.75 MiB (60) | 109.69 MiB / 111.04 MiB (60) |
| ext_rss_ready | 9.26 MiB / 9.49 MiB (60) | 161.16 MiB / 162.13 MiB (60) | 111.94 MiB / 114.50 MiB (60) |
| ext_pss_ready | 9.25 MiB / 9.48 MiB (60) | 157.16 MiB / 158.18 MiB (60) | 109.95 MiB / 112.60 MiB (60) |
| heap_used_gc_end | 0.56 MiB / 0.57 MiB (60) | 36.31 MiB / 36.32 MiB (60) | 25.76 MiB / 25.77 MiB (60) |
| heap_used_asis_end | 1.59 MiB / 3.11 MiB (60) | 52.71 MiB / 53.06 MiB (60) | 37.06 MiB / 38.80 MiB (60) |
| heap_inuse_gc_end | 1.66 MiB / 1.73 MiB (60) | 104.34 MiB / 104.84 MiB (60) | 61.60 MiB / 63.85 MiB (60) |
| heap_inuse_asis_end | 2.93 MiB / 4.27 MiB (60) | 105.59 MiB / 106.09 MiB (60) | 65.29 MiB / 67.77 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) | 207.85 MiB / 210.00 MiB (60) | 163.76 MiB / 167.13 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) | 207.85 MiB / 210.00 MiB (60) | 163.69 MiB / 167.13 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 4 / 4 (60) | 4 / 4 (60) | 4 / 4 (60) |
| mallocs_workload | 48217.5 / 48758 (60) | - | - |
| num_gc_workload | 9 / 9 (60) | 7 / 7 (60) | 12 / 14 (60) |
| tool_ms:history.read | 42.7 / 47.9 (60) | 54.5 / 66.0 (60) | 45.9 / 57.3 (60) |
| tool_ms:history.search:common | 31.4 / 36.9 (60) | 41.3 / 49.9 (60) | 33.5 / 42.7 (60) |
| tool_ms:history.search:rare | 83.6 / 93.1 (60) | 293.5 / 329.0 (60) | 258.8 / 296.9 (60) |
| total_alloc_workload | 15.42 MiB / 15.50 MiB (60) | - | - |

### Workload e, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| startup_ms | 115.1 / 128.4 (25) | 796.4 / 882.3 (25) | 435.1 / 482.1 (25) |
| to_first_tool_ms | 19.8 / 23.0 (25) | 33.9 / 41.3 (25) | 75.7 / 82.9 (25) |
| turn_ms | 26358.7 / 26858.5 (25) | 15996.7 / 16444.8 (25) | 2545.2 / 2668.4 (25) |
| cpu_total_ms_end | 31510 / 32330 (25) | 19490 / 19910 (25) | 4200 / 4450 (25) |
| cpu_total_workload_ms | 31430.0 / 32250.0 (25) | 18370.0 / 18830.0 (25) | 3570.0 / 3730.0 (25) |
| cpu_ext_workload_ms | 18130.0 / 18630.0 (25) | 9300.0 / 9650.0 (25) | 3570.0 / 3730.0 (25) |
| ext_cpu_ms_end | 18140 / 18640 (25) | 10230 / 10550 (25) | 4200 / 4450 (25) |
| ext_rss_end | 15.40 MiB / 22.08 MiB (25) | 459.14 MiB / 487.18 MiB (25) | 310.57 MiB / 316.40 MiB (25) |
| ext_pss_end | 15.39 MiB / 22.07 MiB (25) | 455.12 MiB / 483.25 MiB (25) | 308.32 MiB / 314.31 MiB (25) |
| tree_rss_end | 128.54 MiB / 141.33 MiB (25) | 591.01 MiB / 619.77 MiB (25) | 310.57 MiB / 316.40 MiB (25) |
| tree_pss_end | 127.08 MiB / 139.91 MiB (25) | 585.53 MiB / 614.21 MiB (25) | 308.32 MiB / 314.31 MiB (25) |
| ext_peak_rss_end | 21.66 MiB / 22.70 MiB (25) | 516.88 MiB / 540.95 MiB (25) | 311.85 MiB / 316.96 MiB (25) |
| ext_anon_end | 8.24 MiB / 14.86 MiB (25) | 398.77 MiB / 426.95 MiB (25) | 251.51 MiB / 257.46 MiB (25) |
| ext_rss_ready | 9.24 MiB / 9.47 MiB (25) | 161.12 MiB / 161.66 MiB (25) | 111.50 MiB / 113.69 MiB (25) |
| ext_pss_ready | 9.24 MiB / 9.47 MiB (25) | 157.14 MiB / 157.71 MiB (25) | 109.51 MiB / 111.62 MiB (25) |
| heap_used_gc_end | 0.66 MiB / 0.67 MiB (25) | 35.93 MiB / 36.81 MiB (25) | 30.18 MiB / 30.22 MiB (25) |
| heap_used_asis_end | 2.26 MiB / 2.78 MiB (25) | 48.24 MiB / 96.66 MiB (25) | 86.66 MiB / 104.08 MiB (25) |
| heap_inuse_gc_end | 2.12 MiB / 2.22 MiB (25) | 168.84 MiB / 169.34 MiB (25) | 162.60 MiB / 174.35 MiB (25) |
| heap_inuse_asis_end | 3.64 MiB / 4.28 MiB (25) | 174.49 MiB / 197.35 MiB (25) | 190.07 MiB / 206.20 MiB (25) |
| sys_asis_end | 25.94 MiB / 26.01 MiB (25) | 446.05 MiB / 487.97 MiB (25) | 291.68 MiB / 314.69 MiB (25) |
| sys_gc_end | 25.94 MiB / 26.01 MiB (25) | 426.42 MiB / 466.71 MiB (25) | 272.16 MiB / 292.32 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 200 / 200 (25) | 200 / 200 (25) | 200 / 200 (25) |
| mallocs_workload | 15140566 / 15148435 (25) | - | - |
| num_gc_workload | 1154 / 1166 (25) | 41 / 57 (25) | 21 / 22 (25) |
| session_compaction | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) |
| session_context_window | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| session_custom_message | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) |
| session_entries | 531 / 531 (25) | 531 / 531 (25) | 531 / 531 (25) |
| session_tool_results | 180 / 180 (25) | 180 / 180 (25) | 180 / 180 (25) |
| tool_ms:get_context_remaining | 5.2 / 6.8 (750) | 6.7 / 9.1 (750) | 0.5 / 0.7 (750) |
| tool_ms:notes.append | 6.7 / 8.3 (1500) | 9.1 / 12.1 (1500) | 1.3 / 1.6 (1500) |
| tool_ms:notes.write | 7.1 / 8.8 (1500) | 11.3 / 14.7 (1500) | 3.5 / 4.6 (1500) |
| tool_ms:read | 10.9 / 12.8 (750) | 11.8 / 14.0 (750) | 3.8 / 4.8 (750) |
| total_alloc_workload | 2417.73 MiB / 2420.00 MiB (25) | - | - |

### Workload f, warm

| Metric | go median / p90 (n) | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|---:|
| cpu_total_ms_end | 4730 / 4930 (25) | 70830 / 72760 (25) | 42200 / 43180 (25) |
| ext_cpu_ms_end | 290 / 400 (25) | 60270 / 62060 (25) | 42210 / 43180 (25) |
| ext_rss_end | 558.06 MiB / 559.03 MiB (25) | 8093.95 MiB / 8132.78 MiB (25) | 6165.12 MiB / 6177.95 MiB (25) |
| ext_pss_end | 153.04 MiB / 153.98 MiB (25) | 4814.08 MiB / 4852.40 MiB (25) | 3090.12 MiB / 3102.97 MiB (25) |
| tree_rss_end | 4191.91 MiB / 4196.93 MiB (25) | 13149.36 MiB / 13182.81 MiB (25) | 6165.11 MiB / 6177.95 MiB (25) |
| tree_pss_end | 1386.53 MiB / 1388.46 MiB (25) | 7461.77 MiB / 7495.47 MiB (25) | 3090.10 MiB / 3102.97 MiB (25) |
| ext_peak_rss_end | 558.06 MiB / 559.03 MiB (25) | 9332.15 MiB / 9349.92 MiB (25) | 6389.39 MiB / 6405.65 MiB (25) |
| ext_anon_end | 146.11 MiB / 147.05 MiB (25) | 4761.07 MiB / 4799.39 MiB (25) | 3037.91 MiB / 3050.75 MiB (25) |
| ext_avg_rss_end | 9.30 MiB / 9.32 MiB (25) | 134.90 MiB / 135.55 MiB (25) | 102.75 MiB / 102.97 MiB (25) |
| ext_avg_pss_end | 2.55 MiB / 2.57 MiB (25) | 80.23 MiB / 80.87 MiB (25) | 51.50 MiB / 51.72 MiB (25) |
| all_ready_ms | 6695.5 / 6868.4 (25) | 47176.5 / 48278.1 (25) | 25532.0 / 25878.6 (25) |
| ext_count | 60 / 60 (25) | 60 / 60 (25) | 60 / 60 (25) |

## Behaviour parity of the workloads (model requests, session records)

identical across sides.

## Ratchet: go <= ts (pig host, Node extension runtime) on every metric

| Workload | Metric | go median | ts median | go p90 | ts p90 | verdict |
|---|---|---:|---:|---:|---:|---|
| a/cold | startup_ms | 634.7 | 2193.8 | 681.2 | 2272.0 | pass |
| a/cold | cpu_total_ms_end | 930 | 3530 | 1020 | 3670 | pass |
| a/cold | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/cold | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/cold | ext_cpu_ms_end | 10 | 2950 | 10 | 3080 | pass |
| a/cold | ext_rss_end | 9.20 MiB | 311.45 MiB | 9.46 MiB | 313.56 MiB | pass |
| a/cold | ext_pss_end | 9.20 MiB | 307.33 MiB | 9.46 MiB | 309.50 MiB | pass |
| a/cold | tree_rss_end | 79.00 MiB | 467.43 MiB | 85.05 MiB | 471.72 MiB | pass |
| a/cold | tree_pss_end | 77.54 MiB | 461.96 MiB | 83.62 MiB | 466.17 MiB | pass |
| a/cold | ext_peak_rss_end | 9.20 MiB | 311.45 MiB | 9.46 MiB | 313.56 MiB | pass |
| a/cold | ext_anon_end | 2.39 MiB | 255.66 MiB | 2.55 MiB | 257.89 MiB | pass |
| a/cold | ext_rss_ready | 9.20 MiB | 311.45 MiB | 9.46 MiB | 313.56 MiB | pass |
| a/cold | ext_pss_ready | 9.20 MiB | 307.33 MiB | 9.46 MiB | 309.50 MiB | pass |
| a/cold | heap_used_gc_end | 0.51 MiB | 47.18 MiB | 0.51 MiB | 47.18 MiB | pass |
| a/cold | heap_used_asis_end | 0.51 MiB | 48.61 MiB | 0.52 MiB | 48.62 MiB | pass |
| a/cold | heap_inuse_gc_end | 1.55 MiB | 181.25 MiB | 1.60 MiB | 181.50 MiB | pass |
| a/cold | heap_inuse_asis_end | 1.55 MiB | 181.25 MiB | 1.61 MiB | 181.50 MiB | pass |
| a/cold | sys_asis_end | 13.41 MiB | 302.14 MiB | 13.41 MiB | 304.50 MiB | pass |
| a/cold | sys_gc_end | 13.42 MiB | 302.14 MiB | 13.44 MiB | 304.50 MiB | pass |
| a/warm | startup_ms | 113.0 | 768.1 | 119.8 | 788.7 | pass |
| a/warm | cpu_total_ms_end | 80 | 1040 | 90 | 1070 | pass |
| a/warm | cpu_total_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/warm | cpu_ext_workload_ms | 0.0 | 0.0 | 0.0 | 0.0 | pass |
| a/warm | ext_cpu_ms_end | 10 | 870 | 10 | 900 | pass |
| a/warm | ext_rss_end | 9.21 MiB | 160.74 MiB | 9.58 MiB | 162.50 MiB | pass |
| a/warm | ext_pss_end | 9.21 MiB | 156.74 MiB | 9.58 MiB | 158.50 MiB | pass |
| a/warm | tree_rss_end | 69.78 MiB | 245.24 MiB | 70.37 MiB | 246.99 MiB | pass |
| a/warm | tree_pss_end | 68.27 MiB | 239.74 MiB | 68.84 MiB | 241.51 MiB | pass |
| a/warm | ext_peak_rss_end | 9.21 MiB | 160.74 MiB | 9.58 MiB | 162.50 MiB | pass |
| a/warm | ext_anon_end | 2.40 MiB | 105.25 MiB | 2.61 MiB | 106.96 MiB | pass |
| a/warm | ext_rss_ready | 9.21 MiB | 160.74 MiB | 9.58 MiB | 162.50 MiB | pass |
| a/warm | ext_pss_ready | 9.21 MiB | 156.74 MiB | 9.58 MiB | 158.50 MiB | pass |
| a/warm | heap_used_gc_end | 0.51 MiB | 34.94 MiB | 0.51 MiB | 34.94 MiB | pass |
| a/warm | heap_used_asis_end | 0.51 MiB | 35.19 MiB | 0.52 MiB | 35.20 MiB | pass |
| a/warm | heap_inuse_gc_end | 1.52 MiB | 69.84 MiB | 1.61 MiB | 101.59 MiB | pass |
| a/warm | heap_inuse_asis_end | 1.53 MiB | 70.09 MiB | 1.62 MiB | 101.59 MiB | pass |
| a/warm | sys_asis_end | 13.41 MiB | 150.00 MiB | 13.66 MiB | 155.94 MiB | pass |
| a/warm | sys_gc_end | 13.43 MiB | 150.00 MiB | 13.67 MiB | 155.94 MiB | pass |
| b/cold | startup_ms | 626.9 | 2176.7 | 730.5 | 2267.8 | pass |
| b/cold | first_turn_ms | 34.8 | 53.8 | 44.8 | 60.1 | pass |
| b/cold | to_first_tool_ms | 17.6 | 28.1 | 21.7 | 34.1 | pass |
| b/cold | turn_ms | 34.8 | 53.8 | 44.8 | 60.1 | pass |
| b/cold | cpu_total_ms_end | 950 | 3540 | 1100 | 3740 | pass |
| b/cold | cpu_total_workload_ms | 40.0 | 80.0 | 50.0 | 90.0 | pass |
| b/cold | cpu_ext_workload_ms | 20.0 | 50.0 | 20.0 | 70.0 | pass |
| b/cold | ext_cpu_ms_end | 20 | 2950 | 30 | 3160 | pass |
| b/cold | ext_rss_end | 11.02 MiB | 317.11 MiB | 12.11 MiB | 320.46 MiB | pass |
| b/cold | ext_pss_end | 11.02 MiB | 313.13 MiB | 12.10 MiB | 316.39 MiB | pass |
| b/cold | tree_rss_end | 84.84 MiB | 480.90 MiB | 90.24 MiB | 485.38 MiB | pass |
| b/cold | tree_pss_end | 83.33 MiB | 475.41 MiB | 88.71 MiB | 479.85 MiB | pass |
| b/cold | ext_peak_rss_end | 11.02 MiB | 317.11 MiB | 12.11 MiB | 320.46 MiB | pass |
| b/cold | ext_anon_end | 4.09 MiB | 256.87 MiB | 5.07 MiB | 260.14 MiB | pass |
| b/cold | ext_rss_ready | 9.27 MiB | 311.98 MiB | 9.54 MiB | 315.38 MiB | pass |
| b/cold | ext_pss_ready | 9.26 MiB | 307.92 MiB | 9.54 MiB | 311.30 MiB | pass |
| b/cold | heap_used_gc_end | 0.54 MiB | 47.42 MiB | 0.54 MiB | 47.43 MiB | pass |
| b/cold | heap_used_asis_end | 1.79 MiB | 49.99 MiB | 1.83 MiB | 50.00 MiB | pass |
| b/cold | heap_inuse_gc_end | 1.63 MiB | 181.25 MiB | 1.71 MiB | 181.75 MiB | pass |
| b/cold | heap_inuse_asis_end | 3.11 MiB | 181.25 MiB | 3.29 MiB | 181.75 MiB | pass |
| b/cold | sys_asis_end | 13.41 MiB | 306.83 MiB | 13.66 MiB | 309.00 MiB | pass |
| b/cold | sys_gc_end | 13.67 MiB | 306.83 MiB | 13.69 MiB | 309.00 MiB | pass |
| b/cold | tool_ms:get_context_remaining | 2.7 | 9.0 | 5.5 | 10.9 | pass |
| b/warm | startup_ms | 114.6 | 812.5 | 127.2 | 878.0 | pass |
| b/warm | first_turn_ms | 41.0 | 62.3 | 47.3 | 75.1 | pass |
| b/warm | to_first_tool_ms | 19.3 | 33.1 | 21.8 | 38.0 | pass |
| b/warm | turn_ms | 41.0 | 62.3 | 47.3 | 75.1 | pass |
| b/warm | cpu_total_ms_end | 130 | 1180 | 150 | 1300 | pass |
| b/warm | cpu_total_workload_ms | 50.0 | 80.0 | 70.0 | 120.0 | pass |
| b/warm | cpu_ext_workload_ms | 20.0 | 50.0 | 30.0 | 80.0 | pass |
| b/warm | ext_cpu_ms_end | 30 | 980 | 30 | 1070 | pass |
| b/warm | ext_rss_end | 10.91 MiB | 167.31 MiB | 12.12 MiB | 169.08 MiB | pass |
| b/warm | ext_pss_end | 10.91 MiB | 163.33 MiB | 12.11 MiB | 165.14 MiB | pass |
| b/warm | tree_rss_end | 73.92 MiB | 257.10 MiB | 75.27 MiB | 258.81 MiB | pass |
| b/warm | tree_pss_end | 72.40 MiB | 251.59 MiB | 73.75 MiB | 253.28 MiB | pass |
| b/warm | ext_peak_rss_end | 10.91 MiB | 167.31 MiB | 12.12 MiB | 169.08 MiB | pass |
| b/warm | ext_anon_end | 4.06 MiB | 107.14 MiB | 5.08 MiB | 108.89 MiB | pass |
| b/warm | ext_rss_ready | 9.25 MiB | 160.88 MiB | 9.56 MiB | 161.69 MiB | pass |
| b/warm | ext_pss_ready | 9.24 MiB | 156.83 MiB | 9.55 MiB | 157.72 MiB | pass |
| b/warm | heap_used_gc_end | 0.54 MiB | 35.17 MiB | 0.55 MiB | 35.17 MiB | pass |
| b/warm | heap_used_asis_end | 1.79 MiB | 36.54 MiB | 1.84 MiB | 36.55 MiB | pass |
| b/warm | heap_inuse_gc_end | 1.64 MiB | 70.09 MiB | 1.71 MiB | 101.84 MiB | pass |
| b/warm | heap_inuse_asis_end | 3.12 MiB | 69.84 MiB | 3.48 MiB | 101.59 MiB | pass |
| b/warm | sys_asis_end | 13.66 MiB | 156.00 MiB | 17.36 MiB | 161.62 MiB | pass |
| b/warm | sys_gc_end | 13.67 MiB | 156.00 MiB | 17.42 MiB | 161.62 MiB | pass |
| b/warm | tool_ms:get_context_remaining | 4.3 | 10.4 | 5.7 | 13.8 | pass |
| c1/warm | startup_ms | 116.3 | 845.4 | 132.3 | 880.3 | pass |
| c1/warm | to_first_tool_ms | 19.8 | 33.8 | 22.6 | 37.3 | pass |
| c1/warm | turn_ms | 825.8 | 682.0 | 861.1 | 825.2 | FAIL (median) |
| c1/warm | cpu_total_ms_end | 1080 | 2015 | 1130 | 2250 | pass |
| c1/warm | cpu_total_workload_ms | 990.0 | 850.0 | 1040.0 | 1060.0 | FAIL (median) |
| c1/warm | cpu_ext_workload_ms | 520.0 | 515.0 | 540.0 | 650.0 | FAIL (median) |
| c1/warm | ext_cpu_ms_end | 525 | 1490 | 550 | 1650 | pass |
| c1/warm | ext_rss_end | 14.51 MiB | 208.80 MiB | 14.73 MiB | 209.95 MiB | pass |
| c1/warm | ext_pss_end | 14.51 MiB | 204.87 MiB | 14.73 MiB | 206.02 MiB | pass |
| c1/warm | tree_rss_end | 85.43 MiB | 318.20 MiB | 86.29 MiB | 323.78 MiB | pass |
| c1/warm | tree_pss_end | 84.02 MiB | 312.90 MiB | 84.92 MiB | 318.61 MiB | pass |
| c1/warm | ext_peak_rss_end | 14.51 MiB | 208.80 MiB | 14.73 MiB | 209.95 MiB | pass |
| c1/warm | ext_anon_end | 7.39 MiB | 148.63 MiB | 7.59 MiB | 149.70 MiB | pass |
| c1/warm | ext_rss_ready | 9.28 MiB | 160.89 MiB | 9.50 MiB | 161.93 MiB | pass |
| c1/warm | ext_pss_ready | 9.27 MiB | 156.94 MiB | 9.50 MiB | 157.93 MiB | pass |
| c1/warm | heap_used_gc_end | 0.57 MiB | 35.89 MiB | 0.57 MiB | 35.89 MiB | pass |
| c1/warm | heap_used_asis_end | 1.77 MiB | 60.36 MiB | 2.76 MiB | 60.66 MiB | pass |
| c1/warm | heap_inuse_gc_end | 1.71 MiB | 102.59 MiB | 1.80 MiB | 102.84 MiB | pass |
| c1/warm | heap_inuse_asis_end | 3.04 MiB | 107.27 MiB | 3.94 MiB | 107.54 MiB | pass |
| c1/warm | sys_asis_end | 17.69 MiB | 188.18 MiB | 17.94 MiB | 199.11 MiB | pass |
| c1/warm | sys_gc_end | 17.69 MiB | 184.81 MiB | 17.94 MiB | 199.01 MiB | pass |
| c1/warm | tool_ms:notes.append | 5.2 | 7.1 | 7.2 | 10.7 | pass |
| c1/warm | tool_ms:notes.list | 5.5 | 8.0 | 7.5 | 14.1 | pass |
| c1/warm | tool_ms:notes.read | 4.9 | 7.2 | 7.3 | 10.4 | pass |
| c1/warm | tool_ms:notes.search | 5.1 | 7.7 | 7.2 | 11.4 | pass |
| c1/warm | tool_ms:notes.write | 5.9 | 9.1 | 7.4 | 13.0 | pass |
| c100/warm | startup_ms | 115.6 | 858.5 | 131.6 | 887.4 | pass |
| c100/warm | to_first_tool_ms | 20.0 | 34.4 | 22.5 | 38.0 | pass |
| c100/warm | turn_ms | 1080.3 | 1108.2 | 1114.9 | 1215.3 | pass |
| c100/warm | cpu_total_ms_end | 1415 | 2505 | 1470 | 2710 | pass |
| c100/warm | cpu_total_workload_ms | 1320.0 | 1355.0 | 1370.0 | 1500.0 | pass |
| c100/warm | cpu_ext_workload_ms | 755.0 | 895.0 | 790.0 | 1020.0 | pass |
| c100/warm | ext_cpu_ms_end | 760 | 1845 | 800 | 2030 | pass |
| c100/warm | ext_rss_end | 14.81 MiB | 224.54 MiB | 15.11 MiB | 226.93 MiB | pass |
| c100/warm | ext_pss_end | 14.81 MiB | 220.61 MiB | 15.10 MiB | 222.96 MiB | pass |
| c100/warm | tree_rss_end | 87.72 MiB | 334.80 MiB | 88.68 MiB | 340.58 MiB | pass |
| c100/warm | tree_pss_end | 86.30 MiB | 329.27 MiB | 87.25 MiB | 335.28 MiB | pass |
| c100/warm | ext_peak_rss_end | 14.84 MiB | 224.54 MiB | 15.11 MiB | 226.93 MiB | pass |
| c100/warm | ext_anon_end | 7.65 MiB | 164.36 MiB | 7.95 MiB | 166.60 MiB | pass |
| c100/warm | ext_rss_ready | 9.27 MiB | 160.97 MiB | 9.50 MiB | 161.95 MiB | pass |
| c100/warm | ext_pss_ready | 9.27 MiB | 157.06 MiB | 9.50 MiB | 157.97 MiB | pass |
| c100/warm | heap_used_gc_end | 0.62 MiB | 36.07 MiB | 0.63 MiB | 36.09 MiB | pass |
| c100/warm | heap_used_asis_end | 2.22 MiB | 58.24 MiB | 2.39 MiB | 64.77 MiB | pass |
| c100/warm | heap_inuse_gc_end | 1.80 MiB | 103.34 MiB | 1.88 MiB | 104.84 MiB | pass |
| c100/warm | heap_inuse_asis_end | 3.31 MiB | 110.09 MiB | 3.56 MiB | 113.60 MiB | pass |
| c100/warm | sys_asis_end | 17.69 MiB | 205.40 MiB | 17.94 MiB | 217.65 MiB | pass |
| c100/warm | sys_gc_end | 17.69 MiB | 200.39 MiB | 17.94 MiB | 211.52 MiB | pass |
| c100/warm | tool_ms:notes.append | 6.2 | 8.0 | 7.6 | 11.3 | pass |
| c100/warm | tool_ms:notes.list | 10.5 | 21.4 | 13.0 | 28.8 | pass |
| c100/warm | tool_ms:notes.read | 5.7 | 8.2 | 7.6 | 11.3 | pass |
| c100/warm | tool_ms:notes.search | 11.0 | 31.2 | 13.5 | 41.8 | pass |
| c100/warm | tool_ms:notes.write | 6.3 | 10.0 | 8.0 | 13.9 | pass |
| c1000/warm | startup_ms | 115.4 | 824.3 | 131.6 | 881.1 | pass |
| c1000/warm | to_first_tool_ms | 18.8 | 33.0 | 22.0 | 38.3 | pass |
| c1000/warm | turn_ms | 1242.9 | 1645.1 | 1335.4 | 2083.1 | pass |
| c1000/warm | cpu_total_ms_end | 1705 | 3205 | 1890 | 3920 | pass |
| c1000/warm | cpu_total_workload_ms | 1615.0 | 2080.0 | 1800.0 | 2710.0 | pass |
| c1000/warm | cpu_ext_workload_ms | 1090.0 | 1670.0 | 1210.0 | 2210.0 | pass |
| c1000/warm | ext_cpu_ms_end | 1100 | 2610 | 1210 | 3210 | pass |
| c1000/warm | ext_rss_end | 15.22 MiB | 231.21 MiB | 15.41 MiB | 233.26 MiB | pass |
| c1000/warm | ext_pss_end | 15.21 MiB | 227.34 MiB | 15.41 MiB | 229.33 MiB | pass |
| c1000/warm | tree_rss_end | 88.06 MiB | 338.65 MiB | 89.16 MiB | 346.00 MiB | pass |
| c1000/warm | tree_pss_end | 86.57 MiB | 333.39 MiB | 87.77 MiB | 340.69 MiB | pass |
| c1000/warm | ext_peak_rss_end | 15.30 MiB | 231.21 MiB | 16.50 MiB | 233.26 MiB | pass |
| c1000/warm | ext_anon_end | 8.06 MiB | 170.95 MiB | 8.25 MiB | 173.08 MiB | pass |
| c1000/warm | ext_rss_ready | 9.19 MiB | 160.87 MiB | 9.46 MiB | 161.84 MiB | pass |
| c1000/warm | ext_pss_ready | 9.19 MiB | 156.84 MiB | 9.46 MiB | 157.81 MiB | pass |
| c1000/warm | heap_used_gc_end | 0.71 MiB | 36.16 MiB | 0.71 MiB | 36.17 MiB | pass |
| c1000/warm | heap_used_asis_end | 2.46 MiB | 53.80 MiB | 2.46 MiB | 65.20 MiB | pass |
| c1000/warm | heap_inuse_gc_end | 1.88 MiB | 104.09 MiB | 1.96 MiB | 109.09 MiB | pass |
| c1000/warm | heap_inuse_asis_end | 3.55 MiB | 113.24 MiB | 3.62 MiB | 117.49 MiB | pass |
| c1000/warm | sys_asis_end | 17.69 MiB | 219.95 MiB | 17.94 MiB | 223.60 MiB | pass |
| c1000/warm | sys_gc_end | 17.69 MiB | 217.96 MiB | 17.94 MiB | 220.25 MiB | pass |
| c1000/warm | tool_ms:notes.append | 5.9 | 6.8 | 7.7 | 10.6 | pass |
| c1000/warm | tool_ms:notes.list | 20.8 | 42.9 | 24.0 | 61.9 | pass |
| c1000/warm | tool_ms:notes.read | 5.6 | 7.3 | 7.6 | 11.4 | pass |
| c1000/warm | tool_ms:notes.search | 32.0 | 114.1 | 36.5 | 176.4 | pass |
| c1000/warm | tool_ms:notes.write | 6.0 | 9.0 | 7.9 | 13.2 | pass |
| d50/warm | startup_ms | 112.5 | 804.9 | 129.1 | 881.8 | pass |
| d50/warm | to_first_tool_ms | 19.3 | 35.7 | 22.8 | 39.5 | pass |
| d50/warm | turn_ms | 151.2 | 367.8 | 171.3 | 402.9 | pass |
| d50/warm | cpu_total_ms_end | 335 | 1715 | 380 | 1870 | pass |
| d50/warm | cpu_total_workload_ms | 250.0 | 610.0 | 290.0 | 670.0 | pass |
| d50/warm | cpu_ext_workload_ms | 80.0 | 420.0 | 100.0 | 470.0 | pass |
| d50/warm | ext_cpu_ms_end | 90 | 1330 | 110 | 1470 | pass |
| d50/warm | ext_rss_end | 14.45 MiB | 208.75 MiB | 14.70 MiB | 210.27 MiB | pass |
| d50/warm | ext_pss_end | 14.45 MiB | 204.81 MiB | 14.69 MiB | 206.42 MiB | pass |
| d50/warm | tree_rss_end | 77.61 MiB | 301.36 MiB | 78.06 MiB | 303.11 MiB | pass |
| d50/warm | tree_pss_end | 76.16 MiB | 296.03 MiB | 76.71 MiB | 297.63 MiB | pass |
| d50/warm | ext_peak_rss_end | 14.45 MiB | 208.75 MiB | 14.71 MiB | 210.27 MiB | pass |
| d50/warm | ext_anon_end | 7.23 MiB | 148.40 MiB | 7.47 MiB | 150.04 MiB | pass |
| d50/warm | ext_rss_ready | 9.37 MiB | 161.11 MiB | 9.58 MiB | 161.89 MiB | pass |
| d50/warm | ext_pss_ready | 9.37 MiB | 157.12 MiB | 9.58 MiB | 157.90 MiB | pass |
| d50/warm | heap_used_gc_end | 0.55 MiB | 36.26 MiB | 0.56 MiB | 36.27 MiB | pass |
| d50/warm | heap_used_asis_end | 1.17 MiB | 60.11 MiB | 3.09 MiB | 60.56 MiB | pass |
| d50/warm | heap_inuse_gc_end | 1.65 MiB | 104.09 MiB | 1.73 MiB | 104.59 MiB | pass |
| d50/warm | heap_inuse_asis_end | 2.48 MiB | 105.36 MiB | 4.37 MiB | 105.86 MiB | pass |
| d50/warm | sys_asis_end | 17.69 MiB | 208.38 MiB | 17.94 MiB | 210.00 MiB | pass |
| d50/warm | sys_gc_end | 17.69 MiB | 208.38 MiB | 17.94 MiB | 210.00 MiB | pass |
| d50/warm | tool_ms:history.read | 20.0 | 34.1 | 23.5 | 39.0 | pass |
| d50/warm | tool_ms:history.search:common | 19.3 | 30.5 | 23.3 | 36.7 | pass |
| d50/warm | tool_ms:history.search:rare | 49.7 | 231.9 | 57.2 | 245.0 | pass |
| d200/warm | startup_ms | 114.6 | 785.5 | 126.1 | 879.4 | pass |
| d200/warm | to_first_tool_ms | 19.4 | 32.5 | 22.0 | 38.7 | pass |
| d200/warm | turn_ms | 221.2 | 465.5 | 246.6 | 529.7 | pass |
| d200/warm | cpu_total_ms_end | 655 | 2000 | 740 | 2310 | pass |
| d200/warm | cpu_total_workload_ms | 570.0 | 940.0 | 650.0 | 1100.0 | pass |
| d200/warm | cpu_ext_workload_ms | 100.0 | 455.0 | 120.0 | 540.0 | pass |
| d200/warm | ext_cpu_ms_end | 100 | 1355 | 120 | 1560 | pass |
| d200/warm | ext_rss_end | 14.51 MiB | 212.39 MiB | 14.87 MiB | 214.01 MiB | pass |
| d200/warm | ext_pss_end | 14.51 MiB | 208.41 MiB | 14.86 MiB | 210.02 MiB | pass |
| d200/warm | tree_rss_end | 77.59 MiB | 305.02 MiB | 78.48 MiB | 306.48 MiB | pass |
| d200/warm | tree_pss_end | 76.18 MiB | 299.67 MiB | 77.03 MiB | 301.15 MiB | pass |
| d200/warm | ext_peak_rss_end | 14.52 MiB | 212.39 MiB | 14.96 MiB | 214.01 MiB | pass |
| d200/warm | ext_anon_end | 7.29 MiB | 152.07 MiB | 7.64 MiB | 153.75 MiB | pass |
| d200/warm | ext_rss_ready | 9.26 MiB | 161.16 MiB | 9.49 MiB | 162.13 MiB | pass |
| d200/warm | ext_pss_ready | 9.25 MiB | 157.16 MiB | 9.48 MiB | 158.18 MiB | pass |
| d200/warm | heap_used_gc_end | 0.56 MiB | 36.31 MiB | 0.57 MiB | 36.32 MiB | pass |
| d200/warm | heap_used_asis_end | 1.59 MiB | 52.71 MiB | 3.11 MiB | 53.06 MiB | pass |
| d200/warm | heap_inuse_gc_end | 1.66 MiB | 104.34 MiB | 1.73 MiB | 104.84 MiB | pass |
| d200/warm | heap_inuse_asis_end | 2.93 MiB | 105.59 MiB | 4.27 MiB | 106.09 MiB | pass |
| d200/warm | sys_asis_end | 17.69 MiB | 207.85 MiB | 17.94 MiB | 210.00 MiB | pass |
| d200/warm | sys_gc_end | 17.69 MiB | 207.85 MiB | 17.94 MiB | 210.00 MiB | pass |
| d200/warm | tool_ms:history.read | 42.7 | 54.5 | 47.9 | 66.0 | pass |
| d200/warm | tool_ms:history.search:common | 31.4 | 41.3 | 36.9 | 49.9 | pass |
| d200/warm | tool_ms:history.search:rare | 83.6 | 293.5 | 93.1 | 329.0 | pass |
| e/warm | startup_ms | 115.1 | 796.4 | 128.4 | 882.3 | pass |
| e/warm | to_first_tool_ms | 19.8 | 33.9 | 23.0 | 41.3 | pass |
| e/warm | turn_ms | 26358.7 | 15996.7 | 26858.5 | 16444.8 | FAIL (median) |
| e/warm | cpu_total_ms_end | 31510 | 19490 | 32330 | 19910 | FAIL (median) |
| e/warm | cpu_total_workload_ms | 31430.0 | 18370.0 | 32250.0 | 18830.0 | FAIL (median) |
| e/warm | cpu_ext_workload_ms | 18130.0 | 9300.0 | 18630.0 | 9650.0 | FAIL (median) |
| e/warm | ext_cpu_ms_end | 18140 | 10230 | 18640 | 10550 | FAIL (median) |
| e/warm | ext_rss_end | 15.40 MiB | 459.14 MiB | 22.08 MiB | 487.18 MiB | pass |
| e/warm | ext_pss_end | 15.39 MiB | 455.12 MiB | 22.07 MiB | 483.25 MiB | pass |
| e/warm | tree_rss_end | 128.54 MiB | 591.01 MiB | 141.33 MiB | 619.77 MiB | pass |
| e/warm | tree_pss_end | 127.08 MiB | 585.53 MiB | 139.91 MiB | 614.21 MiB | pass |
| e/warm | ext_peak_rss_end | 21.66 MiB | 516.88 MiB | 22.70 MiB | 540.95 MiB | pass |
| e/warm | ext_anon_end | 8.24 MiB | 398.77 MiB | 14.86 MiB | 426.95 MiB | pass |
| e/warm | ext_rss_ready | 9.24 MiB | 161.12 MiB | 9.47 MiB | 161.66 MiB | pass |
| e/warm | ext_pss_ready | 9.24 MiB | 157.14 MiB | 9.47 MiB | 157.71 MiB | pass |
| e/warm | heap_used_gc_end | 0.66 MiB | 35.93 MiB | 0.67 MiB | 36.81 MiB | pass |
| e/warm | heap_used_asis_end | 2.26 MiB | 48.24 MiB | 2.78 MiB | 96.66 MiB | pass |
| e/warm | heap_inuse_gc_end | 2.12 MiB | 168.84 MiB | 2.22 MiB | 169.34 MiB | pass |
| e/warm | heap_inuse_asis_end | 3.64 MiB | 174.49 MiB | 4.28 MiB | 197.35 MiB | pass |
| e/warm | sys_asis_end | 25.94 MiB | 446.05 MiB | 26.01 MiB | 487.97 MiB | pass |
| e/warm | sys_gc_end | 25.94 MiB | 426.42 MiB | 26.01 MiB | 466.71 MiB | pass |
| e/warm | tool_ms:get_context_remaining | 5.2 | 6.7 | 6.8 | 9.1 | pass |
| e/warm | tool_ms:notes.append | 6.7 | 9.1 | 8.3 | 12.1 | pass |
| e/warm | tool_ms:notes.write | 7.1 | 11.3 | 8.8 | 14.7 | pass |
| e/warm | tool_ms:read | 10.9 | 11.8 | 12.8 | 14.0 | pass |
| f/warm | cpu_total_ms_end | 4730 | 70830 | 4930 | 72760 | pass |
| f/warm | ext_cpu_ms_end | 290 | 60270 | 400 | 62060 | pass |
| f/warm | ext_rss_end | 558.06 MiB | 8093.95 MiB | 559.03 MiB | 8132.78 MiB | pass |
| f/warm | ext_pss_end | 153.04 MiB | 4814.08 MiB | 153.98 MiB | 4852.40 MiB | pass |
| f/warm | tree_rss_end | 4191.91 MiB | 13149.36 MiB | 4196.93 MiB | 13182.81 MiB | pass |
| f/warm | tree_pss_end | 1386.53 MiB | 7461.77 MiB | 1388.46 MiB | 7495.47 MiB | pass |
| f/warm | ext_peak_rss_end | 558.06 MiB | 9332.15 MiB | 559.03 MiB | 9349.92 MiB | pass |
| f/warm | ext_anon_end | 146.11 MiB | 4761.07 MiB | 147.05 MiB | 4799.39 MiB | pass |
| f/warm | ext_avg_rss_end | 9.30 MiB | 134.90 MiB | 9.32 MiB | 135.55 MiB | pass |
| f/warm | ext_avg_pss_end | 2.55 MiB | 80.23 MiB | 2.57 MiB | 80.87 MiB | pass |
| f/warm | all_ready_ms | 6695.5 | 47176.5 | 6868.4 | 48278.1 | pass |

Median misses: 8.

## Relative spread (p90-p50)/p50 above 5% (go and ts sides, ratchet metrics)

- a/cold go startup_ms: 7.3% (n=25, median 634.7)
- a/cold go cpu_total_ms_end: 9.7% (n=25, median 930)
- a/cold go tree_rss_end: 7.6% (n=25, median 79.00 MiB)
- a/cold go tree_pss_end: 7.8% (n=25, median 77.54 MiB)
- a/cold go ext_anon_end: 6.5% (n=25, median 2.39 MiB)
- a/warm go startup_ms: 6.1% (n=25, median 113.0)
- a/warm go cpu_total_ms_end: 12.5% (n=25, median 80)
- a/warm go ext_anon_end: 8.8% (n=25, median 2.40 MiB)
- a/warm go heap_inuse_gc_end: 5.6% (n=25, median 1.52 MiB)
- a/warm ts heap_inuse_gc_end: 45.5% (n=25, median 69.84 MiB)
- a/warm go heap_inuse_asis_end: 5.6% (n=25, median 1.53 MiB)
- a/warm ts heap_inuse_asis_end: 44.9% (n=25, median 70.09 MiB)
- b/cold go startup_ms: 16.5% (n=25, median 626.9)
- b/cold go first_turn_ms: 28.7% (n=25, median 34.8)
- b/cold ts first_turn_ms: 11.7% (n=25, median 53.8)
- b/cold go to_first_tool_ms: 23.0% (n=25, median 17.6)
- b/cold ts to_first_tool_ms: 21.3% (n=25, median 28.1)
- b/cold go turn_ms: 28.7% (n=25, median 34.8)
- b/cold ts turn_ms: 11.7% (n=25, median 53.8)
- b/cold go cpu_total_ms_end: 15.8% (n=25, median 950)
- b/cold ts cpu_total_ms_end: 5.6% (n=25, median 3540)
- b/cold go cpu_total_workload_ms: 25.0% (n=25, median 40.0)
- b/cold ts cpu_total_workload_ms: 12.5% (n=25, median 80.0)
- b/cold ts cpu_ext_workload_ms: 40.0% (n=25, median 50.0)
- b/cold go ext_cpu_ms_end: 50.0% (n=25, median 20)
- b/cold ts ext_cpu_ms_end: 7.1% (n=25, median 2950)
- b/cold go ext_rss_end: 9.9% (n=25, median 11.02 MiB)
- b/cold go ext_pss_end: 9.9% (n=25, median 11.02 MiB)
- b/cold go tree_rss_end: 6.4% (n=25, median 84.84 MiB)
- b/cold go tree_pss_end: 6.5% (n=25, median 83.33 MiB)
- b/cold go ext_peak_rss_end: 9.9% (n=25, median 11.02 MiB)
- b/cold go ext_anon_end: 23.9% (n=25, median 4.09 MiB)
- b/cold go heap_inuse_asis_end: 5.8% (n=25, median 3.11 MiB)
- b/cold go tool_ms:get_context_remaining: 104.8% (n=25, median 2.7)
- b/cold ts tool_ms:get_context_remaining: 21.2% (n=25, median 9.0)
- b/warm go startup_ms: 11.0% (n=25, median 114.6)
- b/warm ts startup_ms: 8.1% (n=25, median 812.5)
- b/warm go first_turn_ms: 15.2% (n=25, median 41.0)
- b/warm ts first_turn_ms: 20.7% (n=25, median 62.3)
- b/warm go to_first_tool_ms: 12.4% (n=25, median 19.3)
- b/warm ts to_first_tool_ms: 14.8% (n=25, median 33.1)
- b/warm go turn_ms: 15.2% (n=25, median 41.0)
- b/warm ts turn_ms: 20.7% (n=25, median 62.3)
- b/warm go cpu_total_ms_end: 15.4% (n=25, median 130)
- b/warm ts cpu_total_ms_end: 10.2% (n=25, median 1180)
- b/warm go cpu_total_workload_ms: 40.0% (n=25, median 50.0)
- b/warm ts cpu_total_workload_ms: 50.0% (n=25, median 80.0)
- b/warm go cpu_ext_workload_ms: 50.0% (n=25, median 20.0)
- b/warm ts cpu_ext_workload_ms: 60.0% (n=25, median 50.0)
- b/warm ts ext_cpu_ms_end: 9.2% (n=25, median 980)
- b/warm go ext_rss_end: 11.1% (n=25, median 10.91 MiB)
- b/warm go ext_pss_end: 11.1% (n=25, median 10.91 MiB)
- b/warm go ext_peak_rss_end: 11.1% (n=25, median 10.91 MiB)
- b/warm go ext_anon_end: 25.1% (n=25, median 4.06 MiB)
- b/warm ts heap_inuse_gc_end: 45.3% (n=25, median 70.09 MiB)
- b/warm go heap_inuse_asis_end: 11.2% (n=25, median 3.12 MiB)
- b/warm ts heap_inuse_asis_end: 45.5% (n=25, median 69.84 MiB)
- b/warm go sys_asis_end: 27.0% (n=25, median 13.66 MiB)
- b/warm go sys_gc_end: 27.5% (n=25, median 13.67 MiB)
- b/warm go tool_ms:get_context_remaining: 32.6% (n=25, median 4.3)
- b/warm ts tool_ms:get_context_remaining: 33.6% (n=25, median 10.4)
- c1/warm go startup_ms: 13.8% (n=60, median 116.3)
- c1/warm go to_first_tool_ms: 14.2% (n=60, median 19.8)
- c1/warm ts to_first_tool_ms: 10.5% (n=60, median 33.8)
- c1/warm ts turn_ms: 21.0% (n=60, median 682.0)
- c1/warm ts cpu_total_ms_end: 11.7% (n=60, median 2015)
- c1/warm go cpu_total_workload_ms: 5.1% (n=60, median 990.0)
- c1/warm ts cpu_total_workload_ms: 24.7% (n=60, median 850.0)
- c1/warm ts cpu_ext_workload_ms: 26.2% (n=60, median 515.0)
- c1/warm ts ext_cpu_ms_end: 10.7% (n=60, median 1490)
- c1/warm go heap_used_asis_end: 55.3% (n=60, median 1.77 MiB)
- c1/warm go heap_inuse_gc_end: 5.3% (n=60, median 1.71 MiB)
- c1/warm go heap_inuse_asis_end: 29.7% (n=60, median 3.04 MiB)
- c1/warm ts sys_asis_end: 5.8% (n=60, median 188.18 MiB)
- c1/warm ts sys_gc_end: 7.7% (n=60, median 184.81 MiB)
- c1/warm go tool_ms:notes.append: 38.4% (n=360, median 5.2)
- c1/warm ts tool_ms:notes.append: 50.3% (n=360, median 7.1)
- c1/warm go tool_ms:notes.list: 37.5% (n=360, median 5.5)
- c1/warm ts tool_ms:notes.list: 75.4% (n=360, median 8.0)
- c1/warm go tool_ms:notes.read: 48.1% (n=360, median 4.9)
- c1/warm ts tool_ms:notes.read: 44.4% (n=360, median 7.2)
- c1/warm go tool_ms:notes.search: 41.1% (n=360, median 5.1)
- c1/warm ts tool_ms:notes.search: 48.8% (n=360, median 7.7)
- c1/warm go tool_ms:notes.write: 24.8% (n=360, median 5.9)
- c1/warm ts tool_ms:notes.write: 41.8% (n=360, median 9.1)
- c100/warm go startup_ms: 13.8% (n=60, median 115.6)
- c100/warm go to_first_tool_ms: 12.4% (n=60, median 20.0)
- c100/warm ts to_first_tool_ms: 10.3% (n=60, median 34.4)
- c100/warm ts turn_ms: 9.7% (n=60, median 1108.2)
- c100/warm ts cpu_total_ms_end: 8.2% (n=60, median 2505)
- c100/warm ts cpu_total_workload_ms: 10.7% (n=60, median 1355.0)
- c100/warm ts cpu_ext_workload_ms: 14.0% (n=60, median 895.0)
- c100/warm go ext_cpu_ms_end: 5.3% (n=60, median 760)
- c100/warm ts ext_cpu_ms_end: 10.0% (n=60, median 1845)
- c100/warm go heap_used_asis_end: 7.6% (n=60, median 2.22 MiB)
- c100/warm ts heap_used_asis_end: 11.2% (n=60, median 58.24 MiB)
- c100/warm go heap_inuse_asis_end: 7.7% (n=60, median 3.31 MiB)
- c100/warm ts sys_asis_end: 6.0% (n=60, median 205.40 MiB)
- c100/warm ts sys_gc_end: 5.6% (n=60, median 200.39 MiB)
- c100/warm go tool_ms:notes.append: 23.0% (n=360, median 6.2)
- c100/warm ts tool_ms:notes.append: 40.9% (n=360, median 8.0)
- c100/warm go tool_ms:notes.list: 24.2% (n=360, median 10.5)
- c100/warm ts tool_ms:notes.list: 34.5% (n=360, median 21.4)
- c100/warm go tool_ms:notes.read: 33.4% (n=360, median 5.7)
- c100/warm ts tool_ms:notes.read: 38.0% (n=360, median 8.2)
- c100/warm go tool_ms:notes.search: 22.9% (n=360, median 11.0)
- c100/warm ts tool_ms:notes.search: 34.0% (n=360, median 31.2)
- c100/warm go tool_ms:notes.write: 26.0% (n=360, median 6.3)
- c100/warm ts tool_ms:notes.write: 39.8% (n=360, median 10.0)
- c1000/warm go startup_ms: 14.1% (n=60, median 115.4)
- c1000/warm ts startup_ms: 6.9% (n=60, median 824.3)
- c1000/warm go to_first_tool_ms: 17.0% (n=60, median 18.8)
- c1000/warm ts to_first_tool_ms: 16.1% (n=60, median 33.0)
- c1000/warm go turn_ms: 7.4% (n=60, median 1242.9)
- c1000/warm ts turn_ms: 26.6% (n=60, median 1645.1)
- c1000/warm go cpu_total_ms_end: 10.9% (n=60, median 1705)
- c1000/warm ts cpu_total_ms_end: 22.3% (n=60, median 3205)
- c1000/warm go cpu_total_workload_ms: 11.5% (n=60, median 1615.0)
- c1000/warm ts cpu_total_workload_ms: 30.3% (n=60, median 2080.0)
- c1000/warm go cpu_ext_workload_ms: 11.0% (n=60, median 1090.0)
- c1000/warm ts cpu_ext_workload_ms: 32.3% (n=60, median 1670.0)
- c1000/warm go ext_cpu_ms_end: 10.0% (n=60, median 1100)
- c1000/warm ts ext_cpu_ms_end: 23.0% (n=60, median 2610)
- c1000/warm go ext_peak_rss_end: 7.8% (n=60, median 15.30 MiB)
- c1000/warm ts heap_used_asis_end: 21.2% (n=60, median 53.80 MiB)
- c1000/warm go tool_ms:notes.append: 31.0% (n=360, median 5.9)
- c1000/warm ts tool_ms:notes.append: 55.0% (n=360, median 6.8)
- c1000/warm go tool_ms:notes.list: 15.4% (n=360, median 20.8)
- c1000/warm ts tool_ms:notes.list: 44.4% (n=360, median 42.9)
- c1000/warm go tool_ms:notes.read: 35.5% (n=360, median 5.6)
- c1000/warm ts tool_ms:notes.read: 54.6% (n=360, median 7.3)
- c1000/warm go tool_ms:notes.search: 14.0% (n=360, median 32.0)
- c1000/warm ts tool_ms:notes.search: 54.6% (n=360, median 114.1)
- c1000/warm go tool_ms:notes.write: 29.9% (n=360, median 6.0)
- c1000/warm ts tool_ms:notes.write: 46.1% (n=360, median 9.0)
- d50/warm go startup_ms: 14.7% (n=60, median 112.5)
- d50/warm ts startup_ms: 9.5% (n=60, median 804.9)
- d50/warm go to_first_tool_ms: 18.3% (n=60, median 19.3)
- d50/warm ts to_first_tool_ms: 10.6% (n=60, median 35.7)
- d50/warm go turn_ms: 13.3% (n=60, median 151.2)
- d50/warm ts turn_ms: 9.6% (n=60, median 367.8)
- d50/warm go cpu_total_ms_end: 13.4% (n=60, median 335)
- d50/warm ts cpu_total_ms_end: 9.0% (n=60, median 1715)
- d50/warm go cpu_total_workload_ms: 16.0% (n=60, median 250.0)
- d50/warm ts cpu_total_workload_ms: 9.8% (n=60, median 610.0)
- d50/warm go cpu_ext_workload_ms: 25.0% (n=60, median 80.0)
- d50/warm ts cpu_ext_workload_ms: 11.9% (n=60, median 420.0)
- d50/warm go ext_cpu_ms_end: 22.2% (n=60, median 90)
- d50/warm ts ext_cpu_ms_end: 10.5% (n=60, median 1330)
- d50/warm go heap_used_asis_end: 164.6% (n=60, median 1.17 MiB)
- d50/warm go heap_inuse_asis_end: 76.1% (n=60, median 2.48 MiB)
- d50/warm go tool_ms:history.read: 17.5% (n=60, median 20.0)
- d50/warm ts tool_ms:history.read: 14.3% (n=60, median 34.1)
- d50/warm go tool_ms:history.search:common: 20.3% (n=60, median 19.3)
- d50/warm ts tool_ms:history.search:common: 20.3% (n=60, median 30.5)
- d50/warm go tool_ms:history.search:rare: 15.1% (n=60, median 49.7)
- d50/warm ts tool_ms:history.search:rare: 5.6% (n=60, median 231.9)
- d200/warm go startup_ms: 10.1% (n=60, median 114.6)
- d200/warm ts startup_ms: 11.9% (n=60, median 785.5)
- d200/warm go to_first_tool_ms: 13.8% (n=60, median 19.4)
- d200/warm ts to_first_tool_ms: 19.3% (n=60, median 32.5)
- d200/warm go turn_ms: 11.5% (n=60, median 221.2)
- d200/warm ts turn_ms: 13.8% (n=60, median 465.5)
- d200/warm go cpu_total_ms_end: 13.0% (n=60, median 655)
- d200/warm ts cpu_total_ms_end: 15.5% (n=60, median 2000)
- d200/warm go cpu_total_workload_ms: 14.0% (n=60, median 570.0)
- d200/warm ts cpu_total_workload_ms: 17.0% (n=60, median 940.0)
- d200/warm go cpu_ext_workload_ms: 20.0% (n=60, median 100.0)
- d200/warm ts cpu_ext_workload_ms: 18.7% (n=60, median 455.0)
- d200/warm go ext_cpu_ms_end: 20.0% (n=60, median 100)
- d200/warm ts ext_cpu_ms_end: 15.1% (n=60, median 1355)
- d200/warm go heap_used_asis_end: 94.9% (n=60, median 1.59 MiB)
- d200/warm go heap_inuse_asis_end: 45.4% (n=60, median 2.93 MiB)
- d200/warm go tool_ms:history.read: 12.1% (n=60, median 42.7)
- d200/warm ts tool_ms:history.read: 20.9% (n=60, median 54.5)
- d200/warm go tool_ms:history.search:common: 17.5% (n=60, median 31.4)
- d200/warm ts tool_ms:history.search:common: 20.8% (n=60, median 41.3)
- d200/warm go tool_ms:history.search:rare: 11.4% (n=60, median 83.6)
- d200/warm ts tool_ms:history.search:rare: 12.1% (n=60, median 293.5)
- e/warm go startup_ms: 11.5% (n=25, median 115.1)
- e/warm ts startup_ms: 10.8% (n=25, median 796.4)
- e/warm go to_first_tool_ms: 16.2% (n=25, median 19.8)
- e/warm ts to_first_tool_ms: 21.7% (n=25, median 33.9)
- e/warm go ext_rss_end: 43.4% (n=25, median 15.40 MiB)
- e/warm ts ext_rss_end: 6.1% (n=25, median 459.14 MiB)
- e/warm go ext_pss_end: 43.4% (n=25, median 15.39 MiB)
- e/warm ts ext_pss_end: 6.2% (n=25, median 455.12 MiB)
- e/warm go tree_rss_end: 10.0% (n=25, median 128.54 MiB)
- e/warm go tree_pss_end: 10.1% (n=25, median 127.08 MiB)
- e/warm go ext_anon_end: 80.3% (n=25, median 8.24 MiB)
- e/warm ts ext_anon_end: 7.1% (n=25, median 398.77 MiB)
- e/warm go heap_used_asis_end: 23.3% (n=25, median 2.26 MiB)
- e/warm ts heap_used_asis_end: 100.4% (n=25, median 48.24 MiB)
- e/warm go heap_inuse_asis_end: 17.6% (n=25, median 3.64 MiB)
- e/warm ts heap_inuse_asis_end: 13.1% (n=25, median 174.49 MiB)
- e/warm ts sys_asis_end: 9.4% (n=25, median 446.05 MiB)
- e/warm ts sys_gc_end: 9.4% (n=25, median 426.42 MiB)
- e/warm go tool_ms:get_context_remaining: 30.3% (n=750, median 5.2)
- e/warm ts tool_ms:get_context_remaining: 34.8% (n=750, median 6.7)
- e/warm go tool_ms:notes.append: 24.2% (n=1500, median 6.7)
- e/warm ts tool_ms:notes.append: 32.3% (n=1500, median 9.1)
- e/warm go tool_ms:notes.write: 23.4% (n=1500, median 7.1)
- e/warm ts tool_ms:notes.write: 29.7% (n=1500, median 11.3)
- e/warm go tool_ms:read: 17.6% (n=750, median 10.9)
- e/warm ts tool_ms:read: 19.0% (n=750, median 11.8)
- f/warm go ext_cpu_ms_end: 37.9% (n=25, median 290)

