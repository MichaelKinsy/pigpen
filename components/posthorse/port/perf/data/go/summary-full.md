# Posthorse perf summary (every metric)

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
| cpu_total_ms_ready | 930 / 1020 (25) |
| cpu_wait4_ms | 958.0 / 1063.5 (25) |
| ext_anon_ready | 2.39 MiB / 2.55 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) |
| ext_count | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) |
| ext_peak_rss_ready | 9.20 MiB / 9.46 MiB (25) |
| ext_threads_end | 5 / 5 (25) |
| ext_threads_ready | 5 / 5 (25) |
| heap_inuse_asis_ready | 2.15 MiB / 2.27 MiB (25) |
| heap_inuse_gc_ready | 1.55 MiB / 1.60 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.04 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) |
| host_anon_end | 28.50 MiB / 34.50 MiB (25) |
| host_anon_ready | 28.50 MiB / 34.50 MiB (25) |
| host_cchild_ms_end | 720 / 780 (25) |
| host_cchild_ms_ready | 720 / 780 (25) |
| host_cpu_ms_end | 210 / 230 (25) |
| host_cpu_ms_ready | 210 / 230 (25) |
| host_peak_rss_end | 69.84 MiB / 75.95 MiB (25) |
| host_peak_rss_ready | 69.84 MiB / 75.95 MiB (25) |
| host_pss_end | 68.33 MiB / 74.52 MiB (25) |
| host_pss_ready | 68.33 MiB / 74.52 MiB (25) |
| host_rss_end | 69.84 MiB / 75.95 MiB (25) |
| host_rss_ready | 69.84 MiB / 75.95 MiB (25) |
| host_threads_end | 7 / 7 (25) |
| host_threads_ready | 7 / 7 (25) |
| mallocs_end | 12782 / 12795 (25) |
| mallocs_ready | 12596 / 12613 (25) |
| mallocs_workload | 188 / 190 (25) |
| num_gc_end | 2 / 2 (25) |
| num_gc_ready | 0 / 0 (25) |
| num_gc_workload | 2 / 2 (25) |
| sys_asis_ready | 12.21 MiB / 12.46 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.41 MiB (25) |
| total_alloc_end | 1.08 MiB / 1.08 MiB (25) |
| total_alloc_ready | 1.04 MiB / 1.04 MiB (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) |
| tree_pss_ready | 77.54 MiB / 83.62 MiB (25) |
| tree_rss_ready | 79.00 MiB / 85.05 MiB (25) |

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
| cpu_total_ms_ready | 80 / 90 (25) |
| cpu_wait4_ms | 92.0 / 108.5 (25) |
| ext_anon_ready | 2.40 MiB / 2.61 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) |
| ext_count | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) |
| ext_peak_rss_ready | 9.21 MiB / 9.58 MiB (25) |
| ext_threads_end | 5 / 5 (25) |
| ext_threads_ready | 5 / 5 (25) |
| heap_inuse_asis_ready | 2.10 MiB / 2.38 MiB (25) |
| heap_inuse_gc_ready | 1.52 MiB / 1.61 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.05 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) |
| host_anon_end | 19.67 MiB / 20.20 MiB (25) |
| host_anon_ready | 19.67 MiB / 20.20 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) |
| host_cpu_ms_end | 70 / 80 (25) |
| host_cpu_ms_ready | 70 / 80 (25) |
| host_peak_rss_end | 60.52 MiB / 60.97 MiB (25) |
| host_peak_rss_ready | 60.52 MiB / 60.97 MiB (25) |
| host_pss_end | 59.07 MiB / 59.45 MiB (25) |
| host_pss_ready | 59.07 MiB / 59.45 MiB (25) |
| host_rss_end | 60.52 MiB / 60.97 MiB (25) |
| host_rss_ready | 60.52 MiB / 60.97 MiB (25) |
| host_threads_end | 7 / 7 (25) |
| host_threads_ready | 7 / 7 (25) |
| mallocs_end | 12876 / 12895 (25) |
| mallocs_ready | 12687 / 12707 (25) |
| mallocs_workload | 188 / 189 (25) |
| num_gc_end | 2 / 2 (25) |
| num_gc_ready | 0 / 0 (25) |
| num_gc_workload | 2 / 2 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (25) |
| total_alloc_end | 1.08 MiB / 1.09 MiB (25) |
| total_alloc_ready | 1.04 MiB / 1.05 MiB (25) |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) |
| tree_pss_ready | 68.27 MiB / 68.84 MiB (25) |
| tree_rss_ready | 69.78 MiB / 70.37 MiB (25) |

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
| cpu_total_ms_ready | 900 / 1050 (25) |
| cpu_wait4_ms | 952.7 / 1084.5 (25) |
| ext_anon_ready | 2.42 MiB / 2.61 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) |
| ext_count | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) |
| ext_peak_rss_ready | 9.27 MiB / 9.54 MiB (25) |
| ext_threads_end | 5 / 9 (25) |
| ext_threads_ready | 5 / 5 (25) |
| heap_inuse_asis_ready | 2.12 MiB / 2.30 MiB (25) |
| heap_inuse_gc_ready | 1.52 MiB / 1.60 MiB (25) |
| heap_used_asis_ready | 1.05 MiB / 1.05 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) |
| host_anon_end | 30.18 MiB / 35.83 MiB (25) |
| host_anon_ready | 29.08 MiB / 35.42 MiB (25) |
| host_cchild_ms_end | 690 / 780 (25) |
| host_cchild_ms_ready | 690 / 780 (25) |
| host_cpu_ms_end | 240 / 280 (25) |
| host_cpu_ms_ready | 210 / 240 (25) |
| host_peak_rss_end | 73.59 MiB / 79.05 MiB (25) |
| host_peak_rss_ready | 70.87 MiB / 77.02 MiB (25) |
| host_pss_end | 72.12 MiB / 77.56 MiB (25) |
| host_pss_ready | 69.33 MiB / 75.53 MiB (25) |
| host_rss_end | 73.59 MiB / 79.05 MiB (25) |
| host_rss_ready | 70.87 MiB / 77.02 MiB (25) |
| host_threads_end | 8 / 8 (25) |
| host_threads_ready | 7 / 7 (25) |
| llm_requests | 2 / 2 (25) |
| mallocs_end | 27007 / 27061 (25) |
| mallocs_ready | 12689 / 12703 (25) |
| mallocs_workload | 14324 / 14366 (25) |
| num_gc_end | 2 / 2 (25) |
| num_gc_ready | 0 / 0 (25) |
| num_gc_workload | 2 / 2 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) |
| sys_gc_ready | 13.40 MiB / 13.65 MiB (25) |
| tool_ms:get_context_remaining | 2.7 / 5.5 (25) |
| total_alloc_end | 2.36 MiB / 2.40 MiB (25) |
| total_alloc_ready | 1.05 MiB / 1.05 MiB (25) |
| total_alloc_workload | 1.31 MiB / 1.35 MiB (25) |
| tree_pss_ready | 78.95 MiB / 84.70 MiB (25) |
| tree_rss_ready | 80.46 MiB / 86.19 MiB (25) |

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
| cpu_total_ms_ready | 90 / 100 (25) |
| cpu_wait4_ms | 125.0 / 159.0 (25) |
| ext_anon_ready | 2.41 MiB / 2.68 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) |
| ext_count | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) |
| ext_peak_rss_ready | 9.25 MiB / 9.56 MiB (25) |
| ext_threads_end | 5 / 9 (25) |
| ext_threads_ready | 5 / 5 (25) |
| heap_inuse_asis_ready | 2.09 MiB / 2.30 MiB (25) |
| heap_inuse_gc_ready | 1.47 MiB / 1.59 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.05 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.51 MiB (25) |
| host_anon_end | 20.23 MiB / 20.95 MiB (25) |
| host_anon_ready | 19.79 MiB / 20.48 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) |
| host_cpu_ms_end | 100 / 120 (25) |
| host_cpu_ms_ready | 80 / 90 (25) |
| host_peak_rss_end | 62.92 MiB / 63.79 MiB (25) |
| host_peak_rss_ready | 60.64 MiB / 61.55 MiB (25) |
| host_pss_end | 61.45 MiB / 62.37 MiB (25) |
| host_pss_ready | 59.14 MiB / 60.08 MiB (25) |
| host_rss_end | 62.92 MiB / 63.79 MiB (25) |
| host_rss_ready | 60.64 MiB / 61.55 MiB (25) |
| host_threads_end | 8 / 8 (25) |
| host_threads_ready | 7 / 7 (25) |
| llm_requests | 2 / 2 (25) |
| mallocs_end | 26993 / 27067 (25) |
| mallocs_ready | 12687 / 12699 (25) |
| mallocs_workload | 14339 / 14381 (25) |
| num_gc_end | 2 / 2 (25) |
| num_gc_ready | 0 / 0 (25) |
| num_gc_workload | 2 / 2 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.67 MiB (25) |
| tool_ms:get_context_remaining | 4.3 / 5.7 (25) |
| total_alloc_end | 2.36 MiB / 2.40 MiB (25) |
| total_alloc_ready | 1.04 MiB / 1.05 MiB (25) |
| total_alloc_workload | 1.32 MiB / 1.36 MiB (25) |
| tree_pss_ready | 68.44 MiB / 69.52 MiB (25) |
| tree_rss_ready | 70.00 MiB / 70.93 MiB (25) |

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
| cpu_total_ms_ready | 90 / 100 (60) |
| cpu_wait4_ms | 575.8 / 621.9 (60) |
| ext_anon_ready | 2.41 MiB / 2.63 MiB (60) |
| ext_cchild_ms_end | 0 / 0 (60) |
| ext_cchild_ms_ready | 0 / 0 (60) |
| ext_count | 1 / 1 (60) |
| ext_cpu_ms_ready | 10 / 10 (60) |
| ext_peak_rss_ready | 9.28 MiB / 9.50 MiB (60) |
| ext_threads_end | 10 / 10 (60) |
| ext_threads_ready | 5 / 5 (60) |
| heap_inuse_asis_ready | 2.10 MiB / 2.28 MiB (60) |
| heap_inuse_gc_ready | 1.48 MiB / 1.58 MiB (60) |
| heap_used_asis_ready | 1.04 MiB / 1.04 MiB (60) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (60) |
| host_anon_end | 28.21 MiB / 29.02 MiB (60) |
| host_anon_ready | 19.82 MiB / 20.40 MiB (60) |
| host_cchild_ms_end | 0 / 0 (60) |
| host_cchild_ms_ready | 0 / 0 (60) |
| host_cpu_ms_end | 550 / 580 (60) |
| host_cpu_ms_ready | 80 / 90 (60) |
| host_peak_rss_end | 70.94 MiB / 71.86 MiB (60) |
| host_peak_rss_ready | 60.72 MiB / 61.30 MiB (60) |
| host_pss_end | 69.49 MiB / 70.39 MiB (60) |
| host_pss_ready | 59.28 MiB / 59.90 MiB (60) |
| host_rss_end | 70.94 MiB / 71.86 MiB (60) |
| host_rss_ready | 60.72 MiB / 61.30 MiB (60) |
| host_threads_end | 8 / 8 (60) |
| host_threads_ready | 7 / 7 (60) |
| llm_requests | 31 / 31 (60) |
| mallocs_end | 847945.5 / 849675 (60) |
| mallocs_ready | 12566.5 / 12585 (60) |
| mallocs_workload | 835399 / 837101 (60) |
| num_gc_end | 32 / 34 (60) |
| num_gc_ready | 0 / 0 (60) |
| num_gc_workload | 32 / 34 (60) |
| sys_asis_ready | 12.24 MiB / 12.46 MiB (60) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (60) |
| tool_ms:notes.append | 5.2 / 7.2 (360) |
| tool_ms:notes.list | 5.5 / 7.5 (360) |
| tool_ms:notes.read | 4.9 / 7.3 (360) |
| tool_ms:notes.search | 5.1 / 7.2 (360) |
| tool_ms:notes.write | 5.9 / 7.4 (360) |
| total_alloc_end | 58.46 MiB / 58.82 MiB (60) |
| total_alloc_ready | 1.04 MiB / 1.04 MiB (60) |
| total_alloc_workload | 57.41 MiB / 57.78 MiB (60) |
| tree_pss_ready | 68.58 MiB / 69.25 MiB (60) |
| tree_rss_ready | 69.98 MiB / 70.75 MiB (60) |

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
| cpu_total_ms_ready | 90 / 100 (60) |
| cpu_wait4_ms | 679.4 / 1486.5 (60) |
| ext_anon_ready | 2.41 MiB / 2.66 MiB (60) |
| ext_cchild_ms_end | 0 / 0 (60) |
| ext_cchild_ms_ready | 0 / 0 (60) |
| ext_count | 1 / 1 (60) |
| ext_cpu_ms_ready | 10 / 10 (60) |
| ext_peak_rss_ready | 9.27 MiB / 9.50 MiB (60) |
| ext_threads_end | 10 / 10 (60) |
| ext_threads_ready | 5 / 5 (60) |
| heap_inuse_asis_ready | 2.22 MiB / 2.37 MiB (60) |
| heap_inuse_gc_ready | 1.54 MiB / 1.62 MiB (60) |
| heap_used_asis_ready | 1.04 MiB / 1.04 MiB (60) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (60) |
| host_anon_end | 30.34 MiB / 30.75 MiB (60) |
| host_anon_ready | 19.82 MiB / 20.43 MiB (60) |
| host_cchild_ms_end | 0 / 0 (60) |
| host_cchild_ms_ready | 0 / 0 (60) |
| host_cpu_ms_end | 645 / 670 (60) |
| host_cpu_ms_ready | 80 / 90 (60) |
| host_peak_rss_end | 72.93 MiB / 73.69 MiB (60) |
| host_peak_rss_ready | 60.73 MiB / 61.38 MiB (60) |
| host_pss_end | 71.48 MiB / 72.34 MiB (60) |
| host_pss_ready | 59.27 MiB / 59.88 MiB (60) |
| host_rss_end | 72.93 MiB / 73.69 MiB (60) |
| host_rss_ready | 60.73 MiB / 61.38 MiB (60) |
| host_threads_end | 8 / 8 (60) |
| host_threads_ready | 7 / 7 (60) |
| llm_requests | 31 / 31 (60) |
| mallocs_end | 927729.5 / 929615 (60) |
| mallocs_ready | 12566 / 12589 (60) |
| mallocs_workload | 915169 / 917046 (60) |
| num_gc_end | 48 / 49 (60) |
| num_gc_ready | 0 / 0 (60) |
| num_gc_workload | 48 / 49 (60) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (60) |
| sys_gc_ready | 13.40 MiB / 13.66 MiB (60) |
| tool_ms:notes.append | 6.2 / 7.6 (360) |
| tool_ms:notes.list | 10.5 / 13.0 (360) |
| tool_ms:notes.read | 5.7 / 7.6 (360) |
| tool_ms:notes.search | 11.0 / 13.5 (360) |
| tool_ms:notes.write | 6.3 / 8.0 (360) |
| total_alloc_end | 85.37 MiB / 85.77 MiB (60) |
| total_alloc_ready | 1.04 MiB / 1.04 MiB (60) |
| total_alloc_workload | 84.33 MiB / 84.72 MiB (60) |
| tree_pss_ready | 68.52 MiB / 69.21 MiB (60) |
| tree_rss_ready | 69.94 MiB / 70.63 MiB (60) |

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
| cpu_total_ms_ready | 80 / 100 (60) |
| cpu_wait4_ms | 666.5 / 1324.8 (60) |
| ext_anon_ready | 2.39 MiB / 2.60 MiB (60) |
| ext_cchild_ms_end | 0 / 0 (60) |
| ext_cchild_ms_ready | 0 / 0 (60) |
| ext_count | 1 / 1 (60) |
| ext_cpu_ms_ready | 10 / 10 (60) |
| ext_peak_rss_ready | 9.19 MiB / 9.46 MiB (60) |
| ext_threads_end | 10 / 10 (60) |
| ext_threads_ready | 5 / 5 (60) |
| heap_inuse_asis_ready | 2.18 MiB / 2.33 MiB (60) |
| heap_inuse_gc_ready | 1.52 MiB / 1.61 MiB (60) |
| heap_used_asis_ready | 1.04 MiB / 1.05 MiB (60) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (60) |
| host_anon_end | 30.07 MiB / 30.98 MiB (60) |
| host_anon_ready | 19.84 MiB / 20.43 MiB (60) |
| host_cchild_ms_end | 0 / 0 (60) |
| host_cchild_ms_ready | 0 / 0 (60) |
| host_cpu_ms_end | 605 / 680 (60) |
| host_cpu_ms_ready | 80 / 90 (60) |
| host_peak_rss_end | 72.85 MiB / 73.93 MiB (60) |
| host_peak_rss_ready | 60.73 MiB / 61.22 MiB (60) |
| host_pss_end | 71.37 MiB / 72.45 MiB (60) |
| host_pss_ready | 59.31 MiB / 59.85 MiB (60) |
| host_rss_end | 72.85 MiB / 73.93 MiB (60) |
| host_rss_ready | 60.73 MiB / 61.22 MiB (60) |
| host_threads_end | 8 / 8 (60) |
| host_threads_ready | 7 / 7 (60) |
| llm_requests | 31 / 31 (60) |
| mallocs_end | 1359029 / 1360748 (60) |
| mallocs_ready | 12569.5 / 12589 (60) |
| mallocs_workload | 1346482 / 1348176 (60) |
| num_gc_end | 79 / 80 (60) |
| num_gc_ready | 0 / 0 (60) |
| num_gc_workload | 79 / 80 (60) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (60) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (60) |
| tool_ms:notes.append | 5.9 / 7.7 (360) |
| tool_ms:notes.list | 20.8 / 24.0 (360) |
| tool_ms:notes.read | 5.6 / 7.6 (360) |
| tool_ms:notes.search | 32.0 / 36.5 (360) |
| tool_ms:notes.write | 6.0 / 7.9 (360) |
| total_alloc_end | 141.66 MiB / 141.96 MiB (60) |
| total_alloc_ready | 1.04 MiB / 1.05 MiB (60) |
| total_alloc_workload | 140.61 MiB / 140.92 MiB (60) |
| tree_pss_ready | 68.56 MiB / 69.09 MiB (60) |
| tree_rss_ready | 70.00 MiB / 70.51 MiB (60) |

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
| cpu_total_ms_ready | 90 / 100 (60) |
| cpu_wait4_ms | 159.7 / 385.2 (60) |
| ext_anon_ready | 2.48 MiB / 2.71 MiB (60) |
| ext_cchild_ms_end | 110 / 130 (60) |
| ext_cchild_ms_ready | 0 / 0 (60) |
| ext_count | 1 / 1 (60) |
| ext_cpu_ms_ready | 10 / 10 (60) |
| ext_peak_rss_ready | 9.37 MiB / 9.58 MiB (60) |
| ext_threads_end | 9 / 10 (60) |
| ext_threads_ready | 5 / 5 (60) |
| heap_inuse_asis_ready | 2.16 MiB / 2.32 MiB (60) |
| heap_inuse_gc_ready | 1.54 MiB / 1.61 MiB (60) |
| heap_used_asis_ready | 1.05 MiB / 1.05 MiB (60) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (60) |
| host_anon_end | 20.43 MiB / 21.03 MiB (60) |
| host_anon_ready | 19.80 MiB / 20.43 MiB (60) |
| host_cchild_ms_end | 0 / 0 (60) |
| host_cchild_ms_ready | 0 / 0 (60) |
| host_cpu_ms_end | 140 / 150 (60) |
| host_cpu_ms_ready | 80 / 90 (60) |
| host_peak_rss_end | 63.14 MiB / 63.63 MiB (60) |
| host_peak_rss_ready | 60.74 MiB / 61.20 MiB (60) |
| host_pss_end | 61.69 MiB / 62.24 MiB (60) |
| host_pss_ready | 59.26 MiB / 59.80 MiB (60) |
| host_rss_end | 63.14 MiB / 63.63 MiB (60) |
| host_rss_ready | 60.74 MiB / 61.20 MiB (60) |
| host_threads_end | 8 / 8 (60) |
| host_threads_ready | 7 / 7 (60) |
| llm_requests | 4 / 4 (60) |
| mallocs_end | 59162.5 / 59789 (60) |
| mallocs_ready | 12687.5 / 12710 (60) |
| mallocs_workload | 46455.5 / 47108 (60) |
| num_gc_end | 8 / 8 (60) |
| num_gc_ready | 0 / 0 (60) |
| num_gc_workload | 8 / 8 (60) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (60) |
| sys_gc_ready | 13.41 MiB / 13.67 MiB (60) |
| tool_ms:history.read | 20.0 / 23.5 (60) |
| tool_ms:history.search:common | 19.3 / 23.3 (60) |
| tool_ms:history.search:rare | 49.7 / 57.2 (60) |
| total_alloc_end | 14.05 MiB / 14.13 MiB (60) |
| total_alloc_ready | 1.05 MiB / 1.05 MiB (60) |
| total_alloc_workload | 13.00 MiB / 13.08 MiB (60) |
| tree_pss_ready | 68.63 MiB / 69.15 MiB (60) |
| tree_rss_ready | 70.02 MiB / 70.61 MiB (60) |

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
| cpu_total_ms_ready | 80 / 100 (60) |
| cpu_wait4_ms | 154.3 / 658.0 (60) |
| ext_anon_ready | 2.42 MiB / 2.59 MiB (60) |
| ext_cchild_ms_end | 415 / 470 (60) |
| ext_cchild_ms_ready | 0 / 0 (60) |
| ext_count | 1 / 1 (60) |
| ext_cpu_ms_ready | 10 / 10 (60) |
| ext_peak_rss_ready | 9.26 MiB / 9.49 MiB (60) |
| ext_threads_end | 9 / 10 (60) |
| ext_threads_ready | 5 / 5 (60) |
| heap_inuse_asis_ready | 2.25 MiB / 2.33 MiB (60) |
| heap_inuse_gc_ready | 1.54 MiB / 1.61 MiB (60) |
| heap_used_asis_ready | 1.05 MiB / 1.05 MiB (60) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (60) |
| host_anon_end | 20.45 MiB / 21.06 MiB (60) |
| host_anon_ready | 19.80 MiB / 20.40 MiB (60) |
| host_cchild_ms_end | 0 / 0 (60) |
| host_cchild_ms_ready | 0 / 0 (60) |
| host_cpu_ms_end | 130 / 150 (60) |
| host_cpu_ms_ready | 80 / 90 (60) |
| host_peak_rss_end | 63.09 MiB / 63.82 MiB (60) |
| host_peak_rss_ready | 60.63 MiB / 61.33 MiB (60) |
| host_pss_end | 61.66 MiB / 62.28 MiB (60) |
| host_pss_ready | 59.26 MiB / 59.85 MiB (60) |
| host_rss_end | 63.09 MiB / 63.82 MiB (60) |
| host_rss_ready | 60.63 MiB / 61.33 MiB (60) |
| host_threads_end | 8 / 8 (60) |
| host_threads_ready | 7 / 7 (60) |
| llm_requests | 4 / 4 (60) |
| mallocs_end | 60896.5 / 61456 (60) |
| mallocs_ready | 12688 / 12708 (60) |
| mallocs_workload | 48217.5 / 48758 (60) |
| num_gc_end | 9 / 9 (60) |
| num_gc_ready | 0 / 0 (60) |
| num_gc_workload | 9 / 9 (60) |
| sys_asis_ready | 12.28 MiB / 12.46 MiB (60) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (60) |
| tool_ms:history.read | 42.7 / 47.9 (60) |
| tool_ms:history.search:common | 31.4 / 36.9 (60) |
| tool_ms:history.search:rare | 83.6 / 93.1 (60) |
| total_alloc_end | 16.47 MiB / 16.55 MiB (60) |
| total_alloc_ready | 1.05 MiB / 1.05 MiB (60) |
| total_alloc_workload | 15.42 MiB / 15.50 MiB (60) |
| tree_pss_ready | 68.54 MiB / 69.19 MiB (60) |
| tree_rss_ready | 69.96 MiB / 70.67 MiB (60) |

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
| cpu_total_ms_ready | 80 / 100 (25) |
| cpu_wait4_ms | 13553.5 / 31480.3 (25) |
| ext_anon_ready | 2.39 MiB / 2.63 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) |
| ext_count | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) |
| ext_peak_rss_ready | 9.24 MiB / 9.47 MiB (25) |
| ext_threads_end | 10 / 10 (25) |
| ext_threads_ready | 5 / 5 (25) |
| heap_inuse_asis_ready | 2.26 MiB / 2.34 MiB (25) |
| heap_inuse_gc_ready | 1.55 MiB / 1.62 MiB (25) |
| heap_used_asis_ready | 1.05 MiB / 1.05 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) |
| host_anon_end | 69.19 MiB / 80.41 MiB (25) |
| host_anon_ready | 19.84 MiB / 20.37 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) |
| host_cpu_ms_end | 13420 / 13670 (25) |
| host_cpu_ms_ready | 80 / 90 (25) |
| host_peak_rss_end | 119.71 MiB / 125.94 MiB (25) |
| host_peak_rss_ready | 60.60 MiB / 61.35 MiB (25) |
| host_pss_end | 110.95 MiB / 122.38 MiB (25) |
| host_pss_ready | 59.21 MiB / 59.88 MiB (25) |
| host_rss_end | 112.44 MiB / 123.80 MiB (25) |
| host_rss_ready | 60.60 MiB / 61.35 MiB (25) |
| host_threads_end | 9 / 9 (25) |
| host_threads_ready | 7 / 7 (25) |
| llm_requests | 200 / 200 (25) |
| mallocs_end | 15153273 / 15161087 (25) |
| mallocs_ready | 12689 / 12706 (25) |
| mallocs_workload | 15140566 / 15148435 (25) |
| num_gc_end | 1154 / 1166 (25) |
| num_gc_ready | 0 / 0 (25) |
| num_gc_workload | 1154 / 1166 (25) |
| session_compaction | 3 / 3 (25) |
| session_context_window | 0 / 0 (25) |
| session_custom_message | 3 / 3 (25) |
| session_entries | 531 / 531 (25) |
| session_tool_results | 180 / 180 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (25) |
| tool_ms:get_context_remaining | 5.2 / 6.8 (750) |
| tool_ms:notes.append | 6.7 / 8.3 (1500) |
| tool_ms:notes.write | 7.1 / 8.8 (1500) |
| tool_ms:read | 10.9 / 12.8 (750) |
| total_alloc_end | 2418.78 MiB / 2421.05 MiB (25) |
| total_alloc_ready | 1.05 MiB / 1.05 MiB (25) |
| total_alloc_workload | 2417.73 MiB / 2420.00 MiB (25) |
| tree_pss_ready | 68.46 MiB / 69.27 MiB (25) |
| tree_rss_ready | 69.93 MiB / 70.79 MiB (25) |

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
| ext_cchild_ms_end | 0 / 0 (25) |
| ext_count | 60 / 60 (25) |
| ext_threads_end | 303 / 305 (25) |
| host_anon_end | 1189.79 MiB / 1192.55 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) |
| host_cpu_ms_end | 4420 / 4590 (25) |
| host_peak_rss_end | 3633.50 MiB / 3638.83 MiB (25) |
| host_pss_end | 1233.73 MiB / 1236.31 MiB (25) |
| host_rss_end | 3633.50 MiB / 3638.83 MiB (25) |
| host_threads_end | 420 / 420 (25) |

