# Posthorse perf summary (every metric)

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
| cpu_total_ms_ready | 1140 / 1190 (25) | 3740 / 4070 (25) | 1130 / 1200 (25) | 3810 / 4170 (25) |
| cpu_wait4_ms | 1165.6 / 1217.0 (25) | 663.2 / 710.6 (25) | 1152.8 / 1229.0 (25) | 678.3 / 714.9 (25) |
| ext_anon_ready | 2.42 MiB / 2.61 MiB (25) | 255.71 MiB / 259.29 MiB (25) | 2.45 MiB / 2.71 MiB (25) | 255.86 MiB / 258.83 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) | 3090 / 3420 (25) | 10 / 10 (25) | 3150 / 3490 (25) |
| ext_peak_rss_ready | 9.34 MiB / 9.59 MiB (25) | 311.43 MiB / 314.92 MiB (25) | 9.37 MiB / 9.58 MiB (25) | 311.56 MiB / 314.60 MiB (25) |
| ext_rchar_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_syscr_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_syscw_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_threads_end | 5 / 5 (25) | 12 / 12 (25) | 5 / 6 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 6 (25) | 12 / 12 (25) |
| ext_wchar_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| heap_inuse_asis_ready | 2.23 MiB / 2.32 MiB (25) | 193.75 MiB / 194.75 MiB (25) | 2.17 MiB / 2.37 MiB (25) | 194.00 MiB / 194.75 MiB (25) |
| heap_inuse_gc_ready | 1.55 MiB / 1.59 MiB (25) | 181.25 MiB / 182.00 MiB (25) | 1.51 MiB / 1.60 MiB (25) | 181.25 MiB / 182.00 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.05 MiB (25) | 49.84 MiB / 49.86 MiB (25) | 1.03 MiB / 1.04 MiB (25) | 49.84 MiB / 49.97 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.51 MiB (25) | 48.56 MiB / 48.57 MiB (25) | 0.50 MiB / 0.51 MiB (25) | 48.58 MiB / 48.58 MiB (25) |
| host_anon_end | 31.98 MiB / 34.79 MiB (25) | 106.05 MiB / 112.82 MiB (25) | 32.09 MiB / 35.73 MiB (25) | 101.27 MiB / 113.12 MiB (25) |
| host_anon_ready | 31.98 MiB / 34.79 MiB (25) | 106.05 MiB / 112.82 MiB (25) | 32.09 MiB / 35.73 MiB (25) | 101.27 MiB / 113.12 MiB (25) |
| host_cchild_ms_end | 890 / 940 (25) | 0 / 0 (25) | 890 / 950 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 890 / 940 (25) | 0 / 0 (25) | 890 / 950 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 230 / 240 (25) | 640 / 670 (25) | 230 / 250 (25) | 640 / 680 (25) |
| host_cpu_ms_ready | 230 / 240 (25) | 640 / 670 (25) | 230 / 250 (25) | 640 / 680 (25) |
| host_peak_rss_end | 73.38 MiB / 76.15 MiB (25) | 152.59 MiB / 159.33 MiB (25) | 73.72 MiB / 77.25 MiB (25) | 147.78 MiB / 159.68 MiB (25) |
| host_peak_rss_ready | 73.38 MiB / 76.15 MiB (25) | 152.59 MiB / 159.33 MiB (25) | 73.72 MiB / 77.25 MiB (25) | 147.78 MiB / 159.68 MiB (25) |
| host_pss_end | 71.96 MiB / 74.70 MiB (25) | 151.14 MiB / 157.91 MiB (25) | 72.32 MiB / 75.85 MiB (25) | 146.30 MiB / 158.33 MiB (25) |
| host_pss_ready | 71.96 MiB / 74.70 MiB (25) | 151.14 MiB / 157.91 MiB (25) | 72.32 MiB / 75.85 MiB (25) | 146.30 MiB / 158.33 MiB (25) |
| host_rss_end | 73.38 MiB / 76.15 MiB (25) | 152.59 MiB / 159.33 MiB (25) | 73.72 MiB / 77.25 MiB (25) | 147.78 MiB / 159.68 MiB (25) |
| host_rss_ready | 73.38 MiB / 76.15 MiB (25) | 152.59 MiB / 159.33 MiB (25) | 73.72 MiB / 77.25 MiB (25) | 147.78 MiB / 159.68 MiB (25) |
| host_threads_end | 7 / 7 (25) | 12 / 12 (25) | 7 / 7 (25) | 12 / 14 (25) |
| host_threads_ready | 7 / 7 (25) | 12 / 12 (25) | 7 / 7 (25) | 12 / 14 (25) |
| mallocs_end | 12788 / 12802 (25) | - | 12992 / 13036 (25) | - |
| mallocs_ready | 12600 / 12613 (25) | - | 12805 / 12846 (25) | - |
| mallocs_workload | 188 / 194 (25) | - | 187 / 194 (25) | - |
| num_gc_end | 2 / 2 (25) | 11 / 11 (25) | 2 / 2 (25) | 11 / 11 (25) |
| num_gc_ready | 0 / 0 (25) | 10 / 10 (25) | 0 / 0 (25) | 10 / 10 (25) |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) | 301.98 MiB / 304.50 MiB (25) | 12.28 MiB / 12.46 MiB (25) | 302.02 MiB / 304.50 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.60 MiB (25) | 301.98 MiB / 304.50 MiB (25) | 13.40 MiB / 13.66 MiB (25) | 302.02 MiB / 304.50 MiB (25) |
| total_alloc_end | 1.08 MiB / 1.08 MiB (25) | - | 1.07 MiB / 1.08 MiB (25) | - |
| total_alloc_ready | 1.04 MiB / 1.05 MiB (25) | - | 1.03 MiB / 1.04 MiB (25) | - |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) | - | 0.04 MiB / 0.04 MiB (25) | - |
| tree_pss_ready | 81.28 MiB / 84.00 MiB (25) | 458.49 MiB / 467.87 MiB (25) | 81.67 MiB / 85.31 MiB (25) | 455.43 MiB / 468.04 MiB (25) |
| tree_rss_ready | 82.64 MiB / 85.45 MiB (25) | 463.91 MiB / 473.21 MiB (25) | 83.06 MiB / 86.72 MiB (25) | 460.76 MiB / 473.34 MiB (25) |

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
| cpu_total_ms_ready | 90 / 100 (25) | 1180 / 1220 (25) | 90 / 100 (25) | 1160 / 1210 (25) |
| cpu_wait4_ms | 102.8 / 124.9 (25) | 213.9 / 231.3 (25) | 103.7 / 127.2 (25) | 215.0 / 225.2 (25) |
| ext_anon_ready | 2.48 MiB / 2.67 MiB (25) | 105.54 MiB / 106.34 MiB (25) | 2.50 MiB / 2.71 MiB (25) | 104.99 MiB / 106.43 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) | 1000 / 1020 (25) | 10 / 10 (25) | 970 / 1020 (25) |
| ext_peak_rss_ready | 9.40 MiB / 9.59 MiB (25) | 161.08 MiB / 161.93 MiB (25) | 9.36 MiB / 9.58 MiB (25) | 160.73 MiB / 161.91 MiB (25) |
| ext_rchar_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_syscr_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_syscw_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_threads_end | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_wchar_workload | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| heap_inuse_asis_ready | 2.01 MiB / 2.28 MiB (25) | 63.59 MiB / 98.09 MiB (25) | 2.24 MiB / 2.32 MiB (25) | 63.59 MiB / 98.09 MiB (25) |
| heap_inuse_gc_ready | 1.48 MiB / 1.60 MiB (25) | 70.09 MiB / 101.59 MiB (25) | 1.53 MiB / 1.57 MiB (25) | 70.09 MiB / 101.09 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.05 MiB (25) | 41.45 MiB / 41.56 MiB (25) | 1.04 MiB / 1.04 MiB (25) | 41.58 MiB / 41.65 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) | 35.15 MiB / 35.15 MiB (25) | 0.50 MiB / 0.50 MiB (25) | 35.16 MiB / 35.16 MiB (25) |
| host_anon_end | 19.78 MiB / 20.43 MiB (25) | 43.16 MiB / 43.36 MiB (25) | 19.73 MiB / 20.42 MiB (25) | 43.20 MiB / 43.39 MiB (25) |
| host_anon_ready | 19.78 MiB / 20.43 MiB (25) | 43.16 MiB / 43.36 MiB (25) | 19.73 MiB / 20.42 MiB (25) | 43.20 MiB / 43.39 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 80 / 90 (25) | 180 / 200 (25) | 80 / 90 (25) | 180 / 200 (25) |
| host_cpu_ms_ready | 80 / 90 (25) | 180 / 200 (25) | 80 / 90 (25) | 180 / 200 (25) |
| host_peak_rss_end | 60.52 MiB / 61.17 MiB (25) | 84.13 MiB / 84.59 MiB (25) | 60.83 MiB / 61.49 MiB (25) | 84.33 MiB / 84.50 MiB (25) |
| host_peak_rss_ready | 60.52 MiB / 61.17 MiB (25) | 84.13 MiB / 84.59 MiB (25) | 60.83 MiB / 61.49 MiB (25) | 84.33 MiB / 84.50 MiB (25) |
| host_pss_end | 59.17 MiB / 59.77 MiB (25) | 82.71 MiB / 83.20 MiB (25) | 59.46 MiB / 60.13 MiB (25) | 82.89 MiB / 83.14 MiB (25) |
| host_pss_ready | 59.17 MiB / 59.77 MiB (25) | 82.71 MiB / 83.20 MiB (25) | 59.46 MiB / 60.13 MiB (25) | 82.89 MiB / 83.14 MiB (25) |
| host_rss_end | 60.52 MiB / 61.17 MiB (25) | 84.13 MiB / 84.59 MiB (25) | 60.83 MiB / 61.49 MiB (25) | 84.33 MiB / 84.50 MiB (25) |
| host_rss_ready | 60.52 MiB / 61.17 MiB (25) | 84.13 MiB / 84.59 MiB (25) | 60.83 MiB / 61.49 MiB (25) | 84.33 MiB / 84.50 MiB (25) |
| host_threads_end | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) |
| host_threads_ready | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) |
| mallocs_end | 12869 / 12895 (25) | - | 13108 / 13134 (25) | - |
| mallocs_ready | 12682 / 12709 (25) | - | 12921 / 12946 (25) | - |
| mallocs_workload | 187 / 190 (25) | - | 183 / 189 (25) | - |
| num_gc_end | 2 / 2 (25) | 8 / 8 (25) | 2 / 2 (25) | 8 / 8 (25) |
| num_gc_ready | 0 / 0 (25) | 7 / 7 (25) | 0 / 0 (25) | 7 / 7 (25) |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) | 151.02 MiB / 156.00 MiB (25) | 12.21 MiB / 12.46 MiB (25) | 150.00 MiB / 154.50 MiB (25) |
| sys_gc_ready | 13.40 MiB / 13.66 MiB (25) | 149.09 MiB / 154.30 MiB (25) | 13.41 MiB / 13.66 MiB (25) | 149.61 MiB / 152.32 MiB (25) |
| total_alloc_end | 1.08 MiB / 1.09 MiB (25) | - | 1.07 MiB / 1.08 MiB (25) | - |
| total_alloc_ready | 1.04 MiB / 1.05 MiB (25) | - | 1.04 MiB / 1.04 MiB (25) | - |
| total_alloc_workload | 0.04 MiB / 0.04 MiB (25) | - | 0.04 MiB / 0.04 MiB (25) | - |
| tree_pss_ready | 68.55 MiB / 69.34 MiB (25) | 240.09 MiB / 240.77 MiB (25) | 68.82 MiB / 69.63 MiB (25) | 239.67 MiB / 240.93 MiB (25) |
| tree_rss_ready | 70.00 MiB / 70.67 MiB (25) | 245.41 MiB / 246.10 MiB (25) | 70.17 MiB / 71.06 MiB (25) | 245.02 MiB / 246.19 MiB (25) |

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
| cpu_total_ms_ready | 1160 / 1200 (25) | 3880 / 4180 (25) | 1190 / 1210 (25) | 3830 / 4170 (25) |
| cpu_wait4_ms | 1219.3 / 1276.7 (25) | 742.4 / 767.4 (25) | 1248.4 / 1282.0 (25) | 708.3 / 746.8 (25) |
| ext_anon_ready | 2.45 MiB / 2.66 MiB (25) | 255.45 MiB / 257.71 MiB (25) | 2.37 MiB / 2.58 MiB (25) | 255.70 MiB / 258.17 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 20 (25) | 3220 / 3490 (25) | 10 / 10 (25) | 3190 / 3490 (25) |
| ext_peak_rss_ready | 9.37 MiB / 9.57 MiB (25) | 311.09 MiB / 313.46 MiB (25) | 9.27 MiB / 9.45 MiB (25) | 311.45 MiB / 313.80 MiB (25) |
| ext_rchar_workload | 134749 / 134965 (25) | 149658 / 149841 (25) | 134672 / 134672 (25) | 149617 / 149824 (25) |
| ext_syscr_workload | 115 / 124 (25) | 163 / 205 (25) | 115 / 116 (25) | 161 / 187 (25) |
| ext_syscw_workload | 99 / 103 (25) | 183 / 226 (25) | 99 / 99 (25) | 177 / 208 (25) |
| ext_threads_end | 5 / 9 (25) | 12 / 12 (25) | 5 / 6 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_wchar_workload | 38126 / 38558 (25) | 33965 / 34293 (25) | 38112 / 38112 (25) | 33917 / 34168 (25) |
| heap_inuse_asis_ready | 2.02 MiB / 2.28 MiB (25) | 193.75 MiB / 195.00 MiB (25) | 2.29 MiB / 2.52 MiB (25) | 193.75 MiB / 195.00 MiB (25) |
| heap_inuse_gc_ready | 1.45 MiB / 1.58 MiB (25) | 181.25 MiB / 181.50 MiB (25) | 1.52 MiB / 1.63 MiB (25) | 181.50 MiB / 182.25 MiB (25) |
| heap_used_asis_ready | 1.05 MiB / 1.05 MiB (25) | 49.85 MiB / 49.98 MiB (25) | 1.04 MiB / 1.05 MiB (25) | 49.86 MiB / 49.99 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) | 48.58 MiB / 48.58 MiB (25) | 0.50 MiB / 0.50 MiB (25) | 48.59 MiB / 48.59 MiB (25) |
| host_anon_end | 30.95 MiB / 32.60 MiB (25) | 117.09 MiB / 117.20 MiB (25) | 30.33 MiB / 32.93 MiB (25) | 117.23 MiB / 117.40 MiB (25) |
| host_anon_ready | 31.68 MiB / 32.80 MiB (25) | 113.27 MiB / 113.39 MiB (25) | 30.67 MiB / 32.59 MiB (25) | 113.43 MiB / 113.65 MiB (25) |
| host_cchild_ms_end | 910 / 950 (25) | 0 / 0 (25) | 940 / 950 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 910 / 950 (25) | 0 / 0 (25) | 940 / 950 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 270 / 280 (25) | 700 / 720 (25) | 270 / 290 (25) | 670 / 700 (25) |
| host_cpu_ms_ready | 240 / 250 (25) | 670 / 690 (25) | 240 / 250 (25) | 650 / 670 (25) |
| host_peak_rss_end | 74.38 MiB / 76.08 MiB (25) | 164.78 MiB / 165.19 MiB (25) | 73.52 MiB / 76.20 MiB (25) | 165.20 MiB / 165.61 MiB (25) |
| host_peak_rss_ready | 73.18 MiB / 74.24 MiB (25) | 159.80 MiB / 160.22 MiB (25) | 72.62 MiB / 74.33 MiB (25) | 160.11 MiB / 160.50 MiB (25) |
| host_pss_end | 73.04 MiB / 74.75 MiB (25) | 163.36 MiB / 163.82 MiB (25) | 72.16 MiB / 74.84 MiB (25) | 163.80 MiB / 164.21 MiB (25) |
| host_pss_ready | 71.83 MiB / 72.89 MiB (25) | 158.39 MiB / 158.83 MiB (25) | 71.22 MiB / 72.97 MiB (25) | 158.71 MiB / 159.13 MiB (25) |
| host_rss_end | 74.38 MiB / 76.08 MiB (25) | 164.78 MiB / 165.19 MiB (25) | 73.52 MiB / 76.20 MiB (25) | 165.20 MiB / 165.61 MiB (25) |
| host_rss_ready | 73.18 MiB / 74.24 MiB (25) | 159.80 MiB / 160.22 MiB (25) | 72.62 MiB / 74.33 MiB (25) | 160.11 MiB / 160.50 MiB (25) |
| host_threads_end | 8 / 8 (25) | 12 / 12 (25) | 8 / 8 (25) | 12 / 13 (25) |
| host_threads_ready | 7 / 7 (25) | 12 / 12 (25) | 7 / 7 (25) | 12 / 13 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) |
| mallocs_end | 26564 / 27064 (25) | - | 22512 / 23044 (25) | - |
| mallocs_ready | 12687 / 12711 (25) | - | 12895 / 12944 (25) | - |
| mallocs_workload | 13866 / 14374 (25) | - | 9617 / 10144 (25) | - |
| num_gc_end | 2 / 2 (25) | 11 / 11 (25) | 2 / 2 (25) | 11 / 11 (25) |
| num_gc_ready | 0 / 0 (25) | 10 / 10 (25) | 0 / 0 (25) | 10 / 10 (25) |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) | 301.50 MiB / 303.40 MiB (25) | 12.46 MiB / 12.46 MiB (25) | 301.90 MiB / 304.50 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (25) | 301.50 MiB / 303.40 MiB (25) | 13.41 MiB / 17.17 MiB (25) | 301.90 MiB / 304.50 MiB (25) |
| tool_ms:get_context_remaining | 4.9 / 6.5 (25) | 10.9 / 14.2 (25) | 4.6 / 6.0 (25) | 10.4 / 13.9 (25) |
| total_alloc_end | 2.35 MiB / 2.40 MiB (25) | - | 2.16 MiB / 2.21 MiB (25) | - |
| total_alloc_ready | 1.05 MiB / 1.05 MiB (25) | - | 1.04 MiB / 1.05 MiB (25) | - |
| total_alloc_workload | 1.31 MiB / 1.35 MiB (25) | - | 1.12 MiB / 1.17 MiB (25) | - |
| tree_pss_ready | 81.27 MiB / 82.46 MiB (25) | 459.61 MiB / 467.56 MiB (25) | 80.73 MiB / 82.19 MiB (25) | 464.86 MiB / 468.40 MiB (25) |
| tree_rss_ready | 82.63 MiB / 83.81 MiB (25) | 464.98 MiB / 472.95 MiB (25) | 82.14 MiB / 83.59 MiB (25) | 470.14 MiB / 473.69 MiB (25) |

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
| cpu_total_ms_ready | 100 / 100 (25) | 1210 / 1220 (25) | 90 / 100 (25) | 1190 / 1210 (25) |
| cpu_wait4_ms | 136.1 / 171.1 (25) | 262.8 / 268.0 (25) | 140.2 / 185.5 (25) | 252.7 / 259.6 (25) |
| ext_anon_ready | 2.40 MiB / 2.64 MiB (25) | 105.43 MiB / 105.96 MiB (25) | 2.38 MiB / 2.62 MiB (25) | 105.52 MiB / 106.85 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) | 1010 / 1020 (25) | 10 / 10 (25) | 1000 / 1020 (25) |
| ext_peak_rss_ready | 9.32 MiB / 9.55 MiB (25) | 160.94 MiB / 161.52 MiB (25) | 9.24 MiB / 9.55 MiB (25) | 161.05 MiB / 162.48 MiB (25) |
| ext_rchar_workload | 134600 / 134816 (25) | 149391 / 149687 (25) | 134458 / 134674 (25) | 149435 / 149554 (25) |
| ext_syscr_workload | 115 / 124 (25) | 139 / 177 (25) | 115 / 124 (25) | 160 / 177 (25) |
| ext_syscw_workload | 99 / 103 (25) | 152 / 193 (25) | 99 / 103 (25) | 177 / 196 (25) |
| ext_threads_end | 5 / 9 (25) | 12 / 12 (25) | 5 / 9 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_wchar_workload | 38106 / 38538 (25) | 33709 / 34023 (25) | 38092 / 38524 (25) | 33912 / 34024 (25) |
| heap_inuse_asis_ready | 2.22 MiB / 2.31 MiB (25) | 63.84 MiB / 98.34 MiB (25) | 2.24 MiB / 2.32 MiB (25) | 63.59 MiB / 98.34 MiB (25) |
| heap_inuse_gc_ready | 1.55 MiB / 1.62 MiB (25) | 70.34 MiB / 101.59 MiB (25) | 1.52 MiB / 1.57 MiB (25) | 70.09 MiB / 101.59 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.05 MiB (25) | 41.36 MiB / 41.53 MiB (25) | 1.03 MiB / 1.05 MiB (25) | 41.52 MiB / 41.67 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) | 35.14 MiB / 35.15 MiB (25) | 0.50 MiB / 0.51 MiB (25) | 35.16 MiB / 35.16 MiB (25) |
| host_anon_end | 20.17 MiB / 20.83 MiB (25) | 47.06 MiB / 47.43 MiB (25) | 20.12 MiB / 20.70 MiB (25) | 47.06 MiB / 47.52 MiB (25) |
| host_anon_ready | 19.73 MiB / 20.39 MiB (25) | 43.13 MiB / 43.51 MiB (25) | 19.72 MiB / 20.29 MiB (25) | 43.16 MiB / 43.58 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 110 / 120 (25) | 230 / 240 (25) | 120 / 130 (25) | 220 / 230 (25) |
| host_cpu_ms_ready | 90 / 90 (25) | 200 / 200 (25) | 90 / 90 (25) | 180 / 190 (25) |
| host_peak_rss_end | 62.66 MiB / 63.33 MiB (25) | 89.44 MiB / 89.85 MiB (25) | 62.83 MiB / 63.28 MiB (25) | 89.60 MiB / 89.98 MiB (25) |
| host_peak_rss_ready | 60.72 MiB / 61.24 MiB (25) | 84.03 MiB / 84.41 MiB (25) | 60.82 MiB / 61.23 MiB (25) | 84.18 MiB / 84.60 MiB (25) |
| host_pss_end | 61.24 MiB / 61.97 MiB (25) | 88.02 MiB / 88.42 MiB (25) | 61.42 MiB / 61.83 MiB (25) | 88.18 MiB / 88.56 MiB (25) |
| host_pss_ready | 59.32 MiB / 59.86 MiB (25) | 82.62 MiB / 82.96 MiB (25) | 59.42 MiB / 59.78 MiB (25) | 82.77 MiB / 83.17 MiB (25) |
| host_rss_end | 62.66 MiB / 63.33 MiB (25) | 89.44 MiB / 89.85 MiB (25) | 62.83 MiB / 63.28 MiB (25) | 89.60 MiB / 89.98 MiB (25) |
| host_rss_ready | 60.72 MiB / 61.24 MiB (25) | 84.03 MiB / 84.41 MiB (25) | 60.82 MiB / 61.23 MiB (25) | 84.18 MiB / 84.60 MiB (25) |
| host_threads_end | 8 / 8 (25) | 8 / 8 (25) | 8 / 8 (25) | 8 / 8 (25) |
| host_threads_ready | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) |
| llm_requests | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) | 2 / 2 (25) |
| mallocs_end | 26572 / 27070 (25) | - | 22541 / 23413 (25) | - |
| mallocs_ready | 12688 / 12704 (25) | - | 12924 / 12956 (25) | - |
| mallocs_workload | 13881 / 14368 (25) | - | 9599 / 10541 (25) | - |
| num_gc_end | 2 / 2 (25) | 8 / 8 (25) | 2 / 2 (25) | 8 / 8 (25) |
| num_gc_ready | 0 / 0 (25) | 7 / 7 (25) | 0 / 0 (25) | 7 / 7 (25) |
| num_gc_workload | 2 / 2 (25) | 1 / 1 (25) | 2 / 2 (25) | 1 / 1 (25) |
| sys_asis_ready | 12.46 MiB / 12.46 MiB (25) | 151.50 MiB / 156.00 MiB (25) | 12.46 MiB / 12.46 MiB (25) | 150.00 MiB / 156.00 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (25) | 151.03 MiB / 155.67 MiB (25) | 13.41 MiB / 13.66 MiB (25) | 149.53 MiB / 153.54 MiB (25) |
| tool_ms:get_context_remaining | 4.8 / 6.6 (25) | 12.9 / 14.4 (25) | 4.6 / 6.5 (25) | 12.1 / 14.8 (25) |
| total_alloc_end | 2.36 MiB / 2.40 MiB (25) | - | 2.15 MiB / 2.24 MiB (25) | - |
| total_alloc_ready | 1.04 MiB / 1.05 MiB (25) | - | 1.03 MiB / 1.05 MiB (25) | - |
| total_alloc_workload | 1.32 MiB / 1.36 MiB (25) | - | 1.12 MiB / 1.21 MiB (25) | - |
| tree_pss_ready | 68.68 MiB / 69.19 MiB (25) | 239.74 MiB / 240.27 MiB (25) | 68.68 MiB / 69.19 MiB (25) | 240.08 MiB / 241.44 MiB (25) |
| tree_rss_ready | 70.10 MiB / 70.59 MiB (25) | 244.96 MiB / 245.55 MiB (25) | 70.04 MiB / 70.58 MiB (25) | 245.39 MiB / 246.73 MiB (25) |

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
| cpu_total_ms_ready | 90 / 100 (25) | 1090 / 1210 (25) | 90 / 100 (25) | 1120 / 1210 (25) |
| cpu_wait4_ms | 593.9 / 1113.8 (25) | 592.7 / 642.2 (25) | 548.4 / 808.9 (25) | 557.2 / 631.5 (25) |
| ext_anon_ready | 2.42 MiB / 2.61 MiB (25) | 105.30 MiB / 106.53 MiB (25) | 2.48 MiB / 2.61 MiB (25) | 105.72 MiB / 106.81 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) | 910 / 1020 (25) | 10 / 10 (25) | 940 / 1020 (25) |
| ext_peak_rss_ready | 9.36 MiB / 9.57 MiB (25) | 160.83 MiB / 162.06 MiB (25) | 9.36 MiB / 9.48 MiB (25) | 161.22 MiB / 162.24 MiB (25) |
| ext_rchar_workload | 5416480 / 5416704 (25) | 5549791 / 5551025 (25) | 5417632 / 5417760 (25) | 5551011 / 5551928 (25) |
| ext_syscr_workload | 2459 / 2483 (25) | 3190 / 3364 (25) | 2446 / 2463 (25) | 3187 / 3345 (25) |
| ext_syscw_workload | 2088 / 2114 (25) | 3395 / 3543 (25) | 2075 / 2094 (25) | 3386 / 3530 (25) |
| ext_threads_end | 10 / 10 (25) | 12 / 12 (25) | 10 / 10 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_wchar_workload | 1124035 / 1124334 (25) | 1045472 / 1046690 (25) | 1123893 / 1124292 (25) | 1045708 / 1046641 (25) |
| heap_inuse_asis_ready | 2.04 MiB / 2.38 MiB (25) | 63.59 MiB / 98.09 MiB (25) | 2.24 MiB / 2.42 MiB (25) | 63.59 MiB / 98.09 MiB (25) |
| heap_inuse_gc_ready | 1.52 MiB / 1.61 MiB (25) | 70.09 MiB / 101.59 MiB (25) | 1.52 MiB / 1.65 MiB (25) | 69.84 MiB / 101.59 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.04 MiB (25) | 41.40 MiB / 41.58 MiB (25) | 1.03 MiB / 1.04 MiB (25) | 41.45 MiB / 41.62 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) | 35.15 MiB / 35.15 MiB (25) | 0.50 MiB / 0.51 MiB (25) | 35.16 MiB / 35.16 MiB (25) |
| host_anon_end | 28.14 MiB / 29.06 MiB (25) | 68.69 MiB / 71.87 MiB (25) | 28.78 MiB / 30.53 MiB (25) | 69.09 MiB / 72.64 MiB (25) |
| host_anon_ready | 19.77 MiB / 20.45 MiB (25) | 43.31 MiB / 43.56 MiB (25) | 19.72 MiB / 20.40 MiB (25) | 43.54 MiB / 43.73 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 560 / 580 (25) | 550 / 610 (25) | 480 / 560 (25) | 530 / 600 (25) |
| host_cpu_ms_ready | 80 / 90 (25) | 180 / 190 (25) | 80 / 90 (25) | 180 / 190 (25) |
| host_peak_rss_end | 70.91 MiB / 72.02 MiB (25) | 111.50 MiB / 114.70 MiB (25) | 71.76 MiB / 73.38 MiB (25) | 111.79 MiB / 115.66 MiB (25) |
| host_peak_rss_ready | 60.70 MiB / 61.46 MiB (25) | 84.11 MiB / 84.71 MiB (25) | 60.76 MiB / 61.50 MiB (25) | 84.36 MiB / 84.88 MiB (25) |
| host_pss_end | 69.51 MiB / 70.62 MiB (25) | 110.07 MiB / 113.25 MiB (25) | 70.40 MiB / 72.00 MiB (25) | 110.36 MiB / 114.23 MiB (25) |
| host_pss_ready | 59.30 MiB / 60.04 MiB (25) | 82.73 MiB / 83.27 MiB (25) | 59.31 MiB / 60.11 MiB (25) | 82.93 MiB / 83.49 MiB (25) |
| host_rss_end | 70.91 MiB / 72.02 MiB (25) | 111.50 MiB / 114.70 MiB (25) | 71.76 MiB / 73.38 MiB (25) | 111.79 MiB / 115.66 MiB (25) |
| host_rss_ready | 60.70 MiB / 61.46 MiB (25) | 84.11 MiB / 84.71 MiB (25) | 60.76 MiB / 61.50 MiB (25) | 84.36 MiB / 84.88 MiB (25) |
| host_threads_end | 8 / 8 (25) | 8 / 8 (25) | 8 / 8 (25) | 8 / 8 (25) |
| host_threads_ready | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) |
| llm_requests | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) |
| mallocs_end | 847690 / 849320 (25) | - | 509044 / 510856 (25) | - |
| mallocs_ready | 12567 / 12586 (25) | - | 12785 / 12831 (25) | - |
| mallocs_workload | 835118 / 836747 (25) | - | 496264 / 498033 (25) | - |
| num_gc_end | 32 / 34 (25) | 11 / 11 (25) | 28 / 30 (25) | 11 / 11 (25) |
| num_gc_ready | 0 / 0 (25) | 7 / 7 (25) | 0 / 0 (25) | 7 / 7 (25) |
| num_gc_workload | 32 / 34 (25) | 4 / 4 (25) | 28 / 30 (25) | 4 / 4 (25) |
| sys_asis_ready | 12.46 MiB / 12.53 MiB (25) | 151.50 MiB / 156.00 MiB (25) | 12.46 MiB / 12.46 MiB (25) | 150.00 MiB / 156.00 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.65 MiB (25) | 150.00 MiB / 156.00 MiB (25) | 13.36 MiB / 13.66 MiB (25) | 149.82 MiB / 155.64 MiB (25) |
| tool_ms:notes.append | 5.8 / 7.6 (150) | 7.8 / 11.3 (150) | 5.0 / 6.9 (150) | 7.4 / 11.2 (150) |
| tool_ms:notes.list | 5.8 / 7.5 (150) | 9.0 / 14.8 (150) | 4.9 / 7.4 (150) | 8.8 / 15.0 (150) |
| tool_ms:notes.read | 5.4 / 7.5 (150) | 7.9 / 12.1 (150) | 4.2 / 6.4 (150) | 8.0 / 12.0 (150) |
| tool_ms:notes.search | 5.5 / 7.5 (150) | 8.9 / 12.4 (150) | 4.7 / 7.0 (150) | 8.7 / 12.2 (150) |
| tool_ms:notes.write | 6.1 / 7.8 (150) | 10.1 / 14.0 (150) | 5.1 / 6.6 (150) | 9.7 / 13.9 (150) |
| total_alloc_end | 58.32 MiB / 58.74 MiB (25) | - | 48.77 MiB / 49.17 MiB (25) | - |
| total_alloc_ready | 1.04 MiB / 1.04 MiB (25) | - | 1.03 MiB / 1.04 MiB (25) | - |
| total_alloc_workload | 57.29 MiB / 57.70 MiB (25) | - | 47.73 MiB / 48.13 MiB (25) | - |
| tree_pss_ready | 68.73 MiB / 69.52 MiB (25) | 239.89 MiB / 241.12 MiB (25) | 68.73 MiB / 69.39 MiB (25) | 240.32 MiB / 241.34 MiB (25) |
| tree_rss_ready | 70.11 MiB / 70.95 MiB (25) | 245.16 MiB / 246.35 MiB (25) | 70.08 MiB / 70.75 MiB (25) | 245.60 MiB / 246.60 MiB (25) |

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
| cpu_total_ms_ready | 90 / 100 (25) | 1140 / 1220 (25) | 90 / 100 (25) | 1120 / 1210 (25) |
| cpu_wait4_ms | 645.7 / 731.8 (25) | 645.2 / 718.7 (25) | 600.2 / 999.7 (25) | 641.6 / 672.2 (25) |
| ext_anon_ready | 2.42 MiB / 2.63 MiB (25) | 105.20 MiB / 106.15 MiB (25) | 2.41 MiB / 2.61 MiB (25) | 105.36 MiB / 106.26 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) | 970 / 1020 (25) | 10 / 10 (25) | 940 / 1020 (25) |
| ext_peak_rss_ready | 9.36 MiB / 9.55 MiB (25) | 160.69 MiB / 161.78 MiB (25) | 9.30 MiB / 9.49 MiB (25) | 160.91 MiB / 161.77 MiB (25) |
| ext_rchar_workload | 9180812 / 9180975 (25) | 9351382 / 9352104 (25) | 9181252 / 9181535 (25) | 9351967 / 9352312 (25) |
| ext_syscr_workload | 3710 / 3727 (25) | 4743 / 4863 (25) | 3693 / 3724 (25) | 4733 / 4809 (25) |
| ext_syscw_workload | 2126 / 2145 (25) | 4286 / 4403 (25) | 2110 / 2143 (25) | 4283 / 4346 (25) |
| ext_threads_end | 10 / 10 (25) | 12 / 12 (25) | 10 / 10 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_wchar_workload | 1665525 / 1666024 (25) | 1593824 / 1594539 (25) | 1665764 / 1666246 (25) | 1593950 / 1594345 (25) |
| heap_inuse_asis_ready | 2.07 MiB / 2.29 MiB (25) | 63.59 MiB / 98.09 MiB (25) | 2.27 MiB / 2.38 MiB (25) | 63.59 MiB / 98.34 MiB (25) |
| heap_inuse_gc_ready | 1.48 MiB / 1.59 MiB (25) | 70.09 MiB / 101.59 MiB (25) | 1.54 MiB / 1.61 MiB (25) | 70.09 MiB / 101.84 MiB (25) |
| heap_used_asis_ready | 1.04 MiB / 1.04 MiB (25) | 41.39 MiB / 41.55 MiB (25) | 1.03 MiB / 1.04 MiB (25) | 41.47 MiB / 41.67 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) | 35.15 MiB / 35.15 MiB (25) | 0.50 MiB / 0.51 MiB (25) | 35.16 MiB / 35.16 MiB (25) |
| host_anon_end | 29.50 MiB / 30.19 MiB (25) | 65.48 MiB / 70.86 MiB (25) | 30.23 MiB / 31.20 MiB (25) | 65.46 MiB / 72.14 MiB (25) |
| host_anon_ready | 19.70 MiB / 20.43 MiB (25) | 43.27 MiB / 43.54 MiB (25) | 19.72 MiB / 20.39 MiB (25) | 43.29 MiB / 43.61 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 620 / 670 (25) | 610 / 680 (25) | 570 / 600 (25) | 600 / 630 (25) |
| host_cpu_ms_ready | 80 / 90 (25) | 190 / 200 (25) | 80 / 90 (25) | 180 / 200 (25) |
| host_peak_rss_end | 72.26 MiB / 73.07 MiB (25) | 108.20 MiB / 113.46 MiB (25) | 73.00 MiB / 74.07 MiB (25) | 108.18 MiB / 114.66 MiB (25) |
| host_peak_rss_ready | 60.66 MiB / 61.25 MiB (25) | 84.16 MiB / 84.40 MiB (25) | 60.82 MiB / 61.12 MiB (25) | 84.21 MiB / 84.61 MiB (25) |
| host_pss_end | 70.90 MiB / 71.72 MiB (25) | 106.81 MiB / 111.99 MiB (25) | 71.63 MiB / 72.72 MiB (25) | 106.80 MiB / 113.28 MiB (25) |
| host_pss_ready | 59.25 MiB / 59.91 MiB (25) | 82.76 MiB / 83.04 MiB (25) | 59.45 MiB / 59.71 MiB (25) | 82.84 MiB / 83.23 MiB (25) |
| host_rss_end | 72.26 MiB / 73.07 MiB (25) | 108.20 MiB / 113.46 MiB (25) | 73.00 MiB / 74.07 MiB (25) | 108.18 MiB / 114.66 MiB (25) |
| host_rss_ready | 60.66 MiB / 61.25 MiB (25) | 84.16 MiB / 84.40 MiB (25) | 60.82 MiB / 61.12 MiB (25) | 84.21 MiB / 84.61 MiB (25) |
| host_threads_end | 8 / 8 (25) | 8 / 8 (25) | 8 / 8 (25) | 8 / 8 (25) |
| host_threads_ready | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) |
| llm_requests | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) | 31 / 31 (25) |
| mallocs_end | 927598 / 929583 (25) | - | 591377 / 593649 (25) | - |
| mallocs_ready | 12570 / 12579 (25) | - | 12772 / 12829 (25) | - |
| mallocs_workload | 915027 / 917012 (25) | - | 578618 / 580872 (25) | - |
| num_gc_end | 48 / 49 (25) | 13 / 13 (25) | 41 / 42 (25) | 13 / 13 (25) |
| num_gc_ready | 0 / 0 (25) | 7 / 7 (25) | 0 / 0 (25) | 7 / 7 (25) |
| num_gc_workload | 48 / 49 (25) | 6 / 6 (25) | 41 / 42 (25) | 6 / 6 (25) |
| sys_asis_ready | 12.21 MiB / 12.46 MiB (25) | 151.50 MiB / 156.00 MiB (25) | 12.46 MiB / 12.53 MiB (25) | 151.50 MiB / 156.00 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (25) | 150.11 MiB / 155.54 MiB (25) | 13.36 MiB / 13.66 MiB (25) | 149.93 MiB / 154.50 MiB (25) |
| tool_ms:notes.append | 5.9 / 7.6 (150) | 7.7 / 11.2 (150) | 5.5 / 7.2 (150) | 7.5 / 11.1 (150) |
| tool_ms:notes.list | 10.4 / 13.2 (150) | 21.0 / 30.6 (150) | 9.1 / 11.9 (150) | 20.6 / 29.1 (150) |
| tool_ms:notes.read | 5.9 / 7.7 (150) | 7.9 / 11.2 (150) | 5.0 / 6.9 (150) | 7.8 / 11.7 (150) |
| tool_ms:notes.search | 11.0 / 13.4 (150) | 31.0 / 41.5 (150) | 9.9 / 13.0 (150) | 29.2 / 39.1 (150) |
| tool_ms:notes.write | 6.3 / 8.2 (150) | 9.7 / 14.1 (150) | 5.7 / 7.0 (150) | 9.5 / 13.5 (150) |
| total_alloc_end | 85.43 MiB / 85.70 MiB (25) | - | 68.18 MiB / 68.95 MiB (25) | - |
| total_alloc_ready | 1.04 MiB / 1.04 MiB (25) | - | 1.03 MiB / 1.04 MiB (25) | - |
| total_alloc_workload | 84.39 MiB / 84.67 MiB (25) | - | 67.16 MiB / 67.92 MiB (25) | - |
| tree_pss_ready | 68.59 MiB / 69.37 MiB (25) | 239.56 MiB / 240.89 MiB (25) | 68.77 MiB / 69.18 MiB (25) | 239.90 MiB / 240.97 MiB (25) |
| tree_rss_ready | 69.95 MiB / 70.74 MiB (25) | 244.88 MiB / 246.12 MiB (25) | 70.12 MiB / 70.55 MiB (25) | 245.17 MiB / 246.25 MiB (25) |

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
| cpu_total_ms_ready | 90 / 100 (25) | 1090 / 1210 (25) | 90 / 100 (25) | 1190 / 1210 (25) |
| cpu_wait4_ms | 10688.6 / 11013.3 (25) | 9455.0 / 9646.2 (25) | 7984.9 / 8346.9 (25) | 7041.6 / 7306.6 (25) |
| ext_anon_ready | 2.45 MiB / 2.62 MiB (25) | 105.69 MiB / 106.57 MiB (25) | 2.44 MiB / 2.60 MiB (25) | 105.77 MiB / 106.17 MiB (25) |
| ext_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| ext_count | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) | 1 / 1 (25) |
| ext_cpu_ms_ready | 10 / 10 (25) | 910 / 1020 (25) | 10 / 10 (25) | 1000 / 1020 (25) |
| ext_peak_rss_ready | 9.37 MiB / 9.55 MiB (25) | 161.29 MiB / 162.00 MiB (25) | 9.33 MiB / 9.47 MiB (25) | 161.27 MiB / 161.82 MiB (25) |
| ext_rchar_workload | 283777854 / 283778534 (25) | 276105689 / 276126285 (25) | 283774374 / 283775146 (25) | 276113810 / 276137620 (25) |
| ext_syscr_workload | 22465 / 22590 (25) | 26093 / 28786 (25) | 22405 / 22555 (25) | 27605 / 30712 (25) |
| ext_syscw_workload | 19225 / 19295 (25) | 23923 / 26542 (25) | 19159 / 19291 (25) | 25423 / 28728 (25) |
| ext_threads_end | 10 / 11 (25) | 12 / 12 (25) | 10 / 10 (25) | 12 / 12 (25) |
| ext_threads_ready | 5 / 5 (25) | 12 / 12 (25) | 5 / 5 (25) | 12 / 12 (25) |
| ext_wchar_workload | 72099480 / 72101520 (25) | 71065544 / 71086875 (25) | 72092562 / 72094338 (25) | 71074498 / 71099718 (25) |
| heap_inuse_asis_ready | 2.14 MiB / 2.40 MiB (25) | 63.59 MiB / 98.34 MiB (25) | 2.26 MiB / 2.35 MiB (25) | 63.59 MiB / 98.34 MiB (25) |
| heap_inuse_gc_ready | 1.52 MiB / 1.66 MiB (25) | 70.34 MiB / 101.59 MiB (25) | 1.50 MiB / 1.62 MiB (25) | 70.09 MiB / 101.59 MiB (25) |
| heap_used_asis_ready | 1.05 MiB / 1.05 MiB (25) | 41.39 MiB / 41.55 MiB (25) | 1.04 MiB / 1.05 MiB (25) | 41.53 MiB / 41.62 MiB (25) |
| heap_used_gc_ready | 0.50 MiB / 0.50 MiB (25) | 35.15 MiB / 35.15 MiB (25) | 0.50 MiB / 0.51 MiB (25) | 35.16 MiB / 35.16 MiB (25) |
| host_anon_end | 70.32 MiB / 73.27 MiB (25) | 88.45 MiB / 92.13 MiB (25) | 67.86 MiB / 74.41 MiB (25) | 88.78 MiB / 92.52 MiB (25) |
| host_anon_ready | 19.78 MiB / 20.43 MiB (25) | 43.54 MiB / 43.62 MiB (25) | 19.69 MiB / 20.38 MiB (25) | 43.19 MiB / 43.60 MiB (25) |
| host_cchild_ms_end | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cchild_ms_ready | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| host_cpu_ms_end | 10660 / 10990 (25) | 9420 / 9610 (25) | 7940 / 8280 (25) | 7010 / 7260 (25) |
| host_cpu_ms_ready | 80 / 90 (25) | 180 / 200 (25) | 80 / 90 (25) | 190 / 200 (25) |
| host_peak_rss_end | 113.34 MiB / 116.38 MiB (25) | 131.86 MiB / 136.36 MiB (25) | 112.88 MiB / 117.71 MiB (25) | 132.23 MiB / 135.53 MiB (25) |
| host_peak_rss_ready | 60.75 MiB / 61.09 MiB (25) | 84.19 MiB / 84.52 MiB (25) | 60.85 MiB / 61.28 MiB (25) | 84.37 MiB / 84.70 MiB (25) |
| host_pss_end | 111.91 MiB / 115.02 MiB (25) | 130.35 MiB / 133.50 MiB (25) | 109.70 MiB / 116.31 MiB (25) | 130.41 MiB / 134.15 MiB (25) |
| host_pss_ready | 59.34 MiB / 59.71 MiB (25) | 82.76 MiB / 83.08 MiB (25) | 59.45 MiB / 59.92 MiB (25) | 82.96 MiB / 83.28 MiB (25) |
| host_rss_end | 113.34 MiB / 116.38 MiB (25) | 131.76 MiB / 134.86 MiB (25) | 111.04 MiB / 117.71 MiB (25) | 131.82 MiB / 135.53 MiB (25) |
| host_rss_ready | 60.75 MiB / 61.09 MiB (25) | 84.19 MiB / 84.52 MiB (25) | 60.85 MiB / 61.28 MiB (25) | 84.37 MiB / 84.70 MiB (25) |
| host_threads_end | 9 / 9 (25) | 9 / 9 (25) | 8 / 9 (25) | 9 / 9 (25) |
| host_threads_ready | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) | 7 / 7 (25) |
| llm_requests | 200 / 200 (25) | 200 / 200 (25) | 200 / 200 (25) | 200 / 200 (25) |
| mallocs_end | 12196516 / 12199224 (25) | - | 7332131 / 7341764 (25) | - |
| mallocs_ready | 12691 / 12718 (25) | - | 12900 / 12948 (25) | - |
| mallocs_workload | 12183835 / 12186583 (25) | - | 7319241 / 7328816 (25) | - |
| num_gc_end | 1006 / 1014 (25) | 36 / 53 (25) | 768 / 776 (25) | 51 / 66 (25) |
| num_gc_ready | 0 / 0 (25) | 7 / 7 (25) | 0 / 0 (25) | 7 / 7 (25) |
| num_gc_workload | 1006 / 1014 (25) | 29 / 46 (25) | 768 / 776 (25) | 44 / 59 (25) |
| session_compaction | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) |
| session_context_window | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) | 0 / 0 (25) |
| session_custom_message | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) | 3 / 3 (25) |
| session_entries | 531 / 531 (25) | 531 / 531 (25) | 531 / 531 (25) | 531 / 531 (25) |
| session_tool_results | 180 / 180 (25) | 180 / 180 (25) | 180 / 180 (25) | 180 / 180 (25) |
| sys_asis_ready | 12.28 MiB / 12.53 MiB (25) | 151.50 MiB / 154.50 MiB (25) | 12.46 MiB / 12.46 MiB (25) | 150.00 MiB / 157.34 MiB (25) |
| sys_gc_ready | 13.41 MiB / 13.66 MiB (25) | 150.48 MiB / 155.94 MiB (25) | 13.41 MiB / 13.60 MiB (25) | 149.86 MiB / 154.96 MiB (25) |
| tool_ms:get_context_remaining | 5.5 / 6.9 (750) | 7.5 / 9.4 (750) | 5.1 / 6.8 (750) | 5.9 / 9.1 (750) |
| tool_ms:notes.append | 6.9 / 8.4 (1500) | 9.8 / 12.3 (1500) | 6.6 / 8.1 (1500) | 8.7 / 11.8 (1500) |
| tool_ms:notes.write | 7.3 / 9.0 (1500) | 12.1 / 15.3 (1500) | 7.0 / 8.6 (1500) | 11.2 / 14.6 (1500) |
| tool_ms:read | 11.1 / 13.1 (750) | 12.4 / 14.3 (750) | 7.7 / 9.2 (750) | 11.3 / 13.8 (750) |
| total_alloc_end | 2010.61 MiB / 2014.22 MiB (25) | - | 1426.73 MiB / 1429.64 MiB (25) | - |
| total_alloc_ready | 1.05 MiB / 1.05 MiB (25) | - | 1.04 MiB / 1.05 MiB (25) | - |
| total_alloc_workload | 2009.56 MiB / 2013.18 MiB (25) | - | 1425.69 MiB / 1428.61 MiB (25) | - |
| tree_pss_ready | 68.63 MiB / 69.19 MiB (25) | 240.06 MiB / 240.88 MiB (25) | 68.81 MiB / 69.29 MiB (25) | 240.25 MiB / 240.70 MiB (25) |
| tree_rss_ready | 70.00 MiB / 70.55 MiB (25) | 245.32 MiB / 246.10 MiB (25) | 70.27 MiB / 70.66 MiB (25) | 245.54 MiB / 246.02 MiB (25) |

