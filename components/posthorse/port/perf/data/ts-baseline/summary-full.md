# Posthorse perf summary (every metric)

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
| cpu_total_ms_ready | 3520 / 3640 (15) | 2560 / 2610 (15) |
| cpu_wait4_ms | 592.3 / 624.2 (15) | 2599.6 / 2654.6 (15) |
| ext_anon_ready | 256.52 MiB / 258.49 MiB (15) | 218.12 MiB / 220.99 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 2940 / 3040 (15) | 2560 / 2610 (15) |
| ext_peak_rss_ready | 312.27 MiB / 314.13 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 193.00 MiB / 195.00 MiB (15) | 187.07 MiB / 188.32 MiB (15) |
| heap_inuse_gc_ready | 181.25 MiB / 181.75 MiB (15) | 168.57 MiB / 169.07 MiB (15) |
| heap_used_asis_ready | 49.83 MiB / 49.93 MiB (15) | 37.65 MiB / 38.39 MiB (15) |
| heap_used_gc_ready | 48.56 MiB / 48.57 MiB (15) | 35.29 MiB / 35.30 MiB (15) |
| host_anon_end | 112.80 MiB / 113.87 MiB (15) | 218.12 MiB / 220.99 MiB (15) |
| host_anon_ready | 112.80 MiB / 113.87 MiB (15) | 218.12 MiB / 220.99 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 560 / 590 (15) | 2560 / 2610 (15) |
| host_cpu_ms_ready | 560 / 590 (15) | 2560 / 2610 (15) |
| host_peak_rss_end | 159.14 MiB / 160.24 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| host_peak_rss_ready | 159.14 MiB / 160.24 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| host_pss_end | 135.41 MiB / 157.69 MiB (15) | 231.66 MiB / 233.78 MiB (15) |
| host_pss_ready | 135.41 MiB / 157.69 MiB (15) | 231.66 MiB / 233.78 MiB (15) |
| host_rss_end | 159.14 MiB / 160.24 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| host_rss_ready | 159.14 MiB / 160.24 MiB (15) | 270.48 MiB / 273.40 MiB (15) |
| host_threads_end | 12 / 13 (15) | 11 / 11 (15) |
| host_threads_ready | 12 / 13 (15) | 11 / 11 (15) |
| num_gc_end | 11 / 11 (15) | 10 / 10 (15) |
| num_gc_ready | 10 / 10 (15) | 9 / 9 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |
| sys_asis_ready | 301.08 MiB / 306.00 MiB (15) | 264.35 MiB / 265.82 MiB (15) |
| sys_gc_ready | 301.08 MiB / 306.00 MiB (15) | 264.35 MiB / 265.82 MiB (15) |
| tree_pss_ready | 400.61 MiB / 427.45 MiB (15) | 231.66 MiB / 233.78 MiB (15) |
| tree_rss_ready | 469.14 MiB / 472.75 MiB (15) | 270.48 MiB / 273.40 MiB (15) |

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
| cpu_total_ms_ready | 1030 / 1040 (15) | 590 / 600 (15) |
| cpu_wait4_ms | 194.0 / 200.7 (15) | 614.4 / 621.6 (15) |
| ext_anon_ready | 105.75 MiB / 106.64 MiB (15) | 58.90 MiB / 59.47 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 860 / 870 (15) | 590 / 600 (15) |
| ext_peak_rss_ready | 161.33 MiB / 162.38 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 66.09 MiB / 98.34 MiB (15) | 40.41 MiB / 40.66 MiB (15) |
| heap_inuse_gc_ready | 101.34 MiB / 101.59 MiB (15) | 40.91 MiB / 41.41 MiB (15) |
| heap_used_asis_ready | 36.96 MiB / 41.58 MiB (15) | 25.65 MiB / 25.68 MiB (15) |
| heap_used_gc_ready | 35.12 MiB / 35.15 MiB (15) | 21.93 MiB / 21.94 MiB (15) |
| host_anon_end | 43.29 MiB / 43.71 MiB (15) | 58.90 MiB / 59.47 MiB (15) |
| host_anon_ready | 43.29 MiB / 43.71 MiB (15) | 58.90 MiB / 59.47 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 170 / 180 (15) | 590 / 600 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 590 / 600 (15) |
| host_peak_rss_end | 84.04 MiB / 84.35 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| host_peak_rss_ready | 84.04 MiB / 84.35 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| host_pss_end | 82.31 MiB / 82.81 MiB (15) | 70.96 MiB / 73.53 MiB (15) |
| host_pss_ready | 82.31 MiB / 82.81 MiB (15) | 71.06 MiB / 73.53 MiB (15) |
| host_rss_end | 84.04 MiB / 84.35 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| host_rss_ready | 84.04 MiB / 84.35 MiB (15) | 111.00 MiB / 111.54 MiB (15) |
| host_threads_end | 7 / 7 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| num_gc_end | 8 / 8 (15) | 2 / 2 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |
| sys_asis_ready | 154.50 MiB / 156.00 MiB (15) | 104.07 MiB / 106.90 MiB (15) |
| sys_gc_ready | 152.91 MiB / 156.00 MiB (15) | 104.17 MiB / 106.90 MiB (15) |
| tree_pss_ready | 200.17 MiB / 203.95 MiB (15) | 71.06 MiB / 73.53 MiB (15) |
| tree_rss_ready | 245.49 MiB / 246.80 MiB (15) | 111.00 MiB / 111.54 MiB (15) |

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
| cpu_total_ms_ready | 3490 / 3540 (15) | 2560 / 2580 (15) |
| cpu_wait4_ms | 621.2 / 637.5 (15) | 2713.1 / 2749.3 (15) |
| ext_anon_ready | 255.98 MiB / 257.82 MiB (15) | 218.20 MiB / 218.88 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 2920 / 2970 (15) | 2560 / 2580 (15) |
| ext_peak_rss_ready | 311.57 MiB / 313.68 MiB (15) | 270.60 MiB / 271.28 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 194.25 MiB / 196.00 MiB (15) | 186.82 MiB / 188.82 MiB (15) |
| heap_inuse_gc_ready | 181.00 MiB / 181.75 MiB (15) | 169.07 MiB / 169.82 MiB (15) |
| heap_used_asis_ready | 49.86 MiB / 49.97 MiB (15) | 38.18 MiB / 38.45 MiB (15) |
| heap_used_gc_ready | 48.58 MiB / 48.58 MiB (15) | 35.41 MiB / 35.42 MiB (15) |
| host_anon_end | 101.20 MiB / 118.28 MiB (15) | 219.36 MiB / 220.01 MiB (15) |
| host_anon_ready | 101.00 MiB / 114.39 MiB (15) | 218.20 MiB / 218.88 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 590 / 610 (15) | 2670 / 2710 (15) |
| host_cpu_ms_ready | 560 / 590 (15) | 2560 / 2580 (15) |
| host_peak_rss_end | 148.94 MiB / 165.90 MiB (15) | 277.73 MiB / 278.40 MiB (15) |
| host_peak_rss_ready | 147.54 MiB / 160.99 MiB (15) | 270.60 MiB / 271.28 MiB (15) |
| host_pss_end | 140.65 MiB / 147.30 MiB (15) | 235.73 MiB / 241.67 MiB (15) |
| host_pss_ready | 136.26 MiB / 146.05 MiB (15) | 229.37 MiB / 232.41 MiB (15) |
| host_rss_end | 148.94 MiB / 165.90 MiB (15) | 277.73 MiB / 278.40 MiB (15) |
| host_rss_ready | 147.54 MiB / 160.99 MiB (15) | 270.60 MiB / 271.28 MiB (15) |
| host_threads_end | 12 / 13 (15) | 11 / 11 (15) |
| host_threads_ready | 12 / 13 (15) | 11 / 11 (15) |
| llm_requests | 2 / 2 (15) | 2 / 2 (15) |
| num_gc_end | 11 / 11 (15) | 10 / 10 (15) |
| num_gc_ready | 10 / 10 (15) | 9 / 9 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |
| sys_asis_ready | 301.22 MiB / 303.00 MiB (15) | 264.70 MiB / 270.44 MiB (15) |
| sys_gc_ready | 301.22 MiB / 303.00 MiB (15) | 264.70 MiB / 270.44 MiB (15) |
| tool_ms:get_context_remaining | 8.9 / 11.1 (15) | 4.4 / 4.8 (15) |
| tree_pss_ready | 406.30 MiB / 418.28 MiB (15) | 229.37 MiB / 232.41 MiB (15) |
| tree_rss_ready | 459.48 MiB / 472.37 MiB (15) | 270.60 MiB / 271.28 MiB (15) |

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
| cpu_total_ms_ready | 1040 / 1120 (15) | 610 / 630 (15) |
| cpu_wait4_ms | 224.9 / 231.0 (15) | 731.0 / 759.0 (15) |
| ext_anon_ready | 105.45 MiB / 107.20 MiB (15) | 59.91 MiB / 62.57 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 870 / 950 (15) | 610 / 630 (15) |
| ext_peak_rss_ready | 161.08 MiB / 162.68 MiB (15) | 112.00 MiB / 114.77 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 63.59 MiB / 98.59 MiB (15) | 40.41 MiB / 40.66 MiB (15) |
| heap_inuse_gc_ready | 70.34 MiB / 101.59 MiB (15) | 40.91 MiB / 41.41 MiB (15) |
| heap_used_asis_ready | 41.46 MiB / 41.59 MiB (15) | 25.66 MiB / 26.15 MiB (15) |
| heap_used_gc_ready | 35.15 MiB / 35.15 MiB (15) | 21.93 MiB / 21.94 MiB (15) |
| host_anon_end | 47.06 MiB / 47.21 MiB (15) | 65.34 MiB / 66.32 MiB (15) |
| host_anon_ready | 43.13 MiB / 43.28 MiB (15) | 59.91 MiB / 62.57 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 200 / 200 (15) | 710 / 740 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 610 / 630 (15) |
| host_peak_rss_end | 89.48 MiB / 89.88 MiB (15) | 123.53 MiB / 124.75 MiB (15) |
| host_peak_rss_ready | 84.05 MiB / 84.34 MiB (15) | 112.00 MiB / 114.77 MiB (15) |
| host_pss_end | 69.90 MiB / 87.95 MiB (15) | 79.42 MiB / 82.33 MiB (15) |
| host_pss_ready | 63.61 MiB / 82.22 MiB (15) | 71.12 MiB / 72.97 MiB (15) |
| host_rss_end | 89.48 MiB / 89.88 MiB (15) | 123.53 MiB / 124.75 MiB (15) |
| host_rss_ready | 84.05 MiB / 84.34 MiB (15) | 112.00 MiB / 114.77 MiB (15) |
| host_threads_end | 8 / 8 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 2 / 2 (15) | 2 / 2 (15) |
| num_gc_end | 8 / 8 (15) | 2 / 2 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 1 / 1 (15) | 1 / 1 (15) |
| sys_asis_ready | 151.50 MiB / 156.00 MiB (15) | 104.78 MiB / 105.68 MiB (15) |
| sys_gc_ready | 150.60 MiB / 155.64 MiB (15) | 104.78 MiB / 105.68 MiB (15) |
| tool_ms:get_context_remaining | 9.5 / 11.3 (15) | 4.6 / 4.7 (15) |
| tree_pss_ready | 182.44 MiB / 199.61 MiB (15) | 71.12 MiB / 72.97 MiB (15) |
| tree_rss_ready | 244.91 MiB / 246.55 MiB (15) | 112.00 MiB / 114.77 MiB (15) |

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
| cpu_total_ms_ready | 1030 / 1080 (15) | 600 / 610 (15) |
| cpu_wait4_ms | 513.6 / 541.8 (15) | 1064.6 / 1130.0 (15) |
| ext_anon_ready | 105.56 MiB / 106.30 MiB (15) | 59.37 MiB / 62.91 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 860 / 910 (15) | 600 / 610 (15) |
| ext_peak_rss_ready | 161.05 MiB / 161.88 MiB (15) | 111.52 MiB / 115.06 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 63.34 MiB / 98.84 MiB (15) | 40.41 MiB / 42.16 MiB (15) |
| heap_inuse_gc_ready | 69.84 MiB / 101.34 MiB (15) | 40.91 MiB / 42.16 MiB (15) |
| heap_used_asis_ready | 41.39 MiB / 41.59 MiB (15) | 26.12 MiB / 31.29 MiB (15) |
| heap_used_gc_ready | 35.15 MiB / 35.16 MiB (15) | 21.94 MiB / 21.94 MiB (15) |
| host_anon_end | 70.95 MiB / 72.74 MiB (15) | 95.92 MiB / 97.43 MiB (15) |
| host_anon_ready | 43.30 MiB / 43.70 MiB (15) | 59.37 MiB / 62.91 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 480 / 510 (15) | 1040 / 1110 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 600 / 610 (15) |
| host_peak_rss_end | 113.50 MiB / 115.51 MiB (15) | 154.74 MiB / 156.40 MiB (15) |
| host_peak_rss_ready | 84.07 MiB / 84.49 MiB (15) | 111.52 MiB / 115.06 MiB (15) |
| host_pss_end | 93.78 MiB / 112.49 MiB (15) | 117.73 MiB / 125.02 MiB (15) |
| host_pss_ready | 64.72 MiB / 82.74 MiB (15) | 76.84 MiB / 78.97 MiB (15) |
| host_rss_end | 113.50 MiB / 115.51 MiB (15) | 154.74 MiB / 156.40 MiB (15) |
| host_rss_ready | 84.07 MiB / 84.49 MiB (15) | 111.52 MiB / 115.06 MiB (15) |
| host_threads_end | 8 / 8 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 31 / 31 (15) | 31 / 31 (15) |
| num_gc_end | 11 / 11 (15) | 7 / 7 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 4 / 4 (15) | 6 / 6 (15) |
| sys_asis_ready | 151.50 MiB / 154.50 MiB (15) | 104.85 MiB / 107.16 MiB (15) |
| sys_gc_ready | 151.03 MiB / 154.27 MiB (15) | 104.86 MiB / 107.53 MiB (15) |
| tool_ms:notes.append | 6.2 / 9.3 (90) | 1.4 / 1.9 (90) |
| tool_ms:notes.list | 6.6 / 11.7 (90) | 3.0 / 10.3 (90) |
| tool_ms:notes.read | 6.3 / 9.2 (90) | 1.5 / 4.6 (90) |
| tool_ms:notes.search | 6.4 / 9.4 (90) | 3.8 / 4.8 (90) |
| tool_ms:notes.write | 7.8 / 11.1 (90) | 2.9 / 4.0 (90) |
| tree_pss_ready | 191.30 MiB / 208.44 MiB (15) | 76.84 MiB / 78.97 MiB (15) |
| tree_rss_ready | 245.51 MiB / 245.77 MiB (15) | 111.52 MiB / 115.06 MiB (15) |

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
| cpu_total_ms_ready | 1030 / 1060 (15) | 600 / 650 (15) |
| cpu_wait4_ms | 587.7 / 620.1 (15) | 1223.1 / 1369.5 (15) |
| ext_anon_ready | 105.89 MiB / 106.66 MiB (15) | 59.47 MiB / 60.23 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 860 / 880 (15) | 600 / 650 (15) |
| ext_peak_rss_ready | 161.41 MiB / 162.37 MiB (15) | 111.61 MiB / 112.28 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 97.84 MiB / 98.34 MiB (15) | 40.41 MiB / 42.41 MiB (15) |
| heap_inuse_gc_ready | 101.34 MiB / 101.59 MiB (15) | 41.16 MiB / 42.41 MiB (15) |
| heap_used_asis_ready | 36.90 MiB / 41.56 MiB (15) | 25.67 MiB / 31.31 MiB (15) |
| heap_used_gc_ready | 35.12 MiB / 35.15 MiB (15) | 21.94 MiB / 21.94 MiB (15) |
| host_anon_end | 65.58 MiB / 70.33 MiB (15) | 99.97 MiB / 101.31 MiB (15) |
| host_anon_ready | 43.29 MiB / 43.73 MiB (15) | 59.47 MiB / 60.23 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 560 / 590 (15) | 1200 / 1340 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 600 / 650 (15) |
| host_peak_rss_end | 108.30 MiB / 113.00 MiB (15) | 158.77 MiB / 160.25 MiB (15) |
| host_peak_rss_ready | 84.14 MiB / 84.57 MiB (15) | 111.61 MiB / 112.28 MiB (15) |
| host_pss_end | 91.47 MiB / 111.45 MiB (15) | 117.32 MiB / 120.06 MiB (15) |
| host_pss_ready | 64.50 MiB / 82.69 MiB (15) | 70.15 MiB / 74.45 MiB (15) |
| host_rss_end | 108.30 MiB / 113.00 MiB (15) | 158.77 MiB / 160.25 MiB (15) |
| host_rss_ready | 84.14 MiB / 84.57 MiB (15) | 111.61 MiB / 112.28 MiB (15) |
| host_threads_end | 8 / 8 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 31 / 31 (15) | 31 / 31 (15) |
| num_gc_end | 10 / 13 (15) | 9 / 9 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 3 / 6 (15) | 8 / 8 (15) |
| sys_asis_ready | 154.50 MiB / 157.50 MiB (15) | 105.45 MiB / 107.18 MiB (15) |
| sys_gc_ready | 152.37 MiB / 155.38 MiB (15) | 105.45 MiB / 107.18 MiB (15) |
| tool_ms:notes.append | 6.2 / 8.7 (90) | 1.3 / 1.9 (90) |
| tool_ms:notes.list | 17.0 / 22.1 (90) | 11.0 / 18.5 (90) |
| tool_ms:notes.read | 6.3 / 8.2 (90) | 1.5 / 2.6 (90) |
| tool_ms:notes.search | 23.2 / 31.5 (90) | 17.9 / 24.3 (90) |
| tool_ms:notes.write | 7.9 / 10.5 (90) | 2.7 / 4.2 (90) |
| tree_pss_ready | 185.98 MiB / 203.84 MiB (15) | 70.15 MiB / 74.45 MiB (15) |
| tree_rss_ready | 245.44 MiB / 246.58 MiB (15) | 111.61 MiB / 112.28 MiB (15) |

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
| cpu_total_ms_ready | 1030 / 1050 (15) | 590 / 630 (15) |
| cpu_wait4_ms | 582.9 / 603.9 (15) | 2013.3 / 2084.8 (15) |
| ext_anon_ready | 105.70 MiB / 106.19 MiB (15) | 59.66 MiB / 60.55 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 860 / 880 (15) | 590 / 630 (15) |
| ext_peak_rss_ready | 161.21 MiB / 161.64 MiB (15) | 111.68 MiB / 112.61 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 63.59 MiB / 98.34 MiB (15) | 40.16 MiB / 40.41 MiB (15) |
| heap_inuse_gc_ready | 70.09 MiB / 101.34 MiB (15) | 40.91 MiB / 41.16 MiB (15) |
| heap_used_asis_ready | 41.51 MiB / 41.76 MiB (15) | 26.08 MiB / 27.58 MiB (15) |
| heap_used_gc_ready | 35.15 MiB / 35.15 MiB (15) | 21.93 MiB / 21.94 MiB (15) |
| host_anon_end | 65.11 MiB / 72.62 MiB (15) | 104.88 MiB / 112.04 MiB (15) |
| host_anon_ready | 43.16 MiB / 43.60 MiB (15) | 59.66 MiB / 60.55 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 560 / 580 (15) | 1990 / 2060 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 590 / 630 (15) |
| host_peak_rss_end | 107.34 MiB / 115.66 MiB (15) | 163.76 MiB / 170.90 MiB (15) |
| host_peak_rss_ready | 84.09 MiB / 84.79 MiB (15) | 111.68 MiB / 112.61 MiB (15) |
| host_pss_end | 85.49 MiB / 111.09 MiB (15) | 120.84 MiB / 125.47 MiB (15) |
| host_pss_ready | 63.12 MiB / 82.51 MiB (15) | 69.88 MiB / 73.76 MiB (15) |
| host_rss_end | 107.34 MiB / 115.66 MiB (15) | 163.76 MiB / 170.90 MiB (15) |
| host_rss_ready | 84.09 MiB / 84.79 MiB (15) | 111.68 MiB / 112.61 MiB (15) |
| host_threads_end | 8 / 8 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 31 / 31 (15) | 31 / 31 (15) |
| num_gc_end | 20 / 22 (15) | 20 / 21 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 13 / 15 (15) | 19 / 20 (15) |
| sys_asis_ready | 150.00 MiB / 154.50 MiB (15) | 105.04 MiB / 107.69 MiB (15) |
| sys_gc_ready | 150.00 MiB / 152.86 MiB (15) | 105.12 MiB / 108.02 MiB (15) |
| tool_ms:notes.append | 6.5 / 8.4 (90) | 1.3 / 1.9 (90) |
| tool_ms:notes.list | 37.0 / 47.7 (90) | 28.9 / 42.8 (90) |
| tool_ms:notes.read | 6.1 / 8.4 (90) | 1.5 / 2.6 (90) |
| tool_ms:notes.search | 83.6 / 125.4 (90) | 75.8 / 122.0 (90) |
| tool_ms:notes.write | 7.9 / 11.3 (90) | 2.8 / 4.4 (90) |
| tree_pss_ready | 178.82 MiB / 199.17 MiB (15) | 69.88 MiB / 73.76 MiB (15) |
| tree_rss_ready | 245.19 MiB / 246.48 MiB (15) | 111.68 MiB / 112.61 MiB (15) |

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
| cpu_total_ms_ready | 1040 / 1050 (15) | 590 / 630 (15) |
| cpu_wait4_ms | 241.2 / 250.7 (15) | 1194.0 / 1253.6 (15) |
| ext_anon_ready | 105.35 MiB / 106.12 MiB (15) | 59.47 MiB / 60.37 MiB (15) |
| ext_cchild_ms_end | 120 / 130 (15) | 120 / 140 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 870 / 880 (15) | 590 / 630 (15) |
| ext_peak_rss_ready | 161.01 MiB / 161.75 MiB (15) | 111.66 MiB / 112.49 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 97.59 MiB / 98.34 MiB (15) | 40.41 MiB / 42.41 MiB (15) |
| heap_inuse_gc_ready | 101.34 MiB / 101.84 MiB (15) | 40.91 MiB / 42.66 MiB (15) |
| heap_used_asis_ready | 36.93 MiB / 41.56 MiB (15) | 25.68 MiB / 31.25 MiB (15) |
| heap_used_gc_ready | 35.12 MiB / 35.15 MiB (15) | 21.94 MiB / 21.95 MiB (15) |
| host_anon_end | 50.05 MiB / 50.47 MiB (15) | 108.79 MiB / 110.21 MiB (15) |
| host_anon_ready | 43.16 MiB / 43.59 MiB (15) | 59.47 MiB / 60.37 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 120 / 140 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 220 / 220 (15) | 1040 / 1080 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 590 / 630 (15) |
| host_peak_rss_end | 92.52 MiB / 93.29 MiB (15) | 168.05 MiB / 169.25 MiB (15) |
| host_peak_rss_ready | 84.13 MiB / 84.76 MiB (15) | 111.66 MiB / 112.49 MiB (15) |
| host_pss_end | 90.83 MiB / 91.11 MiB (15) | 123.20 MiB / 125.88 MiB (15) |
| host_pss_ready | 82.43 MiB / 82.69 MiB (15) | 68.52 MiB / 70.25 MiB (15) |
| host_rss_end | 92.52 MiB / 93.29 MiB (15) | 168.05 MiB / 169.25 MiB (15) |
| host_rss_ready | 84.13 MiB / 84.76 MiB (15) | 111.66 MiB / 112.49 MiB (15) |
| host_threads_end | 8 / 8 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 4 / 4 (15) | 4 / 4 (15) |
| num_gc_end | 12 / 12 (15) | 10 / 12 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 5 / 5 (15) | 9 / 11 (15) |
| sys_asis_ready | 154.50 MiB / 156.00 MiB (15) | 105.05 MiB / 107.18 MiB (15) |
| sys_gc_ready | 152.34 MiB / 154.28 MiB (15) | 105.65 MiB / 107.18 MiB (15) |
| tool_ms:history.read | 27.2 / 28.7 (15) | 23.9 / 25.6 (15) |
| tool_ms:history.search:common | 25.7 / 28.0 (15) | 21.5 / 22.9 (15) |
| tool_ms:history.search:rare | 200.4 / 213.4 (15) | 176.2 / 190.7 (15) |
| tree_pss_ready | 196.49 MiB / 199.36 MiB (15) | 68.52 MiB / 70.25 MiB (15) |
| tree_rss_ready | 245.04 MiB / 245.93 MiB (15) | 111.66 MiB / 112.49 MiB (15) |

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
| cpu_total_ms_ready | 1030 / 1080 (15) | 590 / 610 (15) |
| cpu_wait4_ms | 244.1 / 262.4 (15) | 1558.1 / 1567.6 (15) |
| ext_anon_ready | 105.86 MiB / 106.66 MiB (15) | 59.45 MiB / 62.45 MiB (15) |
| ext_cchild_ms_end | 410 / 420 (15) | 420 / 430 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 850 / 910 (15) | 590 / 610 (15) |
| ext_peak_rss_ready | 161.43 MiB / 162.14 MiB (15) | 111.71 MiB / 114.55 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 63.84 MiB / 98.34 MiB (15) | 40.41 MiB / 40.91 MiB (15) |
| heap_inuse_gc_ready | 70.34 MiB / 101.59 MiB (15) | 40.91 MiB / 41.41 MiB (15) |
| heap_used_asis_ready | 41.49 MiB / 41.59 MiB (15) | 26.13 MiB / 27.57 MiB (15) |
| heap_used_gc_ready | 35.15 MiB / 35.15 MiB (15) | 21.94 MiB / 21.94 MiB (15) |
| host_anon_end | 50.31 MiB / 50.68 MiB (15) | 97.33 MiB / 111.23 MiB (15) |
| host_anon_ready | 43.32 MiB / 43.67 MiB (15) | 59.45 MiB / 62.45 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 420 / 430 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 220 / 230 (15) | 1100 / 1120 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 590 / 610 (15) |
| host_peak_rss_end | 92.56 MiB / 93.11 MiB (15) | 168.85 MiB / 170.29 MiB (15) |
| host_peak_rss_ready | 84.01 MiB / 84.38 MiB (15) | 111.71 MiB / 114.55 MiB (15) |
| host_pss_end | 73.51 MiB / 91.15 MiB (15) | 111.62 MiB / 124.80 MiB (15) |
| host_pss_ready | 64.88 MiB / 82.56 MiB (15) | 66.60 MiB / 69.32 MiB (15) |
| host_rss_end | 92.56 MiB / 93.11 MiB (15) | 156.34 MiB / 170.29 MiB (15) |
| host_rss_ready | 84.01 MiB / 84.38 MiB (15) | 111.71 MiB / 114.55 MiB (15) |
| host_threads_end | 8 / 8 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 4 / 4 (15) | 4 / 4 (15) |
| num_gc_end | 14 / 14 (15) | 15 / 15 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 1 (15) |
| num_gc_workload | 7 / 7 (15) | 14 / 14 (15) |
| sys_asis_ready | 151.50 MiB / 156.00 MiB (15) | 105.68 MiB / 107.18 MiB (15) |
| sys_gc_ready | 150.00 MiB / 154.14 MiB (15) | 105.68 MiB / 107.24 MiB (15) |
| tool_ms:history.read | 48.6 / 55.5 (15) | 43.7 / 45.1 (15) |
| tool_ms:history.search:common | 36.6 / 40.8 (15) | 32.2 / 34.1 (15) |
| tool_ms:history.search:rare | 275.2 / 322.8 (15) | 250.3 / 260.1 (15) |
| tree_pss_ready | 180.12 MiB / 197.08 MiB (15) | 66.60 MiB / 69.32 MiB (15) |
| tree_rss_ready | 245.41 MiB / 246.62 MiB (15) | 111.71 MiB / 114.55 MiB (15) |

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
| cpu_total_ms_ready | 1030 / 1040 (15) | 600 / 610 (15) |
| cpu_wait4_ms | 8756.7 / 8883.7 (15) | 3660.7 / 3734.1 (15) |
| ext_anon_ready | 105.62 MiB / 106.51 MiB (15) | 59.28 MiB / 60.03 MiB (15) |
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 1 / 1 (15) | 1 / 1 (15) |
| ext_cpu_ms_ready | 860 / 870 (15) | 600 / 610 (15) |
| ext_peak_rss_ready | 161.27 MiB / 162.00 MiB (15) | 111.43 MiB / 112.16 MiB (15) |
| ext_threads_end | 12 / 12 (15) | 11 / 11 (15) |
| ext_threads_ready | 12 / 12 (15) | 11 / 11 (15) |
| heap_inuse_asis_ready | 63.84 MiB / 98.34 MiB (15) | 40.41 MiB / 42.66 MiB (15) |
| heap_inuse_gc_ready | 70.09 MiB / 101.59 MiB (15) | 40.91 MiB / 58.16 MiB (15) |
| heap_used_asis_ready | 41.42 MiB / 41.60 MiB (15) | 25.67 MiB / 26.15 MiB (15) |
| heap_used_gc_ready | 35.15 MiB / 35.15 MiB (15) | 21.94 MiB / 21.94 MiB (15) |
| host_anon_end | 87.55 MiB / 96.12 MiB (15) | 256.81 MiB / 258.08 MiB (15) |
| host_anon_ready | 43.32 MiB / 43.74 MiB (15) | 59.28 MiB / 60.03 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cchild_ms_ready | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 8720 / 8850 (15) | 3630 / 3700 (15) |
| host_cpu_ms_ready | 170 / 180 (15) | 600 / 610 (15) |
| host_peak_rss_end | 132.22 MiB / 139.23 MiB (15) | 315.89 MiB / 317.00 MiB (15) |
| host_peak_rss_ready | 84.10 MiB / 84.65 MiB (15) | 111.43 MiB / 112.16 MiB (15) |
| host_pss_end | 104.51 MiB / 110.36 MiB (15) | 269.55 MiB / 277.23 MiB (15) |
| host_pss_ready | 62.92 MiB / 82.52 MiB (15) | 69.71 MiB / 77.07 MiB (15) |
| host_rss_end | 131.00 MiB / 139.23 MiB (15) | 315.89 MiB / 317.00 MiB (15) |
| host_rss_ready | 84.10 MiB / 84.65 MiB (15) | 111.43 MiB / 112.16 MiB (15) |
| host_threads_end | 9 / 9 (15) | 11 / 11 (15) |
| host_threads_ready | 7 / 7 (15) | 11 / 11 (15) |
| llm_requests | 200 / 200 (15) | 200 / 200 (15) |
| num_gc_end | 65 / 68 (15) | 23 / 24 (15) |
| num_gc_ready | 7 / 7 (15) | 1 / 2 (15) |
| num_gc_workload | 58 / 61 (15) | 22 / 23 (15) |
| session_compaction | 3 / 3 (15) | 3 / 3 (15) |
| session_context_window | 0 / 0 (15) | 0 / 0 (15) |
| session_custom_message | 3 / 3 (15) | 3 / 3 (15) |
| session_entries | 531 / 531 (15) | 531 / 531 (15) |
| session_tool_results | 180 / 180 (15) | 180 / 180 (15) |
| sys_asis_ready | 151.50 MiB / 154.50 MiB (15) | 105.01 MiB / 109.38 MiB (15) |
| sys_gc_ready | 151.28 MiB / 154.47 MiB (15) | 105.01 MiB / 109.38 MiB (15) |
| tool_ms:get_context_remaining | 5.8 / 8.1 (450) | 0.4 / 0.6 (450) |
| tool_ms:notes.append | 8.2 / 10.9 (900) | 1.1 / 1.5 (900) |
| tool_ms:notes.write | 10.1 / 13.1 (900) | 3.0 / 4.0 (900) |
| tool_ms:read | 10.9 / 13.0 (450) | 3.3 / 4.2 (450) |
| tree_pss_ready | 177.28 MiB / 196.52 MiB (15) | 69.71 MiB / 77.07 MiB (15) |
| tree_rss_ready | 245.33 MiB / 246.29 MiB (15) | 111.43 MiB / 112.16 MiB (15) |

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
| ext_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| ext_count | 60 / 60 (15) | 60 / 60 (15) |
| ext_threads_end | 720 / 720 (15) | 660 / 660 (15) |
| host_anon_end | 2601.07 MiB / 2603.02 MiB (15) | 3037.86 MiB / 3046.13 MiB (15) |
| host_cchild_ms_end | 0 / 0 (15) | 0 / 0 (15) |
| host_cpu_ms_end | 10270 / 10390 (15) | 40740 / 40980 (15) |
| host_peak_rss_end | 5052.30 MiB / 5054.71 MiB (15) | 6389.30 MiB / 6401.93 MiB (15) |
| host_pss_end | 2642.64 MiB / 2644.91 MiB (15) | 3084.85 MiB / 3096.39 MiB (15) |
| host_rss_end | 5052.30 MiB / 5054.71 MiB (15) | 6165.25 MiB / 6173.42 MiB (15) |
| host_threads_end | 420 / 421 (15) | 660 / 660 (15) |

