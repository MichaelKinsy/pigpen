# Posthorse perf summary

Medians and p90 over fresh-process samples; n in parentheses. Metric definitions: README.md. Every metric: summary-full.md.

### Workload a, cold

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 2167.4 / 2259.2 (15) | 1533.0 / 1593.3 (15) |
| cpu_total_ms_end | 3520 / 3640 (15) | 2560 / 2610 (15) |
| cpu_total_workload_ms | 0.0 / 0.0 (15) | 0.0 / 0.0 (15) |
| cpu_ext_workload_ms | 0.0 / 0.0 (15) | 0.0 / 0.0 (15) |
| ext_cpu_ms_end | 2940 / 3040 (15) | 2560 / 2610 (15) |
| ext_rss_end | 312.27 MiB / 314.13 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| ext_pss_end | 268.51 MiB / 274.21 MiB (15) | 231.66 MiB / 233.68 MiB (15) |
| tree_rss_end | 469.14 MiB / 472.75 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| tree_pss_end | 400.61 MiB / 427.13 MiB (15) | 231.66 MiB / 233.78 MiB (15) |
| ext_peak_rss_end | 312.27 MiB / 314.13 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| ext_anon_end | 256.52 MiB / 258.49 MiB (15) | 218.12 MiB / 220.99 MiB (15) |
| ext_rss_ready | 312.27 MiB / 314.13 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| ext_pss_ready | 268.51 MiB / 274.21 MiB (15) | 231.66 MiB / 233.78 MiB (15) |
| heap_used_gc_end | 47.18 MiB / 47.18 MiB (15) | 33.89 MiB / 33.89 MiB (15) |
| heap_used_asis_end | 48.61 MiB / 48.62 MiB (15) | 35.34 MiB / 35.35 MiB (15) |
| heap_inuse_gc_end | 181.25 MiB / 181.50 MiB (15) | 167.57 MiB / 168.32 MiB (15) |
| heap_inuse_asis_end | 181.25 MiB / 181.75 MiB (15) | 168.57 MiB / 169.07 MiB (15) |
| sys_asis_end | 301.08 MiB / 306.00 MiB (15) | 264.35 MiB / 265.82 MiB (15) |
| sys_gc_end | 301.08 MiB / 306.00 MiB (15) | 264.35 MiB / 265.82 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |

### Workload a, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 759.2 / 770.4 (15) | 408.6 / 413.9 (15) |
| cpu_total_ms_end | 1030 / 1040 (15) | 590 / 600 (15) |
| cpu_total_workload_ms | 0.0 / 0.0 (15) | 0.0 / 0.0 (15) |
| cpu_ext_workload_ms | 0.0 / 0.0 (15) | 0.0 / 0.0 (15) |
| ext_cpu_ms_end | 860 / 870 (15) | 590 / 600 (15) |
| ext_rss_end | 161.33 MiB / 162.38 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| ext_pss_end | 118.76 MiB / 121.14 MiB (15) | 70.95 MiB / 73.53 MiB (15) |
| tree_rss_end | 245.49 MiB / 246.80 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| tree_pss_end | 200.17 MiB / 203.95 MiB (15) | 70.96 MiB / 73.53 MiB (15) |
| ext_peak_rss_end | 161.33 MiB / 162.38 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| ext_anon_end | 105.75 MiB / 106.64 MiB (15) | 58.90 MiB / 59.47 MiB (15) |
| ext_rss_ready | 161.33 MiB / 162.38 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| ext_pss_ready | 118.79 MiB / 121.14 MiB (15) | 71.01 MiB / 73.53 MiB (15) |
| heap_used_gc_end | 34.94 MiB / 34.94 MiB (15) | 21.75 MiB / 21.76 MiB (15) |
| heap_used_asis_end | 35.17 MiB / 35.20 MiB (15) | 21.98 MiB / 21.99 MiB (15) |
| heap_inuse_gc_end | 101.34 MiB / 101.59 MiB (15) | 40.16 MiB / 40.66 MiB (15) |
| heap_inuse_asis_end | 101.34 MiB / 101.59 MiB (15) | 40.91 MiB / 41.41 MiB (15) |
| sys_asis_end | 152.91 MiB / 156.00 MiB (15) | 104.17 MiB / 106.90 MiB (15) |
| sys_gc_end | 152.91 MiB / 156.00 MiB (15) | 104.17 MiB / 106.90 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |

### Workload b, cold

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 2173.2 / 2188.8 (15) | 1537.4 / 1586.6 (15) |
| first_turn_ms | 55.5 / 61.9 (15) | 99.5 / 109.8 (15) |
| to_first_tool_ms | 29.9 / 34.0 (15) | 84.8 / 93.3 (15) |
| turn_ms | 55.5 / 61.9 (15) | 99.5 / 109.8 (15) |
| cpu_total_ms_end | 3580 / 3610 (15) | 2670 / 2710 (15) |
| cpu_total_workload_ms | 70.0 / 90.0 (15) | 110.0 / 130.0 (15) |
| cpu_ext_workload_ms | 50.0 / 60.0 (15) | 110.0 / 130.0 (15) |
| ext_cpu_ms_end | 2980 / 3030 (15) | 2670 / 2710 (15) |
| ext_rss_end | 316.61 MiB / 318.97 MiB (15) | 277.73 MiB / 278.40 MiB (15) |
| ext_pss_end | 274.11 MiB / 276.78 MiB (15) | 235.73 MiB / 241.66 MiB (15) |
| tree_rss_end | 465.84 MiB / 482.34 MiB (15) | 277.73 MiB / 278.40 MiB (15) |
| tree_pss_end | 415.95 MiB / 420.55 MiB (15) | 235.73 MiB / 241.67 MiB (15) |
| ext_peak_rss_end | 316.61 MiB / 318.97 MiB (15) | 277.73 MiB / 278.40 MiB (15) |
| ext_anon_end | 256.31 MiB / 258.41 MiB (15) | 219.36 MiB / 220.01 MiB (15) |
| ext_rss_ready | 311.57 MiB / 313.68 MiB (15) | 270.60 MiB / 271.28 MiB (15) |
| ext_pss_ready | 270.20 MiB / 272.93 MiB (15) | 229.37 MiB / 232.41 MiB (15) |
| heap_used_gc_end | 47.42 MiB / 47.43 MiB (15) | 36.77 MiB / 36.77 MiB (15) |
| heap_used_asis_end | 49.99 MiB / 49.99 MiB (15) | 40.83 MiB / 40.83 MiB (15) |
| heap_inuse_gc_end | 181.25 MiB / 181.75 MiB (15) | 169.25 MiB / 170.00 MiB (15) |
| heap_inuse_asis_end | 181.25 MiB / 181.75 MiB (15) | 169.50 MiB / 170.50 MiB (15) |
| sys_asis_end | 307.34 MiB / 309.66 MiB (15) | 270.70 MiB / 276.44 MiB (15) |
| sys_gc_end | 307.34 MiB / 309.66 MiB (15) | 271.01 MiB / 276.44 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 2 / 2 (15) | 2 / 2 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |
| tool_ms:get_context_remaining | 8.9 / 11.1 (15) | 4.4 / 4.8 (15) |

### Workload b, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 770.9 / 829.2 (15) | 413.8 / 427.7 (15) |
| first_turn_ms | 56.9 / 65.2 (15) | 86.2 / 92.9 (15) |
| to_first_tool_ms | 30.0 / 34.8 (15) | 70.8 / 77.0 (15) |
| turn_ms | 56.9 / 65.2 (15) | 86.2 / 92.9 (15) |
| cpu_total_ms_end | 1130 / 1170 (15) | 710 / 740 (15) |
| cpu_total_workload_ms | 80.0 / 90.0 (15) | 100.0 / 110.0 (15) |
| cpu_ext_workload_ms | 60.0 / 60.0 (15) | 100.0 / 110.0 (15) |
| ext_cpu_ms_end | 930 / 980 (15) | 710 / 740 (15) |
| ext_rss_end | 167.01 MiB / 168.44 MiB (15) | 123.53 MiB / 124.75 MiB (15) |
| ext_pss_end | 123.31 MiB / 126.02 MiB (15) | 79.42 MiB / 82.33 MiB (15) |
| tree_rss_end | 256.69 MiB / 257.90 MiB (15) | 123.53 MiB / 124.75 MiB (15) |
| tree_pss_end | 193.30 MiB / 210.16 MiB (15) | 79.42 MiB / 82.33 MiB (15) |
| ext_peak_rss_end | 167.01 MiB / 168.44 MiB (15) | 123.53 MiB / 124.75 MiB (15) |
| ext_anon_end | 106.81 MiB / 108.31 MiB (15) | 65.34 MiB / 66.32 MiB (15) |
| ext_rss_ready | 161.08 MiB / 162.68 MiB (15) | 112.00 MiB / 114.77 MiB (15) |
| ext_pss_ready | 117.83 MiB / 121.41 MiB (15) | 71.10 MiB / 72.97 MiB (15) |
| heap_used_gc_end | 35.17 MiB / 35.17 MiB (15) | 24.52 MiB / 24.52 MiB (15) |
| heap_used_asis_end | 36.54 MiB / 36.54 MiB (15) | 27.33 MiB / 27.33 MiB (15) |
| heap_inuse_gc_end | 70.09 MiB / 101.84 MiB (15) | 43.10 MiB / 43.35 MiB (15) |
| heap_inuse_asis_end | 70.34 MiB / 101.59 MiB (15) | 42.85 MiB / 42.85 MiB (15) |
| sys_asis_end | 158.10 MiB / 162.84 MiB (15) | 112.59 MiB / 113.18 MiB (15) |
| sys_gc_end | 158.10 MiB / 162.84 MiB (15) | 113.18 MiB / 114.46 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 2 / 2 (15) | 2 / 2 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |
| tool_ms:get_context_remaining | 9.5 / 11.3 (15) | 4.6 / 4.7 (15) |

### Workload c1, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 764.9 / 793.6 (15) | 409.7 / 414.4 (15) |
| to_first_tool_ms | 29.4 / 33.4 (15) | 70.8 / 73.7 (15) |
| turn_ms | 616.9 / 673.2 (15) | 310.6 / 350.0 (15) |
| cpu_total_ms_end | 1810 / 1890 (15) | 1040 / 1110 (15) |
| cpu_total_workload_ms | 780.0 / 860.0 (15) | 450.0 / 500.0 (15) |
| cpu_ext_workload_ms | 460.0 / 510.0 (15) | 450.0 / 500.0 (15) |
| ext_cpu_ms_end | 1320 / 1380 (15) | 1040 / 1110 (15) |
| ext_rss_end | 208.99 MiB / 209.81 MiB (15) | 154.74 MiB / 156.40 MiB (15) |
| ext_pss_end | 170.81 MiB / 185.97 MiB (15) | 117.73 MiB / 125.02 MiB (15) |
| tree_rss_end | 321.90 MiB / 325.33 MiB (15) | 154.74 MiB / 156.40 MiB (15) |
| tree_pss_end | 271.85 MiB / 285.24 MiB (15) | 117.73 MiB / 125.02 MiB (15) |
| ext_peak_rss_end | 208.99 MiB / 209.81 MiB (15) | 154.74 MiB / 156.40 MiB (15) |
| ext_anon_end | 148.79 MiB / 149.68 MiB (15) | 95.92 MiB / 97.43 MiB (15) |
| ext_rss_ready | 161.05 MiB / 161.88 MiB (15) | 111.52 MiB / 115.06 MiB (15) |
| ext_pss_ready | 125.59 MiB / 126.99 MiB (15) | 76.84 MiB / 78.97 MiB (15) |
| heap_used_gc_end | 35.89 MiB / 35.89 MiB (15) | 26.95 MiB / 26.96 MiB (15) |
| heap_used_asis_end | 60.58 MiB / 60.65 MiB (15) | 32.39 MiB / 36.71 MiB (15) |
| heap_inuse_gc_end | 102.59 MiB / 102.84 MiB (15) | 63.10 MiB / 63.60 MiB (15) |
| heap_inuse_asis_end | 107.27 MiB / 107.52 MiB (15) | 63.60 MiB / 63.85 MiB (15) |
| sys_asis_end | 187.49 MiB / 198.83 MiB (15) | 145.92 MiB / 148.14 MiB (15) |
| sys_gc_end | 183.81 MiB / 198.57 MiB (15) | 145.92 MiB / 148.14 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 31 / 31 (15) | 31 / 31 (15) |
| num_gc_workload | 4 / 4 (15) | 6 / 6 (15) |
| tool_ms:notes.append | 6.2 / 9.3 (90) | 1.4 / 1.9 (90) |
| tool_ms:notes.list | 6.6 / 11.7 (90) | 3.0 / 10.3 (90) |
| tool_ms:notes.read | 6.3 / 9.2 (90) | 1.5 / 4.6 (90) |
| tool_ms:notes.search | 6.4 / 9.4 (90) | 3.8 / 4.8 (90) |
| tool_ms:notes.write | 7.8 / 11.1 (90) | 2.9 / 4.0 (90) |

### Workload c100, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 764.9 / 785.6 (15) | 408.9 / 436.6 (15) |
| to_first_tool_ms | 30.3 / 32.3 (15) | 71.3 / 80.0 (15) |
| turn_ms | 903.9 / 955.4 (15) | 444.9 / 517.7 (15) |
| cpu_total_ms_end | 2110 / 2220 (15) | 1200 / 1340 (15) |
| cpu_total_workload_ms | 1080.0 / 1160.0 (15) | 590.0 / 690.0 (15) |
| cpu_ext_workload_ms | 700.0 / 740.0 (15) | 590.0 / 690.0 (15) |
| ext_cpu_ms_end | 1560 / 1610 (15) | 1200 / 1340 (15) |
| ext_rss_end | 224.95 MiB / 227.21 MiB (15) | 158.77 MiB / 160.25 MiB (15) |
| ext_pss_end | 181.12 MiB / 187.07 MiB (15) | 117.32 MiB / 120.06 MiB (15) |
| tree_rss_end | 334.02 MiB / 339.18 MiB (15) | 158.77 MiB / 160.25 MiB (15) |
| tree_pss_end | 277.14 MiB / 298.53 MiB (15) | 117.32 MiB / 120.06 MiB (15) |
| ext_peak_rss_end | 224.95 MiB / 227.21 MiB (15) | 158.77 MiB / 160.25 MiB (15) |
| ext_anon_end | 164.69 MiB / 166.88 MiB (15) | 99.97 MiB / 101.31 MiB (15) |
| ext_rss_ready | 161.41 MiB / 162.37 MiB (15) | 111.61 MiB / 112.28 MiB (15) |
| ext_pss_ready | 118.01 MiB / 121.48 MiB (15) | 70.15 MiB / 74.45 MiB (15) |
| heap_used_gc_end | 36.08 MiB / 36.09 MiB (15) | 27.15 MiB / 27.16 MiB (15) |
| heap_used_asis_end | 64.77 MiB / 64.77 MiB (15) | 34.75 MiB / 34.93 MiB (15) |
| heap_inuse_gc_end | 103.59 MiB / 104.59 MiB (15) | 65.10 MiB / 65.60 MiB (15) |
| heap_inuse_asis_end | 112.85 MiB / 113.35 MiB (15) | 65.63 MiB / 66.13 MiB (15) |
| sys_asis_end | 213.65 MiB / 217.27 MiB (15) | 149.31 MiB / 151.29 MiB (15) |
| sys_gc_end | 208.80 MiB / 212.14 MiB (15) | 149.31 MiB / 151.29 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 31 / 31 (15) | 31 / 31 (15) |
| num_gc_workload | 3 / 6 (15) | 8 / 8 (15) |
| tool_ms:notes.append | 6.2 / 8.7 (90) | 1.3 / 1.9 (90) |
| tool_ms:notes.list | 17.0 / 22.1 (90) | 11.0 / 18.5 (90) |
| tool_ms:notes.read | 6.3 / 8.2 (90) | 1.5 / 2.6 (90) |
| tool_ms:notes.search | 23.2 / 31.5 (90) | 17.9 / 24.3 (90) |
| tool_ms:notes.write | 7.9 / 10.5 (90) | 2.7 / 4.2 (90) |

### Workload c1000, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 766.4 / 775.5 (15) | 407.5 / 435.1 (15) |
| to_first_tool_ms | 28.7 / 31.8 (15) | 70.6 / 74.0 (15) |
| turn_ms | 1396.5 / 1464.4 (15) | 919.9 / 1125.6 (15) |
| cpu_total_ms_end | 2910 / 2960 (15) | 1990 / 2060 (15) |
| cpu_total_workload_ms | 1870.0 / 1930.0 (15) | 1390.0 / 1440.0 (15) |
| cpu_ext_workload_ms | 1490.0 / 1530.0 (15) | 1390.0 / 1440.0 (15) |
| ext_cpu_ms_end | 2360 / 2380 (15) | 1990 / 2060 (15) |
| ext_rss_end | 232.20 MiB / 233.25 MiB (15) | 163.76 MiB / 170.90 MiB (15) |
| ext_pss_end | 185.62 MiB / 187.27 MiB (15) | 120.84 MiB / 125.47 MiB (15) |
| tree_rss_end | 340.08 MiB / 348.51 MiB (15) | 163.76 MiB / 170.90 MiB (15) |
| tree_pss_end | 272.33 MiB / 292.47 MiB (15) | 120.84 MiB / 125.47 MiB (15) |
| ext_peak_rss_end | 232.20 MiB / 233.25 MiB (15) | 163.76 MiB / 170.90 MiB (15) |
| ext_anon_end | 171.88 MiB / 173.02 MiB (15) | 104.88 MiB / 112.04 MiB (15) |
| ext_rss_ready | 161.21 MiB / 161.64 MiB (15) | 111.68 MiB / 112.61 MiB (15) |
| ext_pss_ready | 116.21 MiB / 120.26 MiB (15) | 69.88 MiB / 73.49 MiB (15) |
| heap_used_gc_end | 36.16 MiB / 36.18 MiB (15) | 27.21 MiB / 27.24 MiB (15) |
| heap_used_asis_end | 52.66 MiB / 64.26 MiB (15) | 51.36 MiB / 51.75 MiB (15) |
| heap_inuse_gc_end | 107.84 MiB / 108.84 MiB (15) | 94.60 MiB / 95.35 MiB (15) |
| heap_inuse_asis_end | 113.92 MiB / 115.24 MiB (15) | 102.13 MiB / 102.88 MiB (15) |
| sys_asis_end | 219.52 MiB / 223.37 MiB (15) | 166.81 MiB / 179.75 MiB (15) |
| sys_gc_end | 216.39 MiB / 220.39 MiB (15) | 166.81 MiB / 179.75 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 31 / 31 (15) | 31 / 31 (15) |
| num_gc_workload | 13 / 15 (15) | 19 / 20 (15) |
| tool_ms:notes.append | 6.5 / 8.4 (90) | 1.3 / 1.9 (90) |
| tool_ms:notes.list | 37.0 / 47.7 (90) | 28.9 / 42.8 (90) |
| tool_ms:notes.read | 6.1 / 8.4 (90) | 1.5 / 2.6 (90) |
| tool_ms:notes.search | 83.6 / 125.4 (90) | 75.8 / 122.0 (90) |
| tool_ms:notes.write | 7.9 / 11.3 (90) | 2.8 / 4.4 (90) |

### Workload d50, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 765.4 / 773.0 (15) | 406.6 / 425.6 (15) |
| to_first_tool_ms | 32.8 / 36.3 (15) | 71.3 / 78.9 (15) |
| turn_ms | 326.9 / 337.8 (15) | 317.7 / 334.5 (15) |
| cpu_total_ms_end | 1570 / 1600 (15) | 1160 / 1210 (15) |
| cpu_total_workload_ms | 540.0 / 550.0 (15) | 570.0 / 590.0 (15) |
| cpu_ext_workload_ms | 370.0 / 380.0 (15) | 450.0 / 450.0 (15) |
| ext_cpu_ms_end | 1230 / 1260 (15) | 1040 / 1080 (15) |
| ext_rss_end | 208.56 MiB / 209.39 MiB (15) | 168.05 MiB / 169.25 MiB (15) |
| ext_pss_end | 161.24 MiB / 164.25 MiB (15) | 123.17 MiB / 125.88 MiB (15) |
| tree_rss_end | 300.95 MiB / 302.32 MiB (15) | 168.05 MiB / 169.25 MiB (15) |
| tree_pss_end | 249.02 MiB / 253.43 MiB (15) | 123.20 MiB / 125.88 MiB (15) |
| ext_peak_rss_end | 208.56 MiB / 209.39 MiB (15) | 168.05 MiB / 169.25 MiB (15) |
| ext_anon_end | 148.37 MiB / 148.98 MiB (15) | 108.79 MiB / 110.21 MiB (15) |
| ext_rss_ready | 161.01 MiB / 161.75 MiB (15) | 111.66 MiB / 112.49 MiB (15) |
| ext_pss_ready | 115.17 MiB / 118.87 MiB (15) | 68.66 MiB / 70.25 MiB (15) |
| heap_used_gc_end | 36.27 MiB / 36.27 MiB (15) | 25.70 MiB / 25.71 MiB (15) |
| heap_used_asis_end | 60.10 MiB / 60.33 MiB (15) | 33.34 MiB / 33.38 MiB (15) |
| heap_inuse_gc_end | 104.09 MiB / 104.84 MiB (15) | 61.35 MiB / 62.10 MiB (15) |
| heap_inuse_asis_end | 105.11 MiB / 105.36 MiB (15) | 64.57 MiB / 64.82 MiB (15) |
| sys_asis_end | 207.94 MiB / 209.34 MiB (15) | 161.21 MiB / 163.83 MiB (15) |
| sys_gc_end | 207.94 MiB / 209.34 MiB (15) | 161.21 MiB / 163.83 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 4 / 4 (15) | 4 / 4 (15) |
| num_gc_workload | 5 / 5 (15) | 9 / 11 (15) |
| tool_ms:history.read | 27.2 / 28.7 (15) | 23.9 / 25.6 (15) |
| tool_ms:history.search:common | 25.7 / 28.0 (15) | 21.5 / 22.9 (15) |
| tool_ms:history.search:rare | 200.4 / 213.4 (15) | 176.2 / 190.7 (15) |

### Workload d200, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 760.7 / 828.8 (15) | 408.6 / 418.4 (15) |
| to_first_tool_ms | 32.2 / 38.5 (15) | 71.3 / 72.9 (15) |
| turn_ms | 430.6 / 500.6 (15) | 423.1 / 429.8 (15) |
| cpu_total_ms_end | 1910 / 1990 (15) | 1520 / 1530 (15) |
| cpu_total_workload_ms | 890.0 / 960.0 (15) | 920.0 / 940.0 (15) |
| cpu_ext_workload_ms | 430.0 / 490.0 (15) | 510.0 / 520.0 (15) |
| ext_cpu_ms_end | 1290 / 1350 (15) | 1100 / 1120 (15) |
| ext_rss_end | 212.49 MiB / 214.81 MiB (15) | 156.34 MiB / 170.29 MiB (15) |
| ext_pss_end | 161.96 MiB / 168.09 MiB (15) | 111.62 MiB / 124.80 MiB (15) |
| tree_rss_end | 305.20 MiB / 307.68 MiB (15) | 156.34 MiB / 170.29 MiB (15) |
| tree_pss_end | 240.08 MiB / 254.58 MiB (15) | 111.62 MiB / 124.80 MiB (15) |
| ext_peak_rss_end | 212.49 MiB / 214.81 MiB (15) | 168.85 MiB / 170.29 MiB (15) |
| ext_anon_end | 152.23 MiB / 154.52 MiB (15) | 97.33 MiB / 111.23 MiB (15) |
| ext_rss_ready | 161.43 MiB / 162.14 MiB (15) | 111.71 MiB / 114.55 MiB (15) |
| ext_pss_ready | 113.72 MiB / 116.05 MiB (15) | 66.60 MiB / 69.32 MiB (15) |
| heap_used_gc_end | 36.31 MiB / 36.31 MiB (15) | 25.75 MiB / 25.77 MiB (15) |
| heap_used_asis_end | 52.53 MiB / 52.81 MiB (15) | 26.59 MiB / 38.80 MiB (15) |
| heap_inuse_gc_end | 104.09 MiB / 104.34 MiB (15) | 60.85 MiB / 62.85 MiB (15) |
| heap_inuse_asis_end | 105.34 MiB / 105.84 MiB (15) | 62.10 MiB / 67.27 MiB (15) |
| sys_asis_end | 207.81 MiB / 211.50 MiB (15) | 163.90 MiB / 166.07 MiB (15) |
| sys_gc_end | 207.81 MiB / 211.50 MiB (15) | 163.90 MiB / 166.07 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 4 / 4 (15) | 4 / 4 (15) |
| num_gc_workload | 7 / 7 (15) | 14 / 14 (15) |
| tool_ms:history.read | 48.6 / 55.5 (15) | 43.7 / 45.1 (15) |
| tool_ms:history.search:common | 36.6 / 40.8 (15) | 32.2 / 34.1 (15) |
| tool_ms:history.search:rare | 275.2 / 322.8 (15) | 250.3 / 260.1 (15) |

### Workload e, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| startup_ms | 765.5 / 775.9 (15) | 408.4 / 421.0 (15) |
| to_first_tool_ms | 29.9 / 37.8 (15) | 71.8 / 73.5 (15) |
| turn_ms | 14948.2 / 15096.9 (15) | 2180.3 / 2230.1 (15) |
| cpu_total_ms_end | 18130 / 18610 (15) | 3630 / 3700 (15) |
| cpu_total_workload_ms | 17090.0 / 17580.0 (15) | 3030.0 / 3110.0 (15) |
| cpu_ext_workload_ms | 8540.0 / 8960.0 (15) | 3030.0 / 3110.0 (15) |
| ext_cpu_ms_end | 9390 / 9820 (15) | 3630 / 3700 (15) |
| ext_rss_end | 416.06 MiB / 472.58 MiB (15) | 315.89 MiB / 317.00 MiB (15) |
| ext_pss_end | 364.01 MiB / 425.09 MiB (15) | 269.41 MiB / 277.21 MiB (15) |
| tree_rss_end | 544.31 MiB / 605.12 MiB (15) | 315.89 MiB / 317.00 MiB (15) |
| tree_pss_end | 468.00 MiB / 535.17 MiB (15) | 269.55 MiB / 277.23 MiB (15) |
| ext_peak_rss_end | 498.54 MiB / 518.14 MiB (15) | 315.89 MiB / 317.00 MiB (15) |
| ext_anon_end | 355.78 MiB / 412.39 MiB (15) | 256.81 MiB / 258.08 MiB (15) |
| ext_rss_ready | 161.27 MiB / 162.00 MiB (15) | 111.43 MiB / 112.16 MiB (15) |
| ext_pss_ready | 114.74 MiB / 120.28 MiB (15) | 69.71 MiB / 77.07 MiB (15) |
| heap_used_gc_end | 35.91 MiB / 35.93 MiB (15) | 30.14 MiB / 30.20 MiB (15) |
| heap_used_asis_end | 47.34 MiB / 55.66 MiB (15) | 62.70 MiB / 95.98 MiB (15) |
| heap_inuse_gc_end | 168.84 MiB / 169.34 MiB (15) | 162.10 MiB / 174.85 MiB (15) |
| heap_inuse_asis_end | 174.24 MiB / 177.58 MiB (15) | 188.93 MiB / 203.34 MiB (15) |
| sys_asis_end | 412.89 MiB / 423.44 MiB (15) | 293.41 MiB / 296.86 MiB (15) |
| sys_gc_end | 406.11 MiB / 415.29 MiB (15) | 275.79 MiB / 278.57 MiB (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| llm_requests | 200 / 200 (15) | 200 / 200 (15) |
| num_gc_workload | 58 / 61 (15) | 22 / 23 (15) |
| session_compaction | 3 / 3 (15) | 3 / 3 (15) |
| session_context_window | 0 / 0 (15) | 0 / 0 (15) |
| session_custom_message | 3 / 3 (15) | 3 / 3 (15) |
| session_entries | 531 / 531 (15) | 531 / 531 (15) |
| session_tool_results | 180 / 180 (15) | 180 / 180 (15) |
| tool_ms:get_context_remaining | 5.8 / 8.1 (450) | 0.4 / 0.6 (450) |
| tool_ms:notes.append | 8.2 / 10.9 (900) | 1.1 / 1.5 (900) |
| tool_ms:notes.write | 10.1 / 13.1 (900) | 3.0 / 4.0 (900) |
| tool_ms:read | 10.9 / 13.0 (450) | 3.3 / 4.2 (450) |

### Workload f, warm

| Metric | ts median / p90 (n) | pi median / p90 (n) |
|---|---:|---:|
| cpu_total_ms_end | 68170 / 69040 (15) | 40740 / 40980 (15) |
| ext_cpu_ms_end | 58000 / 58730 (15) | 40740 / 40980 (15) |
| ext_rss_end | 8104.57 MiB / 8143.22 MiB (15) | 6165.25 MiB / 6173.42 MiB (15) |
| ext_pss_end | 4823.92 MiB / 4860.11 MiB (15) | 3084.85 MiB / 3096.39 MiB (15) |
| tree_rss_end | 13156.59 MiB / 13197.94 MiB (15) | 6165.25 MiB / 6173.42 MiB (15) |
| tree_pss_end | 7466.56 MiB / 7501.35 MiB (15) | 3084.85 MiB / 3096.39 MiB (15) |
| ext_peak_rss_end | 9336.89 MiB / 9352.98 MiB (15) | 6389.30 MiB / 6401.93 MiB (15) |
| ext_anon_end | 4772.55 MiB / 4810.62 MiB (15) | 3037.86 MiB / 3046.13 MiB (15) |
| ext_avg_rss_end | 135.08 MiB / 135.72 MiB (15) | 102.75 MiB / 102.89 MiB (15) |
| ext_avg_pss_end | 80.40 MiB / 81.00 MiB (15) | 51.41 MiB / 51.61 MiB (15) |
| all_ready_ms | 46051.9 / 46228.0 (15) | 24874.5 / 24924.9 (15) |
| ext_count | 60 / 60 (15) | 60 / 60 (15) |

## Behaviour parity of the workloads (model requests, session records)

identical across sides.

