# Posthorse perf summary

Medians and p90 over fresh-process samples; n in parentheses. Metric definitions: README.md. Every metric: summary-full.md.

### Workload a, cold

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 2193.8 / 2272.0 (25) | 1542.5 / 1629.0 (25) |
| cpu_total_ms_end | 3530 / 3670 (25) | 2580 / 2730 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 2950 / 3080 (25) | 2580 / 2730 (25) |
| ext_rss_end | 311.45 MiB / 313.56 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| ext_pss_end | 307.33 MiB / 309.50 MiB (25) | 268.05 MiB / 271.14 MiB (25) |
| tree_rss_end | 467.43 MiB / 471.72 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| tree_pss_end | 461.96 MiB / 466.17 MiB (25) | 268.05 MiB / 271.14 MiB (25) |
| ext_peak_rss_end | 311.45 MiB / 313.56 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| ext_anon_end | 255.66 MiB / 257.89 MiB (25) | 217.60 MiB / 220.72 MiB (25) |
| ext_rss_ready | 311.45 MiB / 313.56 MiB (25) | 269.96 MiB / 273.09 MiB (25) |
| ext_pss_ready | 307.33 MiB / 309.50 MiB (25) | 268.05 MiB / 271.14 MiB (25) |
| heap_used_gc_end | 47.18 MiB / 47.18 MiB (25) | 33.89 MiB / 33.89 MiB (25) |
| heap_used_asis_end | 48.61 MiB / 48.62 MiB (25) | 35.34 MiB / 35.35 MiB (25) |
| heap_inuse_gc_end | 181.25 MiB / 181.50 MiB (25) | 168.07 MiB / 168.57 MiB (25) |
| heap_inuse_asis_end | 181.25 MiB / 181.50 MiB (25) | 168.82 MiB / 169.32 MiB (25) |
| sys_asis_end | 302.14 MiB / 304.50 MiB (25) | 264.38 MiB / 268.88 MiB (25) |
| sys_gc_end | 302.14 MiB / 304.50 MiB (25) | 264.38 MiB / 268.88 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) |
| num_gc_workload | 1 / 1 (25) | 1 / 1 (25) |

### Workload a, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 768.1 / 788.7 (25) | 408.7 / 427.3 (25) |
| cpu_total_ms_end | 1040 / 1070 (25) | 600 / 640 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 870 / 900 (25) | 600 / 640 (25) |
| ext_rss_end | 160.74 MiB / 162.50 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| ext_pss_end | 156.74 MiB / 158.50 MiB (25) | 109.17 MiB / 112.08 MiB (25) |
| tree_rss_end | 245.24 MiB / 246.99 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| tree_pss_end | 239.74 MiB / 241.51 MiB (25) | 109.17 MiB / 112.08 MiB (25) |
| ext_peak_rss_end | 160.74 MiB / 162.50 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| ext_anon_end | 105.25 MiB / 106.96 MiB (25) | 59.10 MiB / 61.99 MiB (25) |
| ext_rss_ready | 160.74 MiB / 162.50 MiB (25) | 111.16 MiB / 114.14 MiB (25) |
| ext_pss_ready | 156.74 MiB / 158.50 MiB (25) | 109.17 MiB / 112.08 MiB (25) |
| heap_used_gc_end | 34.94 MiB / 34.94 MiB (25) | 21.75 MiB / 21.76 MiB (25) |
| heap_used_asis_end | 35.19 MiB / 35.20 MiB (25) | 21.98 MiB / 21.99 MiB (25) |
| heap_inuse_gc_end | 69.84 MiB / 101.59 MiB (25) | 40.66 MiB / 40.91 MiB (25) |
| heap_inuse_asis_end | 70.09 MiB / 101.59 MiB (25) | 40.91 MiB / 42.41 MiB (25) |
| sys_asis_end | 150.00 MiB / 155.94 MiB (25) | 105.11 MiB / 107.78 MiB (25) |
| sys_gc_end | 150.00 MiB / 155.94 MiB (25) | 105.11 MiB / 107.78 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) |
| num_gc_workload | 1 / 1 (25) | 1 / 1 (25) |

### Workload b, cold

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 2176.7 / 2267.8 (25) | 1562.9 / 1833.0 (25) |
| first_turn_ms | 53.8 / 60.1 (25) | 100.5 / 111.7 (25) |
| to_first_tool_ms | 28.1 / 34.1 (25) | 85.9 / 96.7 (25) |
| turn_ms | 53.8 / 60.1 (25) | 100.5 / 111.7 (25) |
| cpu_total_ms_end | 3540 / 3740 (25) | 2660 / 2900 (25) |
| cpu_total_workload_ms | 80.0 / 90.0 (25) | 120.0 / 130.0 (25) |
| cpu_ext_workload_ms | 50.0 / 70.0 (25) | 120.0 / 130.0 (25) |
| ext_cpu_ms_end | 2950 / 3160 (25) | 2660 / 2900 (25) |
| ext_rss_end | 317.11 MiB / 320.46 MiB (25) | 278.19 MiB / 281.89 MiB (25) |
| ext_pss_end | 313.13 MiB / 316.39 MiB (25) | 275.98 MiB / 279.73 MiB (25) |
| tree_rss_end | 480.90 MiB / 485.38 MiB (25) | 278.19 MiB / 281.89 MiB (25) |
| tree_pss_end | 475.41 MiB / 479.85 MiB (25) | 275.98 MiB / 279.73 MiB (25) |
| ext_peak_rss_end | 317.11 MiB / 320.46 MiB (25) | 278.19 MiB / 281.89 MiB (25) |
| ext_anon_end | 256.87 MiB / 260.14 MiB (25) | 219.77 MiB / 223.52 MiB (25) |
| ext_rss_ready | 311.98 MiB / 315.38 MiB (25) | 271.20 MiB / 274.60 MiB (25) |
| ext_pss_ready | 307.92 MiB / 311.30 MiB (25) | 269.12 MiB / 272.58 MiB (25) |
| heap_used_gc_end | 47.42 MiB / 47.43 MiB (25) | 36.77 MiB / 36.78 MiB (25) |
| heap_used_asis_end | 49.99 MiB / 50.00 MiB (25) | 40.83 MiB / 40.83 MiB (25) |
| heap_inuse_gc_end | 181.25 MiB / 181.75 MiB (25) | 169.25 MiB / 169.75 MiB (25) |
| heap_inuse_asis_end | 181.25 MiB / 181.75 MiB (25) | 169.50 MiB / 170.25 MiB (25) |
| sys_asis_end | 306.83 MiB / 309.00 MiB (25) | 270.72 MiB / 273.38 MiB (25) |
| sys_gc_end | 306.83 MiB / 309.00 MiB (25) | 271.29 MiB / 273.38 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) |
| num_gc_workload | 1 / 1 (25) | 1 / 1 (25) |
| tool_ms:get_context_remaining | 9.0 / 10.9 (25) | 4.5 / 4.7 (25) |

### Workload b, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 812.5 / 878.0 (25) | 430.2 / 477.7 (25) |
| first_turn_ms | 62.3 / 75.1 (25) | 91.9 / 96.9 (25) |
| to_first_tool_ms | 33.1 / 38.0 (25) | 75.3 / 78.6 (25) |
| turn_ms | 62.3 / 75.1 (25) | 91.9 / 96.9 (25) |
| cpu_total_ms_end | 1180 / 1300 (25) | 750 / 840 (25) |
| cpu_total_workload_ms | 80.0 / 120.0 (25) | 110.0 / 120.0 (25) |
| cpu_ext_workload_ms | 50.0 / 80.0 (25) | 110.0 / 120.0 (25) |
| ext_cpu_ms_end | 980 / 1070 (25) | 750 / 840 (25) |
| ext_rss_end | 167.31 MiB / 169.08 MiB (25) | 122.96 MiB / 123.71 MiB (25) |
| ext_pss_end | 163.33 MiB / 165.14 MiB (25) | 120.76 MiB / 121.56 MiB (25) |
| tree_rss_end | 257.10 MiB / 258.81 MiB (25) | 122.96 MiB / 123.71 MiB (25) |
| tree_pss_end | 251.59 MiB / 253.28 MiB (25) | 120.76 MiB / 121.56 MiB (25) |
| ext_peak_rss_end | 167.31 MiB / 169.08 MiB (25) | 122.96 MiB / 123.71 MiB (25) |
| ext_anon_end | 107.14 MiB / 108.89 MiB (25) | 64.80 MiB / 65.46 MiB (25) |
| ext_rss_ready | 160.88 MiB / 161.69 MiB (25) | 111.57 MiB / 114.25 MiB (25) |
| ext_pss_ready | 156.83 MiB / 157.72 MiB (25) | 109.49 MiB / 112.14 MiB (25) |
| heap_used_gc_end | 35.17 MiB / 35.17 MiB (25) | 24.52 MiB / 24.52 MiB (25) |
| heap_used_asis_end | 36.54 MiB / 36.55 MiB (25) | 27.33 MiB / 27.33 MiB (25) |
| heap_inuse_gc_end | 70.09 MiB / 101.84 MiB (25) | 43.10 MiB / 59.60 MiB (25) |
| heap_inuse_asis_end | 69.84 MiB / 101.59 MiB (25) | 42.85 MiB / 44.10 MiB (25) |
| sys_asis_end | 156.00 MiB / 161.62 MiB (25) | 113.16 MiB / 114.68 MiB (25) |
| sys_gc_end | 156.00 MiB / 161.62 MiB (25) | 113.18 MiB / 114.83 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) |
| num_gc_workload | 1 / 1 (25) | 1 / 1 (25) |
| tool_ms:get_context_remaining | 10.4 / 13.8 (25) | 4.6 / 5.1 (25) |

### Workload c1, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 845.4 / 880.3 (60) | 454.0 / 476.8 (60) |
| to_first_tool_ms | 33.8 / 37.3 (60) | 76.2 / 80.5 (60) |
| turn_ms | 682.0 / 825.2 (60) | 369.1 / 386.1 (60) |
| cpu_total_ms_end | 2015 / 2250 (60) | 1200 / 1280 (60) |
| cpu_total_workload_ms | 850.0 / 1060.0 (60) | 550.0 / 570.0 (60) |
| cpu_ext_workload_ms | 515.0 / 650.0 (60) | 550.0 / 570.0 (60) |
| ext_cpu_ms_end | 1490 / 1650 (60) | 1200 / 1280 (60) |
| ext_rss_end | 208.80 MiB / 209.95 MiB (60) | 155.10 MiB / 157.31 MiB (60) |
| ext_pss_end | 204.87 MiB / 206.02 MiB (60) | 152.97 MiB / 155.25 MiB (60) |
| tree_rss_end | 318.20 MiB / 323.78 MiB (60) | 155.10 MiB / 157.31 MiB (60) |
| tree_pss_end | 312.90 MiB / 318.61 MiB (60) | 152.97 MiB / 155.25 MiB (60) |
| ext_peak_rss_end | 208.80 MiB / 209.95 MiB (60) | 155.10 MiB / 157.31 MiB (60) |
| ext_anon_end | 148.63 MiB / 149.70 MiB (60) | 96.22 MiB / 98.46 MiB (60) |
| ext_rss_ready | 160.89 MiB / 161.93 MiB (60) | 111.80 MiB / 114.20 MiB (60) |
| ext_pss_ready | 156.94 MiB / 157.93 MiB (60) | 109.84 MiB / 112.30 MiB (60) |
| heap_used_gc_end | 35.89 MiB / 35.89 MiB (60) | 26.95 MiB / 26.96 MiB (60) |
| heap_used_asis_end | 60.36 MiB / 60.66 MiB (60) | 32.39 MiB / 32.46 MiB (60) |
| heap_inuse_gc_end | 102.59 MiB / 102.84 MiB (60) | 63.10 MiB / 63.60 MiB (60) |
| heap_inuse_asis_end | 107.27 MiB / 107.54 MiB (60) | 63.35 MiB / 64.10 MiB (60) |
| sys_asis_end | 188.18 MiB / 199.11 MiB (60) | 146.03 MiB / 148.50 MiB (60) |
| sys_gc_end | 184.81 MiB / 199.01 MiB (60) | 146.03 MiB / 148.65 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) | 31 / 31 (60) |
| num_gc_workload | 4 / 4 (60) | 6 / 6 (60) |
| tool_ms:notes.append | 7.1 / 10.7 (360) | 1.5 / 2.1 (360) |
| tool_ms:notes.list | 8.0 / 14.1 (360) | 3.7 / 11.1 (360) |
| tool_ms:notes.read | 7.2 / 10.4 (360) | 1.8 / 5.2 (360) |
| tool_ms:notes.search | 7.7 / 11.4 (360) | 4.4 / 5.6 (360) |
| tool_ms:notes.write | 9.1 / 13.0 (360) | 3.3 / 4.7 (360) |

### Workload c100, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 858.5 / 887.4 (60) | 460.8 / 478.5 (60) |
| to_first_tool_ms | 34.4 / 38.0 (60) | 78.4 / 81.0 (60) |
| turn_ms | 1108.2 / 1215.3 (60) | 561.4 / 598.8 (60) |
| cpu_total_ms_end | 2505 / 2710 (60) | 1415 / 1510 (60) |
| cpu_total_workload_ms | 1355.0 / 1500.0 (60) | 775.0 / 800.0 (60) |
| cpu_ext_workload_ms | 895.0 / 1020.0 (60) | 775.0 / 800.0 (60) |
| ext_cpu_ms_end | 1845 / 2030 (60) | 1415 / 1510 (60) |
| ext_rss_end | 224.54 MiB / 226.93 MiB (60) | 158.99 MiB / 160.80 MiB (60) |
| ext_pss_end | 220.61 MiB / 222.96 MiB (60) | 156.81 MiB / 158.65 MiB (60) |
| tree_rss_end | 334.80 MiB / 340.58 MiB (60) | 158.99 MiB / 160.80 MiB (60) |
| tree_pss_end | 329.27 MiB / 335.28 MiB (60) | 156.81 MiB / 158.65 MiB (60) |
| ext_peak_rss_end | 224.54 MiB / 226.93 MiB (60) | 158.99 MiB / 160.80 MiB (60) |
| ext_anon_end | 164.36 MiB / 166.60 MiB (60) | 100.14 MiB / 101.88 MiB (60) |
| ext_rss_ready | 160.97 MiB / 161.95 MiB (60) | 111.81 MiB / 114.29 MiB (60) |
| ext_pss_ready | 157.06 MiB / 157.97 MiB (60) | 109.90 MiB / 112.38 MiB (60) |
| heap_used_gc_end | 36.07 MiB / 36.09 MiB (60) | 27.15 MiB / 27.16 MiB (60) |
| heap_used_asis_end | 58.24 MiB / 64.77 MiB (60) | 34.81 MiB / 38.70 MiB (60) |
| heap_inuse_gc_end | 103.34 MiB / 104.84 MiB (60) | 64.85 MiB / 65.35 MiB (60) |
| heap_inuse_asis_end | 110.09 MiB / 113.60 MiB (60) | 65.50 MiB / 65.88 MiB (60) |
| sys_asis_end | 205.40 MiB / 217.65 MiB (60) | 149.70 MiB / 151.87 MiB (60) |
| sys_gc_end | 200.39 MiB / 211.52 MiB (60) | 149.70 MiB / 151.87 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) | 31 / 31 (60) |
| num_gc_workload | 6 / 6 (60) | 8 / 8 (60) |
| tool_ms:notes.append | 8.0 / 11.3 (360) | 1.5 / 2.2 (360) |
| tool_ms:notes.list | 21.4 / 28.8 (360) | 13.4 / 21.0 (360) |
| tool_ms:notes.read | 8.2 / 11.3 (360) | 1.8 / 3.1 (360) |
| tool_ms:notes.search | 31.2 / 41.8 (360) | 23.1 / 31.8 (360) |
| tool_ms:notes.write | 10.0 / 13.9 (360) | 3.2 / 5.0 (360) |

### Workload c1000, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 824.3 / 881.1 (60) | 437.4 / 483.6 (60) |
| to_first_tool_ms | 33.0 / 38.3 (60) | 74.5 / 79.9 (60) |
| turn_ms | 1645.1 / 2083.1 (60) | 1142.2 / 1410.8 (60) |
| cpu_total_ms_end | 3205 / 3920 (60) | 2295 / 2660 (60) |
| cpu_total_workload_ms | 2080.0 / 2710.0 (60) | 1600.0 / 1960.0 (60) |
| cpu_ext_workload_ms | 1670.0 / 2210.0 (60) | 1600.0 / 1960.0 (60) |
| ext_cpu_ms_end | 2610 / 3210 (60) | 2295 / 2660 (60) |
| ext_rss_end | 231.21 MiB / 233.26 MiB (60) | 167.76 MiB / 170.29 MiB (60) |
| ext_pss_end | 227.34 MiB / 229.33 MiB (60) | 165.67 MiB / 168.22 MiB (60) |
| tree_rss_end | 338.65 MiB / 346.00 MiB (60) | 167.76 MiB / 170.29 MiB (60) |
| tree_pss_end | 333.39 MiB / 340.69 MiB (60) | 165.67 MiB / 168.22 MiB (60) |
| ext_peak_rss_end | 231.21 MiB / 233.26 MiB (60) | 167.76 MiB / 170.29 MiB (60) |
| ext_anon_end | 170.95 MiB / 173.08 MiB (60) | 108.80 MiB / 111.38 MiB (60) |
| ext_rss_ready | 160.87 MiB / 161.84 MiB (60) | 111.67 MiB / 113.93 MiB (60) |
| ext_pss_ready | 156.84 MiB / 157.81 MiB (60) | 109.76 MiB / 111.98 MiB (60) |
| heap_used_gc_end | 36.16 MiB / 36.17 MiB (60) | 27.20 MiB / 27.21 MiB (60) |
| heap_used_asis_end | 53.80 MiB / 65.20 MiB (60) | 51.45 MiB / 51.88 MiB (60) |
| heap_inuse_gc_end | 104.09 MiB / 109.09 MiB (60) | 95.10 MiB / 95.85 MiB (60) |
| heap_inuse_asis_end | 113.24 MiB / 117.49 MiB (60) | 102.13 MiB / 103.13 MiB (60) |
| sys_asis_end | 219.95 MiB / 223.60 MiB (60) | 177.49 MiB / 180.39 MiB (60) |
| sys_gc_end | 217.96 MiB / 220.25 MiB (60) | 177.80 MiB / 180.39 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) | 31 / 31 (60) |
| num_gc_workload | 13 / 15 (60) | 19 / 20 (60) |
| tool_ms:notes.append | 6.8 / 10.6 (360) | 1.4 / 2.1 (360) |
| tool_ms:notes.list | 42.9 / 61.9 (360) | 33.2 / 48.4 (360) |
| tool_ms:notes.read | 7.3 / 11.4 (360) | 1.6 / 2.8 (360) |
| tool_ms:notes.search | 114.1 / 176.4 (360) | 99.0 / 155.4 (360) |
| tool_ms:notes.write | 9.0 / 13.2 (360) | 3.1 / 4.8 (360) |

### Workload d50, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 804.9 / 881.8 (60) | 429.7 / 480.1 (60) |
| to_first_tool_ms | 35.7 / 39.5 (60) | 76.5 / 82.2 (60) |
| turn_ms | 367.8 / 402.9 (60) | 349.5 / 380.1 (60) |
| cpu_total_ms_end | 1715 / 1870 (60) | 1250 / 1400 (60) |
| cpu_total_workload_ms | 610.0 / 670.0 (60) | 605.0 / 680.0 (60) |
| cpu_ext_workload_ms | 420.0 / 470.0 (60) | 480.0 / 530.0 (60) |
| ext_cpu_ms_end | 1330 / 1470 (60) | 1115 / 1250 (60) |
| ext_rss_end | 208.75 MiB / 210.27 MiB (60) | 168.01 MiB / 169.22 MiB (60) |
| ext_pss_end | 204.81 MiB / 206.42 MiB (60) | 165.87 MiB / 167.09 MiB (60) |
| tree_rss_end | 301.36 MiB / 303.11 MiB (60) | 168.01 MiB / 169.22 MiB (60) |
| tree_pss_end | 296.03 MiB / 297.63 MiB (60) | 165.87 MiB / 167.09 MiB (60) |
| ext_peak_rss_end | 208.75 MiB / 210.27 MiB (60) | 168.27 MiB / 169.22 MiB (60) |
| ext_anon_end | 148.40 MiB / 150.04 MiB (60) | 108.89 MiB / 110.18 MiB (60) |
| ext_rss_ready | 161.11 MiB / 161.89 MiB (60) | 111.81 MiB / 114.59 MiB (60) |
| ext_pss_ready | 157.12 MiB / 157.90 MiB (60) | 109.85 MiB / 112.67 MiB (60) |
| heap_used_gc_end | 36.26 MiB / 36.27 MiB (60) | 25.70 MiB / 25.71 MiB (60) |
| heap_used_asis_end | 60.11 MiB / 60.56 MiB (60) | 33.36 MiB / 44.55 MiB (60) |
| heap_inuse_gc_end | 104.09 MiB / 104.59 MiB (60) | 61.60 MiB / 63.10 MiB (60) |
| heap_inuse_asis_end | 105.36 MiB / 105.86 MiB (60) | 64.82 MiB / 66.82 MiB (60) |
| sys_asis_end | 208.38 MiB / 210.00 MiB (60) | 162.56 MiB / 167.00 MiB (60) |
| sys_gc_end | 208.38 MiB / 210.00 MiB (60) | 162.56 MiB / 167.00 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 4 / 4 (60) | 4 / 4 (60) |
| num_gc_workload | 5 / 5 (60) | 9 / 11 (60) |
| tool_ms:history.read | 34.1 / 39.0 (60) | 25.7 / 32.2 (60) |
| tool_ms:history.search:common | 30.5 / 36.7 (60) | 23.4 / 29.0 (60) |
| tool_ms:history.search:rare | 231.9 / 245.0 (60) | 188.6 / 212.9 (60) |

### Workload d200, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 785.5 / 879.4 (60) | 435.2 / 483.0 (60) |
| to_first_tool_ms | 32.5 / 38.7 (60) | 73.2 / 81.8 (60) |
| turn_ms | 465.5 / 529.7 (60) | 437.2 / 505.5 (60) |
| cpu_total_ms_end | 2000 / 2310 (60) | 1575 / 1820 (60) |
| cpu_total_workload_ms | 940.0 / 1100.0 (60) | 945.0 / 1110.0 (60) |
| cpu_ext_workload_ms | 455.0 / 540.0 (60) | 520.0 / 610.0 (60) |
| ext_cpu_ms_end | 1355 / 1560 (60) | 1160 / 1310 (60) |
| ext_rss_end | 212.39 MiB / 214.01 MiB (60) | 168.72 MiB / 170.13 MiB (60) |
| ext_pss_end | 208.41 MiB / 210.02 MiB (60) | 166.63 MiB / 167.96 MiB (60) |
| tree_rss_end | 305.02 MiB / 306.48 MiB (60) | 168.72 MiB / 170.13 MiB (60) |
| tree_pss_end | 299.67 MiB / 301.15 MiB (60) | 166.63 MiB / 167.96 MiB (60) |
| ext_peak_rss_end | 212.39 MiB / 214.01 MiB (60) | 168.72 MiB / 170.13 MiB (60) |
| ext_anon_end | 152.07 MiB / 153.75 MiB (60) | 109.69 MiB / 111.04 MiB (60) |
| ext_rss_ready | 161.16 MiB / 162.13 MiB (60) | 111.94 MiB / 114.50 MiB (60) |
| ext_pss_ready | 157.16 MiB / 158.18 MiB (60) | 109.95 MiB / 112.60 MiB (60) |
| heap_used_gc_end | 36.31 MiB / 36.32 MiB (60) | 25.76 MiB / 25.77 MiB (60) |
| heap_used_asis_end | 52.71 MiB / 53.06 MiB (60) | 37.06 MiB / 38.80 MiB (60) |
| heap_inuse_gc_end | 104.34 MiB / 104.84 MiB (60) | 61.60 MiB / 63.85 MiB (60) |
| heap_inuse_asis_end | 105.59 MiB / 106.09 MiB (60) | 65.29 MiB / 67.77 MiB (60) |
| sys_asis_end | 207.85 MiB / 210.00 MiB (60) | 163.76 MiB / 167.13 MiB (60) |
| sys_gc_end | 207.85 MiB / 210.00 MiB (60) | 163.69 MiB / 167.13 MiB (60) |
| ext_count | 1 / 1 (60) | 1 / 1 (60) |
| llm_requests | 4 / 4 (60) | 4 / 4 (60) |
| num_gc_workload | 7 / 7 (60) | 12 / 14 (60) |
| tool_ms:history.read | 54.5 / 66.0 (60) | 45.9 / 57.3 (60) |
| tool_ms:history.search:common | 41.3 / 49.9 (60) | 33.5 / 42.7 (60) |
| tool_ms:history.search:rare | 293.5 / 329.0 (60) | 258.8 / 296.9 (60) |

### Workload e, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 796.4 / 882.3 (25) | 435.1 / 482.1 (25) |
| to_first_tool_ms | 33.9 / 41.3 (25) | 75.7 / 82.9 (25) |
| turn_ms | 15996.7 / 16444.8 (25) | 2545.2 / 2668.4 (25) |
| cpu_total_ms_end | 19490 / 19910 (25) | 4200 / 4450 (25) |
| cpu_total_workload_ms | 18370.0 / 18830.0 (25) | 3570.0 / 3730.0 (25) |
| cpu_ext_workload_ms | 9300.0 / 9650.0 (25) | 3570.0 / 3730.0 (25) |
| ext_cpu_ms_end | 10230 / 10550 (25) | 4200 / 4450 (25) |
| ext_rss_end | 459.14 MiB / 487.18 MiB (25) | 310.57 MiB / 316.40 MiB (25) |
| ext_pss_end | 455.12 MiB / 483.25 MiB (25) | 308.32 MiB / 314.31 MiB (25) |
| tree_rss_end | 591.01 MiB / 619.77 MiB (25) | 310.57 MiB / 316.40 MiB (25) |
| tree_pss_end | 585.53 MiB / 614.21 MiB (25) | 308.32 MiB / 314.31 MiB (25) |
| ext_peak_rss_end | 516.88 MiB / 540.95 MiB (25) | 311.85 MiB / 316.96 MiB (25) |
| ext_anon_end | 398.77 MiB / 426.95 MiB (25) | 251.51 MiB / 257.46 MiB (25) |
| ext_rss_ready | 161.12 MiB / 161.66 MiB (25) | 111.50 MiB / 113.69 MiB (25) |
| ext_pss_ready | 157.14 MiB / 157.71 MiB (25) | 109.51 MiB / 111.62 MiB (25) |
| heap_used_gc_end | 35.93 MiB / 36.81 MiB (25) | 30.18 MiB / 30.22 MiB (25) |
| heap_used_asis_end | 48.24 MiB / 96.66 MiB (25) | 86.66 MiB / 104.08 MiB (25) |
| heap_inuse_gc_end | 168.84 MiB / 169.34 MiB (25) | 162.60 MiB / 174.35 MiB (25) |
| heap_inuse_asis_end | 174.49 MiB / 197.35 MiB (25) | 190.07 MiB / 206.20 MiB (25) |
| sys_asis_end | 446.05 MiB / 487.97 MiB (25) | 291.68 MiB / 314.69 MiB (25) |
| sys_gc_end | 426.42 MiB / 466.71 MiB (25) | 272.16 MiB / 292.32 MiB (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) |
| llm_requests | 200 / 200 (25) | 200 / 200 (25) |
| num_gc_workload | 41 / 57 (25) | 21 / 22 (25) |
| session_compaction | 3 / 3 (25) | 3 / 3 (25) |
| session_context_window | 0 / 0 (25) | 0 / 0 (25) |
| session_custom_message | 3 / 3 (25) | 3 / 3 (25) |
| session_entries | 531 / 531 (25) | 531 / 531 (25) |
| session_tool_results | 180 / 180 (25) | 180 / 180 (25) |
| tool_ms:get_context_remaining | 6.7 / 9.1 (750) | 0.5 / 0.7 (750) |
| tool_ms:notes.append | 9.1 / 12.1 (1500) | 1.3 / 1.6 (1500) |
| tool_ms:notes.write | 11.3 / 14.7 (1500) | 3.5 / 4.6 (1500) |
| tool_ms:read | 11.8 / 14.0 (750) | 3.8 / 4.8 (750) |

### Workload f, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| cpu_total_ms_end | 70830 / 72760 (25) | 42200 / 43180 (25) |
| ext_cpu_ms_end | 60270 / 62060 (25) | 42210 / 43180 (25) |
| ext_rss_end | 8093.95 MiB / 8132.78 MiB (25) | 6165.12 MiB / 6177.95 MiB (25) |
| ext_pss_end | 4814.08 MiB / 4852.40 MiB (25) | 3090.12 MiB / 3102.97 MiB (25) |
| tree_rss_end | 13149.36 MiB / 13182.81 MiB (25) | 6165.11 MiB / 6177.95 MiB (25) |
| tree_pss_end | 7461.77 MiB / 7495.47 MiB (25) | 3090.10 MiB / 3102.97 MiB (25) |
| ext_peak_rss_end | 9332.15 MiB / 9349.92 MiB (25) | 6389.39 MiB / 6405.65 MiB (25) |
| ext_anon_end | 4761.07 MiB / 4799.39 MiB (25) | 3037.91 MiB / 3050.75 MiB (25) |
| ext_avg_rss_end | 134.90 MiB / 135.55 MiB (25) | 102.75 MiB / 102.97 MiB (25) |
| ext_avg_pss_end | 80.23 MiB / 80.87 MiB (25) | 51.50 MiB / 51.72 MiB (25) |
| all_ready_ms | 47176.5 / 48278.1 (25) | 25532.0 / 25878.6 (25) |
| ext_count | 60 / 60 (25) | 60 / 60 (25) |

## Behaviour parity of the workloads (model requests, session records)

identical across sides.


## Relative spread (p90-p50)/p50 above 5% (go and ts sides, ratchet metrics)

- a/warm ts heap_inuse_gc_end: 45.5% (n=25, median 69.84 MiB)
- a/warm ts heap_inuse_asis_end: 44.9% (n=25, median 70.09 MiB)
- b/cold ts first_turn_ms: 11.7% (n=25, median 53.8)
- b/cold ts to_first_tool_ms: 21.3% (n=25, median 28.1)
- b/cold ts turn_ms: 11.7% (n=25, median 53.8)
- b/cold ts cpu_total_ms_end: 5.6% (n=25, median 3540)
- b/cold ts cpu_total_workload_ms: 12.5% (n=25, median 80.0)
- b/cold ts cpu_ext_workload_ms: 40.0% (n=25, median 50.0)
- b/cold ts ext_cpu_ms_end: 7.1% (n=25, median 2950)
- b/cold ts tool_ms:get_context_remaining: 21.2% (n=25, median 9.0)
- b/warm ts startup_ms: 8.1% (n=25, median 812.5)
- b/warm ts first_turn_ms: 20.7% (n=25, median 62.3)
- b/warm ts to_first_tool_ms: 14.8% (n=25, median 33.1)
- b/warm ts turn_ms: 20.7% (n=25, median 62.3)
- b/warm ts cpu_total_ms_end: 10.2% (n=25, median 1180)
- b/warm ts cpu_total_workload_ms: 50.0% (n=25, median 80.0)
- b/warm ts cpu_ext_workload_ms: 60.0% (n=25, median 50.0)
- b/warm ts ext_cpu_ms_end: 9.2% (n=25, median 980)
- b/warm ts heap_inuse_gc_end: 45.3% (n=25, median 70.09 MiB)
- b/warm ts heap_inuse_asis_end: 45.5% (n=25, median 69.84 MiB)
- b/warm ts tool_ms:get_context_remaining: 33.6% (n=25, median 10.4)
- c1/warm ts to_first_tool_ms: 10.5% (n=60, median 33.8)
- c1/warm ts turn_ms: 21.0% (n=60, median 682.0)
- c1/warm ts cpu_total_ms_end: 11.7% (n=60, median 2015)
- c1/warm ts cpu_total_workload_ms: 24.7% (n=60, median 850.0)
- c1/warm ts cpu_ext_workload_ms: 26.2% (n=60, median 515.0)
- c1/warm ts ext_cpu_ms_end: 10.7% (n=60, median 1490)
- c1/warm ts sys_asis_end: 5.8% (n=60, median 188.18 MiB)
- c1/warm ts sys_gc_end: 7.7% (n=60, median 184.81 MiB)
- c1/warm ts tool_ms:notes.append: 50.3% (n=360, median 7.1)
- c1/warm ts tool_ms:notes.list: 75.4% (n=360, median 8.0)
- c1/warm ts tool_ms:notes.read: 44.4% (n=360, median 7.2)
- c1/warm ts tool_ms:notes.search: 48.8% (n=360, median 7.7)
- c1/warm ts tool_ms:notes.write: 41.8% (n=360, median 9.1)
- c100/warm ts to_first_tool_ms: 10.3% (n=60, median 34.4)
- c100/warm ts turn_ms: 9.7% (n=60, median 1108.2)
- c100/warm ts cpu_total_ms_end: 8.2% (n=60, median 2505)
- c100/warm ts cpu_total_workload_ms: 10.7% (n=60, median 1355.0)
- c100/warm ts cpu_ext_workload_ms: 14.0% (n=60, median 895.0)
- c100/warm ts ext_cpu_ms_end: 10.0% (n=60, median 1845)
- c100/warm ts heap_used_asis_end: 11.2% (n=60, median 58.24 MiB)
- c100/warm ts sys_asis_end: 6.0% (n=60, median 205.40 MiB)
- c100/warm ts sys_gc_end: 5.6% (n=60, median 200.39 MiB)
- c100/warm ts tool_ms:notes.append: 40.9% (n=360, median 8.0)
- c100/warm ts tool_ms:notes.list: 34.5% (n=360, median 21.4)
- c100/warm ts tool_ms:notes.read: 38.0% (n=360, median 8.2)
- c100/warm ts tool_ms:notes.search: 34.0% (n=360, median 31.2)
- c100/warm ts tool_ms:notes.write: 39.8% (n=360, median 10.0)
- c1000/warm ts startup_ms: 6.9% (n=60, median 824.3)
- c1000/warm ts to_first_tool_ms: 16.1% (n=60, median 33.0)
- c1000/warm ts turn_ms: 26.6% (n=60, median 1645.1)
- c1000/warm ts cpu_total_ms_end: 22.3% (n=60, median 3205)
- c1000/warm ts cpu_total_workload_ms: 30.3% (n=60, median 2080.0)
- c1000/warm ts cpu_ext_workload_ms: 32.3% (n=60, median 1670.0)
- c1000/warm ts ext_cpu_ms_end: 23.0% (n=60, median 2610)
- c1000/warm ts heap_used_asis_end: 21.2% (n=60, median 53.80 MiB)
- c1000/warm ts tool_ms:notes.append: 55.0% (n=360, median 6.8)
- c1000/warm ts tool_ms:notes.list: 44.4% (n=360, median 42.9)
- c1000/warm ts tool_ms:notes.read: 54.6% (n=360, median 7.3)
- c1000/warm ts tool_ms:notes.search: 54.6% (n=360, median 114.1)
- c1000/warm ts tool_ms:notes.write: 46.1% (n=360, median 9.0)
- d50/warm ts startup_ms: 9.5% (n=60, median 804.9)
- d50/warm ts to_first_tool_ms: 10.6% (n=60, median 35.7)
- d50/warm ts turn_ms: 9.6% (n=60, median 367.8)
- d50/warm ts cpu_total_ms_end: 9.0% (n=60, median 1715)
- d50/warm ts cpu_total_workload_ms: 9.8% (n=60, median 610.0)
- d50/warm ts cpu_ext_workload_ms: 11.9% (n=60, median 420.0)
- d50/warm ts ext_cpu_ms_end: 10.5% (n=60, median 1330)
- d50/warm ts tool_ms:history.read: 14.3% (n=60, median 34.1)
- d50/warm ts tool_ms:history.search:common: 20.3% (n=60, median 30.5)
- d50/warm ts tool_ms:history.search:rare: 5.6% (n=60, median 231.9)
- d200/warm ts startup_ms: 11.9% (n=60, median 785.5)
- d200/warm ts to_first_tool_ms: 19.3% (n=60, median 32.5)
- d200/warm ts turn_ms: 13.8% (n=60, median 465.5)
- d200/warm ts cpu_total_ms_end: 15.5% (n=60, median 2000)
- d200/warm ts cpu_total_workload_ms: 17.0% (n=60, median 940.0)
- d200/warm ts cpu_ext_workload_ms: 18.7% (n=60, median 455.0)
- d200/warm ts ext_cpu_ms_end: 15.1% (n=60, median 1355)
- d200/warm ts tool_ms:history.read: 20.9% (n=60, median 54.5)
- d200/warm ts tool_ms:history.search:common: 20.8% (n=60, median 41.3)
- d200/warm ts tool_ms:history.search:rare: 12.1% (n=60, median 293.5)
- e/warm ts startup_ms: 10.8% (n=25, median 796.4)
- e/warm ts to_first_tool_ms: 21.7% (n=25, median 33.9)
- e/warm ts ext_rss_end: 6.1% (n=25, median 459.14 MiB)
- e/warm ts ext_pss_end: 6.2% (n=25, median 455.12 MiB)
- e/warm ts ext_anon_end: 7.1% (n=25, median 398.77 MiB)
- e/warm ts heap_used_asis_end: 100.4% (n=25, median 48.24 MiB)
- e/warm ts heap_inuse_asis_end: 13.1% (n=25, median 174.49 MiB)
- e/warm ts sys_asis_end: 9.4% (n=25, median 446.05 MiB)
- e/warm ts sys_gc_end: 9.4% (n=25, median 426.42 MiB)
- e/warm ts tool_ms:get_context_remaining: 34.8% (n=750, median 6.7)
- e/warm ts tool_ms:notes.append: 32.3% (n=1500, median 9.1)
- e/warm ts tool_ms:notes.write: 29.7% (n=1500, median 11.3)
- e/warm ts tool_ms:read: 19.0% (n=750, median 11.8)

