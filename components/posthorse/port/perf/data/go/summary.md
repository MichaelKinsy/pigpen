# Posthorse perf summary

Medians and p90 over fresh-process samples; n in parentheses. Metric definitions: README.md. Every metric: summary-full.md.

### Workload a, cold

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 634.7 / 681.2 (25) |
| cpu_total_ms_end | 930 / 1020 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 10 / 10 (25) |
| ext_rss_end | 9.20 MiB / 9.46 MiB (25) |
| ext_pss_end | 9.20 MiB / 9.46 MiB (25) |
| tree_rss_end | 79.00 MiB / 85.05 MiB (25) |
| tree_pss_end | 77.54 MiB / 83.62 MiB (25) |
| ext_peak_rss_end | 9.20 MiB / 9.46 MiB (25) |
| ext_anon_end | 2.39 MiB / 2.55 MiB (25) |
| ext_rss_ready | 9.20 MiB / 9.46 MiB (25) |
| ext_pss_ready | 9.20 MiB / 9.46 MiB (25) |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB (25) |
| heap_used_asis_end | 0.51 MiB / 0.52 MiB (25) |
| heap_inuse_gc_end | 1.55 MiB / 1.60 MiB (25) |
| heap_inuse_asis_end | 1.55 MiB / 1.61 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.41 MiB (25) |
| sys_gc_end | 13.42 MiB / 13.44 MiB (25) |
| ext_count | 1 / 1 (25) |
| mallocs_workload | 188 / 190 (25) |
| num_gc_workload | 2 / 2 (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) |

### Workload a, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 113.0 / 119.8 (25) |
| cpu_total_ms_end | 80 / 90 (25) |
| cpu_total_workload_ms | 0.0 / 0.0 (25) |
| cpu_ext_workload_ms | 0.0 / 0.0 (25) |
| ext_cpu_ms_end | 10 / 10 (25) |
| ext_rss_end | 9.21 MiB / 9.58 MiB (25) |
| ext_pss_end | 9.21 MiB / 9.58 MiB (25) |
| tree_rss_end | 69.78 MiB / 70.37 MiB (25) |
| tree_pss_end | 68.27 MiB / 68.84 MiB (25) |
| ext_peak_rss_end | 9.21 MiB / 9.58 MiB (25) |
| ext_anon_end | 2.40 MiB / 2.61 MiB (25) |
| ext_rss_ready | 9.21 MiB / 9.58 MiB (25) |
| ext_pss_ready | 9.21 MiB / 9.58 MiB (25) |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB (25) |
| heap_used_asis_end | 0.51 MiB / 0.52 MiB (25) |
| heap_inuse_gc_end | 1.52 MiB / 1.61 MiB (25) |
| heap_inuse_asis_end | 1.53 MiB / 1.62 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.66 MiB (25) |
| sys_gc_end | 13.43 MiB / 13.67 MiB (25) |
| ext_count | 1 / 1 (25) |
| mallocs_workload | 188 / 189 (25) |
| num_gc_workload | 2 / 2 (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) |

### Workload b, cold

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 626.9 / 730.5 (25) |
| first_turn_ms | 34.8 / 44.8 (25) |
| to_first_tool_ms | 17.6 / 21.7 (25) |
| turn_ms | 34.8 / 44.8 (25) |
| cpu_total_ms_end | 950 / 1100 (25) |
| cpu_total_workload_ms | 40.0 / 50.0 (25) |
| cpu_ext_workload_ms | 20.0 / 20.0 (25) |
| ext_cpu_ms_end | 20 / 30 (25) |
| ext_rss_end | 11.02 MiB / 12.11 MiB (25) |
| ext_pss_end | 11.02 MiB / 12.10 MiB (25) |
| tree_rss_end | 84.84 MiB / 90.24 MiB (25) |
| tree_pss_end | 83.33 MiB / 88.71 MiB (25) |
| ext_peak_rss_end | 11.02 MiB / 12.11 MiB (25) |
| ext_anon_end | 4.09 MiB / 5.07 MiB (25) |
| ext_rss_ready | 9.27 MiB / 9.54 MiB (25) |
| ext_pss_ready | 9.26 MiB / 9.54 MiB (25) |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB (25) |
| heap_used_asis_end | 1.79 MiB / 1.83 MiB (25) |
| heap_inuse_gc_end | 1.63 MiB / 1.71 MiB (25) |
| heap_inuse_asis_end | 3.11 MiB / 3.29 MiB (25) |
| sys_asis_end | 13.41 MiB / 13.66 MiB (25) |
| sys_gc_end | 13.67 MiB / 13.69 MiB (25) |
| ext_count | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) |
| mallocs_workload | 14324 / 14366 (25) |
| num_gc_workload | 2 / 2 (25) |
| tool_ms:get_context_remaining | 2.7 / 5.5 (25) |
| total_alloc_workload | 1.31 MiB / 1.35 MiB (25) |

### Workload b, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 114.6 / 127.2 (25) |
| first_turn_ms | 41.0 / 47.3 (25) |
| to_first_tool_ms | 19.3 / 21.8 (25) |
| turn_ms | 41.0 / 47.3 (25) |
| cpu_total_ms_end | 130 / 150 (25) |
| cpu_total_workload_ms | 50.0 / 70.0 (25) |
| cpu_ext_workload_ms | 20.0 / 30.0 (25) |
| ext_cpu_ms_end | 30 / 30 (25) |
| ext_rss_end | 10.91 MiB / 12.12 MiB (25) |
| ext_pss_end | 10.91 MiB / 12.11 MiB (25) |
| tree_rss_end | 73.92 MiB / 75.27 MiB (25) |
| tree_pss_end | 72.40 MiB / 73.75 MiB (25) |
| ext_peak_rss_end | 10.91 MiB / 12.12 MiB (25) |
| ext_anon_end | 4.06 MiB / 5.08 MiB (25) |
| ext_rss_ready | 9.25 MiB / 9.56 MiB (25) |
| ext_pss_ready | 9.24 MiB / 9.55 MiB (25) |
| heap_used_gc_end | 0.54 MiB / 0.55 MiB (25) |
| heap_used_asis_end | 1.79 MiB / 1.84 MiB (25) |
| heap_inuse_gc_end | 1.64 MiB / 1.71 MiB (25) |
| heap_inuse_asis_end | 3.12 MiB / 3.48 MiB (25) |
| sys_asis_end | 13.66 MiB / 17.36 MiB (25) |
| sys_gc_end | 13.67 MiB / 17.42 MiB (25) |
| ext_count | 1 / 1 (25) |
| llm_requests | 2 / 2 (25) |
| mallocs_workload | 14339 / 14381 (25) |
| num_gc_workload | 2 / 2 (25) |
| tool_ms:get_context_remaining | 4.3 / 5.7 (25) |
| total_alloc_workload | 1.32 MiB / 1.36 MiB (25) |

### Workload c1, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 116.3 / 132.3 (60) |
| to_first_tool_ms | 19.8 / 22.6 (60) |
| turn_ms | 825.8 / 861.1 (60) |
| cpu_total_ms_end | 1080 / 1130 (60) |
| cpu_total_workload_ms | 990.0 / 1040.0 (60) |
| cpu_ext_workload_ms | 520.0 / 540.0 (60) |
| ext_cpu_ms_end | 525 / 550 (60) |
| ext_rss_end | 14.51 MiB / 14.73 MiB (60) |
| ext_pss_end | 14.51 MiB / 14.73 MiB (60) |
| tree_rss_end | 85.43 MiB / 86.29 MiB (60) |
| tree_pss_end | 84.02 MiB / 84.92 MiB (60) |
| ext_peak_rss_end | 14.51 MiB / 14.73 MiB (60) |
| ext_anon_end | 7.39 MiB / 7.59 MiB (60) |
| ext_rss_ready | 9.28 MiB / 9.50 MiB (60) |
| ext_pss_ready | 9.27 MiB / 9.50 MiB (60) |
| heap_used_gc_end | 0.57 MiB / 0.57 MiB (60) |
| heap_used_asis_end | 1.77 MiB / 2.76 MiB (60) |
| heap_inuse_gc_end | 1.71 MiB / 1.80 MiB (60) |
| heap_inuse_asis_end | 3.04 MiB / 3.94 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) |
| ext_count | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) |
| mallocs_workload | 835399 / 837101 (60) |
| num_gc_workload | 32 / 34 (60) |
| tool_ms:notes.append | 5.2 / 7.2 (360) |
| tool_ms:notes.list | 5.5 / 7.5 (360) |
| tool_ms:notes.read | 4.9 / 7.3 (360) |
| tool_ms:notes.search | 5.1 / 7.2 (360) |
| tool_ms:notes.write | 5.9 / 7.4 (360) |
| total_alloc_workload | 57.41 MiB / 57.78 MiB (60) |

### Workload c100, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 115.6 / 131.6 (60) |
| to_first_tool_ms | 20.0 / 22.5 (60) |
| turn_ms | 1080.3 / 1114.9 (60) |
| cpu_total_ms_end | 1415 / 1470 (60) |
| cpu_total_workload_ms | 1320.0 / 1370.0 (60) |
| cpu_ext_workload_ms | 755.0 / 790.0 (60) |
| ext_cpu_ms_end | 760 / 800 (60) |
| ext_rss_end | 14.81 MiB / 15.11 MiB (60) |
| ext_pss_end | 14.81 MiB / 15.10 MiB (60) |
| tree_rss_end | 87.72 MiB / 88.68 MiB (60) |
| tree_pss_end | 86.30 MiB / 87.25 MiB (60) |
| ext_peak_rss_end | 14.84 MiB / 15.11 MiB (60) |
| ext_anon_end | 7.65 MiB / 7.95 MiB (60) |
| ext_rss_ready | 9.27 MiB / 9.50 MiB (60) |
| ext_pss_ready | 9.27 MiB / 9.50 MiB (60) |
| heap_used_gc_end | 0.62 MiB / 0.63 MiB (60) |
| heap_used_asis_end | 2.22 MiB / 2.39 MiB (60) |
| heap_inuse_gc_end | 1.80 MiB / 1.88 MiB (60) |
| heap_inuse_asis_end | 3.31 MiB / 3.56 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) |
| ext_count | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) |
| mallocs_workload | 915169 / 917046 (60) |
| num_gc_workload | 48 / 49 (60) |
| tool_ms:notes.append | 6.2 / 7.6 (360) |
| tool_ms:notes.list | 10.5 / 13.0 (360) |
| tool_ms:notes.read | 5.7 / 7.6 (360) |
| tool_ms:notes.search | 11.0 / 13.5 (360) |
| tool_ms:notes.write | 6.3 / 8.0 (360) |
| total_alloc_workload | 84.33 MiB / 84.72 MiB (60) |

### Workload c1000, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 115.4 / 131.6 (60) |
| to_first_tool_ms | 18.8 / 22.0 (60) |
| turn_ms | 1242.9 / 1335.4 (60) |
| cpu_total_ms_end | 1705 / 1890 (60) |
| cpu_total_workload_ms | 1615.0 / 1800.0 (60) |
| cpu_ext_workload_ms | 1090.0 / 1210.0 (60) |
| ext_cpu_ms_end | 1100 / 1210 (60) |
| ext_rss_end | 15.22 MiB / 15.41 MiB (60) |
| ext_pss_end | 15.21 MiB / 15.41 MiB (60) |
| tree_rss_end | 88.06 MiB / 89.16 MiB (60) |
| tree_pss_end | 86.57 MiB / 87.77 MiB (60) |
| ext_peak_rss_end | 15.30 MiB / 16.50 MiB (60) |
| ext_anon_end | 8.06 MiB / 8.25 MiB (60) |
| ext_rss_ready | 9.19 MiB / 9.46 MiB (60) |
| ext_pss_ready | 9.19 MiB / 9.46 MiB (60) |
| heap_used_gc_end | 0.71 MiB / 0.71 MiB (60) |
| heap_used_asis_end | 2.46 MiB / 2.46 MiB (60) |
| heap_inuse_gc_end | 1.88 MiB / 1.96 MiB (60) |
| heap_inuse_asis_end | 3.55 MiB / 3.62 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) |
| ext_count | 1 / 1 (60) |
| llm_requests | 31 / 31 (60) |
| mallocs_workload | 1346482 / 1348176 (60) |
| num_gc_workload | 79 / 80 (60) |
| tool_ms:notes.append | 5.9 / 7.7 (360) |
| tool_ms:notes.list | 20.8 / 24.0 (360) |
| tool_ms:notes.read | 5.6 / 7.6 (360) |
| tool_ms:notes.search | 32.0 / 36.5 (360) |
| tool_ms:notes.write | 6.0 / 7.9 (360) |
| total_alloc_workload | 140.61 MiB / 140.92 MiB (60) |

### Workload d50, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 112.5 / 129.1 (60) |
| to_first_tool_ms | 19.3 / 22.8 (60) |
| turn_ms | 151.2 / 171.3 (60) |
| cpu_total_ms_end | 335 / 380 (60) |
| cpu_total_workload_ms | 250.0 / 290.0 (60) |
| cpu_ext_workload_ms | 80.0 / 100.0 (60) |
| ext_cpu_ms_end | 90 / 110 (60) |
| ext_rss_end | 14.45 MiB / 14.70 MiB (60) |
| ext_pss_end | 14.45 MiB / 14.69 MiB (60) |
| tree_rss_end | 77.61 MiB / 78.06 MiB (60) |
| tree_pss_end | 76.16 MiB / 76.71 MiB (60) |
| ext_peak_rss_end | 14.45 MiB / 14.71 MiB (60) |
| ext_anon_end | 7.23 MiB / 7.47 MiB (60) |
| ext_rss_ready | 9.37 MiB / 9.58 MiB (60) |
| ext_pss_ready | 9.37 MiB / 9.58 MiB (60) |
| heap_used_gc_end | 0.55 MiB / 0.56 MiB (60) |
| heap_used_asis_end | 1.17 MiB / 3.09 MiB (60) |
| heap_inuse_gc_end | 1.65 MiB / 1.73 MiB (60) |
| heap_inuse_asis_end | 2.48 MiB / 4.37 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) |
| ext_count | 1 / 1 (60) |
| llm_requests | 4 / 4 (60) |
| mallocs_workload | 46455.5 / 47108 (60) |
| num_gc_workload | 8 / 8 (60) |
| tool_ms:history.read | 20.0 / 23.5 (60) |
| tool_ms:history.search:common | 19.3 / 23.3 (60) |
| tool_ms:history.search:rare | 49.7 / 57.2 (60) |
| total_alloc_workload | 13.00 MiB / 13.08 MiB (60) |

### Workload d200, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 114.6 / 126.1 (60) |
| to_first_tool_ms | 19.4 / 22.0 (60) |
| turn_ms | 221.2 / 246.6 (60) |
| cpu_total_ms_end | 655 / 740 (60) |
| cpu_total_workload_ms | 570.0 / 650.0 (60) |
| cpu_ext_workload_ms | 100.0 / 120.0 (60) |
| ext_cpu_ms_end | 100 / 120 (60) |
| ext_rss_end | 14.51 MiB / 14.87 MiB (60) |
| ext_pss_end | 14.51 MiB / 14.86 MiB (60) |
| tree_rss_end | 77.59 MiB / 78.48 MiB (60) |
| tree_pss_end | 76.18 MiB / 77.03 MiB (60) |
| ext_peak_rss_end | 14.52 MiB / 14.96 MiB (60) |
| ext_anon_end | 7.29 MiB / 7.64 MiB (60) |
| ext_rss_ready | 9.26 MiB / 9.49 MiB (60) |
| ext_pss_ready | 9.25 MiB / 9.48 MiB (60) |
| heap_used_gc_end | 0.56 MiB / 0.57 MiB (60) |
| heap_used_asis_end | 1.59 MiB / 3.11 MiB (60) |
| heap_inuse_gc_end | 1.66 MiB / 1.73 MiB (60) |
| heap_inuse_asis_end | 2.93 MiB / 4.27 MiB (60) |
| sys_asis_end | 17.69 MiB / 17.94 MiB (60) |
| sys_gc_end | 17.69 MiB / 17.94 MiB (60) |
| ext_count | 1 / 1 (60) |
| llm_requests | 4 / 4 (60) |
| mallocs_workload | 48217.5 / 48758 (60) |
| num_gc_workload | 9 / 9 (60) |
| tool_ms:history.read | 42.7 / 47.9 (60) |
| tool_ms:history.search:common | 31.4 / 36.9 (60) |
| tool_ms:history.search:rare | 83.6 / 93.1 (60) |
| total_alloc_workload | 15.42 MiB / 15.50 MiB (60) |

### Workload e, warm

| Metric | go median / p90 (n) |
|---|---:|
| startup_ms | 115.1 / 128.4 (25) |
| to_first_tool_ms | 19.8 / 23.0 (25) |
| turn_ms | 26358.7 / 26858.5 (25) |
| cpu_total_ms_end | 31510 / 32330 (25) |
| cpu_total_workload_ms | 31430.0 / 32250.0 (25) |
| cpu_ext_workload_ms | 18130.0 / 18630.0 (25) |
| ext_cpu_ms_end | 18140 / 18640 (25) |
| ext_rss_end | 15.40 MiB / 22.08 MiB (25) |
| ext_pss_end | 15.39 MiB / 22.07 MiB (25) |
| tree_rss_end | 128.54 MiB / 141.33 MiB (25) |
| tree_pss_end | 127.08 MiB / 139.91 MiB (25) |
| ext_peak_rss_end | 21.66 MiB / 22.70 MiB (25) |
| ext_anon_end | 8.24 MiB / 14.86 MiB (25) |
| ext_rss_ready | 9.24 MiB / 9.47 MiB (25) |
| ext_pss_ready | 9.24 MiB / 9.47 MiB (25) |
| heap_used_gc_end | 0.66 MiB / 0.67 MiB (25) |
| heap_used_asis_end | 2.26 MiB / 2.78 MiB (25) |
| heap_inuse_gc_end | 2.12 MiB / 2.22 MiB (25) |
| heap_inuse_asis_end | 3.64 MiB / 4.28 MiB (25) |
| sys_asis_end | 25.94 MiB / 26.01 MiB (25) |
| sys_gc_end | 25.94 MiB / 26.01 MiB (25) |
| ext_count | 1 / 1 (25) |
| llm_requests | 200 / 200 (25) |
| mallocs_workload | 15140566 / 15148435 (25) |
| num_gc_workload | 1154 / 1166 (25) |
| session_compaction | 3 / 3 (25) |
| session_context_window | 0 / 0 (25) |
| session_custom_message | 3 / 3 (25) |
| session_entries | 531 / 531 (25) |
| session_tool_results | 180 / 180 (25) |
| tool_ms:get_context_remaining | 5.2 / 6.8 (750) |
| tool_ms:notes.append | 6.7 / 8.3 (1500) |
| tool_ms:notes.write | 7.1 / 8.8 (1500) |
| tool_ms:read | 10.9 / 12.8 (750) |
| total_alloc_workload | 2417.73 MiB / 2420.00 MiB (25) |

### Workload f, warm

| Metric | go median / p90 (n) |
|---|---:|
| cpu_total_ms_end | 4730 / 4930 (25) |
| ext_cpu_ms_end | 290 / 400 (25) |
| ext_rss_end | 558.06 MiB / 559.03 MiB (25) |
| ext_pss_end | 153.04 MiB / 153.98 MiB (25) |
| tree_rss_end | 4191.91 MiB / 4196.93 MiB (25) |
| tree_pss_end | 1386.53 MiB / 1388.46 MiB (25) |
| ext_peak_rss_end | 558.06 MiB / 559.03 MiB (25) |
| ext_anon_end | 146.11 MiB / 147.05 MiB (25) |
| ext_avg_rss_end | 9.30 MiB / 9.32 MiB (25) |
| ext_avg_pss_end | 2.55 MiB / 2.57 MiB (25) |
| all_ready_ms | 6695.5 / 6868.4 (25) |
| ext_count | 60 / 60 (25) |

## Behaviour parity of the workloads (model requests, session records)

identical across sides.


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
- a/warm go heap_inuse_asis_end: 5.6% (n=25, median 1.53 MiB)
- b/cold go startup_ms: 16.5% (n=25, median 626.9)
- b/cold go first_turn_ms: 28.7% (n=25, median 34.8)
- b/cold go to_first_tool_ms: 23.0% (n=25, median 17.6)
- b/cold go turn_ms: 28.7% (n=25, median 34.8)
- b/cold go cpu_total_ms_end: 15.8% (n=25, median 950)
- b/cold go cpu_total_workload_ms: 25.0% (n=25, median 40.0)
- b/cold go ext_cpu_ms_end: 50.0% (n=25, median 20)
- b/cold go ext_rss_end: 9.9% (n=25, median 11.02 MiB)
- b/cold go ext_pss_end: 9.9% (n=25, median 11.02 MiB)
- b/cold go tree_rss_end: 6.4% (n=25, median 84.84 MiB)
- b/cold go tree_pss_end: 6.5% (n=25, median 83.33 MiB)
- b/cold go ext_peak_rss_end: 9.9% (n=25, median 11.02 MiB)
- b/cold go ext_anon_end: 23.9% (n=25, median 4.09 MiB)
- b/cold go heap_inuse_asis_end: 5.8% (n=25, median 3.11 MiB)
- b/cold go tool_ms:get_context_remaining: 104.8% (n=25, median 2.7)
- b/warm go startup_ms: 11.0% (n=25, median 114.6)
- b/warm go first_turn_ms: 15.2% (n=25, median 41.0)
- b/warm go to_first_tool_ms: 12.4% (n=25, median 19.3)
- b/warm go turn_ms: 15.2% (n=25, median 41.0)
- b/warm go cpu_total_ms_end: 15.4% (n=25, median 130)
- b/warm go cpu_total_workload_ms: 40.0% (n=25, median 50.0)
- b/warm go cpu_ext_workload_ms: 50.0% (n=25, median 20.0)
- b/warm go ext_rss_end: 11.1% (n=25, median 10.91 MiB)
- b/warm go ext_pss_end: 11.1% (n=25, median 10.91 MiB)
- b/warm go ext_peak_rss_end: 11.1% (n=25, median 10.91 MiB)
- b/warm go ext_anon_end: 25.1% (n=25, median 4.06 MiB)
- b/warm go heap_inuse_asis_end: 11.2% (n=25, median 3.12 MiB)
- b/warm go sys_asis_end: 27.0% (n=25, median 13.66 MiB)
- b/warm go sys_gc_end: 27.5% (n=25, median 13.67 MiB)
- b/warm go tool_ms:get_context_remaining: 32.6% (n=25, median 4.3)
- c1/warm go startup_ms: 13.8% (n=60, median 116.3)
- c1/warm go to_first_tool_ms: 14.2% (n=60, median 19.8)
- c1/warm go cpu_total_workload_ms: 5.1% (n=60, median 990.0)
- c1/warm go heap_used_asis_end: 55.3% (n=60, median 1.77 MiB)
- c1/warm go heap_inuse_gc_end: 5.3% (n=60, median 1.71 MiB)
- c1/warm go heap_inuse_asis_end: 29.7% (n=60, median 3.04 MiB)
- c1/warm go tool_ms:notes.append: 38.4% (n=360, median 5.2)
- c1/warm go tool_ms:notes.list: 37.5% (n=360, median 5.5)
- c1/warm go tool_ms:notes.read: 48.1% (n=360, median 4.9)
- c1/warm go tool_ms:notes.search: 41.1% (n=360, median 5.1)
- c1/warm go tool_ms:notes.write: 24.8% (n=360, median 5.9)
- c100/warm go startup_ms: 13.8% (n=60, median 115.6)
- c100/warm go to_first_tool_ms: 12.4% (n=60, median 20.0)
- c100/warm go ext_cpu_ms_end: 5.3% (n=60, median 760)
- c100/warm go heap_used_asis_end: 7.6% (n=60, median 2.22 MiB)
- c100/warm go heap_inuse_asis_end: 7.7% (n=60, median 3.31 MiB)
- c100/warm go tool_ms:notes.append: 23.0% (n=360, median 6.2)
- c100/warm go tool_ms:notes.list: 24.2% (n=360, median 10.5)
- c100/warm go tool_ms:notes.read: 33.4% (n=360, median 5.7)
- c100/warm go tool_ms:notes.search: 22.9% (n=360, median 11.0)
- c100/warm go tool_ms:notes.write: 26.0% (n=360, median 6.3)
- c1000/warm go startup_ms: 14.1% (n=60, median 115.4)
- c1000/warm go to_first_tool_ms: 17.0% (n=60, median 18.8)
- c1000/warm go turn_ms: 7.4% (n=60, median 1242.9)
- c1000/warm go cpu_total_ms_end: 10.9% (n=60, median 1705)
- c1000/warm go cpu_total_workload_ms: 11.5% (n=60, median 1615.0)
- c1000/warm go cpu_ext_workload_ms: 11.0% (n=60, median 1090.0)
- c1000/warm go ext_cpu_ms_end: 10.0% (n=60, median 1100)
- c1000/warm go ext_peak_rss_end: 7.8% (n=60, median 15.30 MiB)
- c1000/warm go tool_ms:notes.append: 31.0% (n=360, median 5.9)
- c1000/warm go tool_ms:notes.list: 15.4% (n=360, median 20.8)
- c1000/warm go tool_ms:notes.read: 35.5% (n=360, median 5.6)
- c1000/warm go tool_ms:notes.search: 14.0% (n=360, median 32.0)
- c1000/warm go tool_ms:notes.write: 29.9% (n=360, median 6.0)
- d50/warm go startup_ms: 14.7% (n=60, median 112.5)
- d50/warm go to_first_tool_ms: 18.3% (n=60, median 19.3)
- d50/warm go turn_ms: 13.3% (n=60, median 151.2)
- d50/warm go cpu_total_ms_end: 13.4% (n=60, median 335)
- d50/warm go cpu_total_workload_ms: 16.0% (n=60, median 250.0)
- d50/warm go cpu_ext_workload_ms: 25.0% (n=60, median 80.0)
- d50/warm go ext_cpu_ms_end: 22.2% (n=60, median 90)
- d50/warm go heap_used_asis_end: 164.6% (n=60, median 1.17 MiB)
- d50/warm go heap_inuse_asis_end: 76.1% (n=60, median 2.48 MiB)
- d50/warm go tool_ms:history.read: 17.5% (n=60, median 20.0)
- d50/warm go tool_ms:history.search:common: 20.3% (n=60, median 19.3)
- d50/warm go tool_ms:history.search:rare: 15.1% (n=60, median 49.7)
- d200/warm go startup_ms: 10.1% (n=60, median 114.6)
- d200/warm go to_first_tool_ms: 13.8% (n=60, median 19.4)
- d200/warm go turn_ms: 11.5% (n=60, median 221.2)
- d200/warm go cpu_total_ms_end: 13.0% (n=60, median 655)
- d200/warm go cpu_total_workload_ms: 14.0% (n=60, median 570.0)
- d200/warm go cpu_ext_workload_ms: 20.0% (n=60, median 100.0)
- d200/warm go ext_cpu_ms_end: 20.0% (n=60, median 100)
- d200/warm go heap_used_asis_end: 94.9% (n=60, median 1.59 MiB)
- d200/warm go heap_inuse_asis_end: 45.4% (n=60, median 2.93 MiB)
- d200/warm go tool_ms:history.read: 12.1% (n=60, median 42.7)
- d200/warm go tool_ms:history.search:common: 17.5% (n=60, median 31.4)
- d200/warm go tool_ms:history.search:rare: 11.4% (n=60, median 83.6)
- e/warm go startup_ms: 11.5% (n=25, median 115.1)
- e/warm go to_first_tool_ms: 16.2% (n=25, median 19.8)
- e/warm go ext_rss_end: 43.4% (n=25, median 15.40 MiB)
- e/warm go ext_pss_end: 43.4% (n=25, median 15.39 MiB)
- e/warm go tree_rss_end: 10.0% (n=25, median 128.54 MiB)
- e/warm go tree_pss_end: 10.1% (n=25, median 127.08 MiB)
- e/warm go ext_anon_end: 80.3% (n=25, median 8.24 MiB)
- e/warm go heap_used_asis_end: 23.3% (n=25, median 2.26 MiB)
- e/warm go heap_inuse_asis_end: 17.6% (n=25, median 3.64 MiB)
- e/warm go tool_ms:get_context_remaining: 30.3% (n=750, median 5.2)
- e/warm go tool_ms:notes.append: 24.2% (n=1500, median 6.7)
- e/warm go tool_ms:notes.write: 23.4% (n=1500, median 7.1)
- e/warm go tool_ms:read: 17.6% (n=750, median 10.9)
- f/warm go ext_cpu_ms_end: 37.9% (n=25, median 290)

