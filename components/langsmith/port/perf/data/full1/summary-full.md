# LangSmith perf summary (every metric)

### Workload a, cold (go n=15, ts n=15, pi n=15, nopig n=15, nopi n=15)

| Metric | go | ts | pi | nopig | nopi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - | - | - |
| cpu_total_ms_end | 790 ±13% | 1560 ±12% | 1190 ±9% | 20 ±100%! | 600 ±5% | 0.51 | 0.66 |
| cpu_total_ms_ready | 790 ±13% | 1560 ±12% | 1190 ±9% | 20 ±100%! | 600 ±5% | 0.51 | 0.66 |
| cpu_total_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - |
| cpu_wait4_ms | 816.6 ±12% | 494.6 ±24%! | 1227.9 ±9% | 27.9 ±31%! | 619.0 ±7% | 1.65 | 0.67 |
| ext_anon_end | 1.93 MiB ±7% | 115.39 MiB ±4% | 126.80 MiB ±2% | - | - | 0.02 | 0.02 |
| ext_anon_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_anon_ready | 1.93 MiB ±7% | 115.39 MiB ±4% | 126.80 MiB ±2% | - | - | 0.02 | 0.02 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 0 ±0% | 0 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 0 ±0% | 1100 ±8% | 1190 ±9% | - | - | 0.00 | 0.00 |
| ext_cpu_ms_ready | 0 ±0% | 1100 ±8% | 1190 ±9% | - | - | 0.00 | 0.00 |
| ext_fds_end | 7 ±0% | 24 ±0% | 18 ±0% | - | - | 0.29 | 0.39 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | - | - | 0.29 | 0.39 |
| ext_peak_rss_end | 9.18 MiB ±2% | 170.27 MiB ±3% | 179.34 MiB ±1% | - | - | 0.05 | 0.05 |
| ext_peak_rss_ready | 9.18 MiB ±2% | 170.27 MiB ±3% | 179.34 MiB ±1% | - | - | 0.05 | 0.05 |
| ext_pss_end | 9.18 MiB ±2% | 127.00 MiB ±10% | 138.68 MiB ±9% | - | - | 0.07 | 0.07 |
| ext_pss_ready | 9.18 MiB ±2% | 129.19 MiB ±10% | 138.88 MiB ±9% | - | - | 0.07 | 0.07 |
| ext_rss_end | 9.18 MiB ±2% | 170.27 MiB ±3% | 179.34 MiB ±1% | - | - | 0.05 | 0.05 |
| ext_rss_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_rss_ready | 9.18 MiB ±2% | 170.27 MiB ±3% | 179.34 MiB ±1% | - | - | 0.05 | 0.05 |
| ext_threads_end | 4 ±25%! | 12 ±0% | 11 ±0% | - | - | 0.33 | 0.36 |
| ext_threads_ready | 4 ±25%! | 12 ±0% | 11 ±0% | - | - | 0.33 | 0.36 |
| gc_pause_ms_end | 0.1 ±193%! | 45.3 ±27%! | 50.9 ±33%! | - | - | 0.00 | 0.00 |
| gc_pause_ms_ready | 0.0 ±0% | 12.5 ±42%! | 18.9 ±23%! | - | - | 0.00 | 0.00 |
| gc_pause_ms_workload | 0.1 ±193%! | 32.6 ±31%! | 31.4 ±46%! | - | - | 0.00 | 0.00 |
| goroutines_asis_end | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_gc_end | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| heap_growth_asis | -0.38 MiB ±2% | -8.33 MiB ±19%! | -18.15 MiB ±24%! | - | - | 0.05 | 0.02 |
| heap_growth_gc | 0.00 MiB ±10% | -0.21 MiB ±0% | -0.50 MiB ±5% | - | - | -0.02 | -0.01 |
| heap_inuse_asis_end | 1.13 MiB ±17%! | 107.93 MiB ±1% | 111.15 MiB ±1% | - | - | 0.01 | 0.01 |
| heap_inuse_asis_ready | 1.57 MiB ±12% | 102.68 MiB ±2% | 109.65 MiB ±2% | - | - | 0.02 | 0.01 |
| heap_inuse_gc_end | 1.12 MiB ±17%! | 106.43 MiB ±1% | 103.65 MiB ±1% | - | - | 0.01 | 0.01 |
| heap_inuse_gc_ready | 1.12 MiB ±17%! | 107.93 MiB ±1% | 111.15 MiB ±1% | - | - | 0.01 | 0.01 |
| heap_used_asis_end | 0.38 MiB ±0% | 39.04 MiB ±0% | 36.14 MiB ±0% | - | - | 0.01 | 0.01 |
| heap_used_asis_ready | 0.76 MiB ±1% | 47.37 MiB ±3% | 54.29 MiB ±8% | - | - | 0.02 | 0.01 |
| heap_used_gc_end | 0.38 MiB ±1% | 38.77 MiB ±0% | 35.59 MiB ±0% | - | - | 0.01 | 0.01 |
| heap_used_gc_ready | 0.37 MiB ±1% | 38.98 MiB ±0% | 36.09 MiB ±0% | - | - | 0.01 | 0.01 |
| host_anon_end | 26.18 MiB ±24%! | 90.43 MiB ±19%! | 126.80 MiB ±2% | 6.73 MiB ±9% | 68.57 MiB ±7% | 0.29 | 0.21 |
| host_anon_ready | 26.18 MiB ±24%! | 90.43 MiB ±19%! | 126.80 MiB ±2% | 6.73 MiB ±9% | 68.57 MiB ±7% | 0.29 | 0.21 |
| host_cchild_ms_end | 610 ±13% | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | inf | inf |
| host_cchild_ms_ready | 610 ±13% | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | inf | inf |
| host_cpu_ms_end | 180 ±11% | 470 ±26%! | 1190 ±9% | 20 ±100%! | 600 ±5% | 0.38 | 0.15 |
| host_cpu_ms_ready | 180 ±11% | 470 ±26%! | 1190 ±9% | 20 ±100%! | 600 ±5% | 0.38 | 0.15 |
| host_fds_end | 13 ±0% | 15 ±0% | 18 ±0% | 9 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 9 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 67.37 MiB ±9% | 136.80 MiB ±12% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.49 | 0.38 |
| host_peak_rss_ready | 67.37 MiB ±9% | 136.80 MiB ±12% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.49 | 0.38 |
| host_pss_end | 65.87 MiB ±9% | 134.01 MiB ±14% | 138.68 MiB ±9% | 41.24 MiB ±42%! | 78.90 MiB ±11% | 0.49 | 0.47 |
| host_pss_ready | 65.89 MiB ±35%! | 135.24 MiB ±12% | 138.94 MiB ±9% | 41.24 MiB ±44%! | 79.11 MiB ±10% | 0.49 | 0.47 |
| host_rss_end | 67.37 MiB ±9% | 136.80 MiB ±12% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.49 | 0.38 |
| host_rss_ready | 67.37 MiB ±9% | 136.80 MiB ±12% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.49 | 0.38 |
| host_threads_end | 7 ±0% | 8 ±12% | 11 ±0% | 6 ±0% | 11 ±0% | 0.88 | 0.64 |
| host_threads_ready | 7 ±0% | 8 ±12% | 11 ±0% | 6 ±0% | 11 ±0% | 0.88 | 0.64 |
| mallocs_end | 10120 ±1% | - | - | - | - | - | - |
| mallocs_ready | 9964 ±1% | - | - | - | - | - | - |
| mallocs_workload | 156 ±2% | - | - | - | - | - | - |
| num_gc_end | 2 ±0% | 3 ±0% | 4 ±0% | - | - | 0.67 | 0.50 |
| num_gc_ready | 0 ±0% | 2 ±0% | 3 ±0% | - | - | 0.00 | 0.00 |
| num_gc_workload | 2 ±0% | 1 ±0% | 1 ±0% | - | - | 2.00 | 2.00 |
| peak_ext_anonymous | 1.93 MiB ±7% | 115.39 MiB ±4% | 126.80 MiB ±2% | 6.73 MiB ±9% | 68.57 MiB ±7% | 0.02 | 0.02 |
| peak_ext_pss | 9.18 MiB ±2% | 130.32 MiB ±9% | 141.69 MiB ±8% | 41.24 MiB ±42%! | 80.74 MiB ±10% | 0.07 | 0.06 |
| peak_ext_rss | 9.18 MiB ±2% | 170.27 MiB ±3% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.05 | 0.05 |
| peak_tree_anonymous | 28.11 MiB ±22%! | 203.62 MiB ±8% | 126.80 MiB ±2% | 6.73 MiB ±9% | 68.57 MiB ±7% | 0.14 | 0.22 |
| peak_tree_pss | 75.08 MiB ±8% | 267.25 MiB ±6% | 141.69 MiB ±8% | 41.24 MiB ±42%! | 80.74 MiB ±10% | 0.28 | 0.53 |
| peak_tree_rss | 76.55 MiB ±8% | 304.84 MiB ±5% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.25 | 0.43 |
| startup_ms | 677.5 ±14% | 1318.2 ±13% | 962.4 ±11% | 31.4 ±44%! | 504.7 ±5% | 0.51 | 0.70 |
| sys_asis_end | 12.40 MiB ±35%! | 167.61 MiB ±4% | 177.80 MiB ±3% | - | - | 0.07 | 0.07 |
| sys_asis_ready | 11.96 MiB ±33%! | 167.57 MiB ±2% | 177.78 MiB ±2% | - | - | 0.07 | 0.07 |
| sys_gc_end | 12.41 MiB ±35%! | 167.61 MiB ±3% | 177.80 MiB ±3% | - | - | 0.07 | 0.07 |
| sys_gc_ready | 12.40 MiB ±35%! | 167.61 MiB ±4% | 177.80 MiB ±3% | - | - | 0.07 | 0.07 |
| total_alloc_end | 0.78 MiB ±1% | - | - | - | - | - | - |
| total_alloc_ready | 0.76 MiB ±1% | - | - | - | - | - | - |
| total_alloc_workload | 0.01 MiB ±4% | - | - | - | - | - | - |
| tree_pss_end | 75.05 MiB ±8% | 260.02 MiB ±9% | 138.68 MiB ±9% | 41.24 MiB ±42%! | 78.90 MiB ±11% | 0.29 | 0.54 |
| tree_pss_ready | 75.07 MiB ±31%! | 264.36 MiB ±6% | 138.94 MiB ±9% | 41.24 MiB ±44%! | 79.11 MiB ±10% | 0.28 | 0.54 |
| tree_rss_end | 76.55 MiB ±8% | 304.84 MiB ±5% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.25 | 0.43 |
| tree_rss_ready | 76.55 MiB ±8% | 304.84 MiB ±5% | 179.34 MiB ±1% | 42.77 MiB ±2% | 120.45 MiB ±4% | 0.25 | 0.43 |
| wall_ms | 3197.6 ±3% | 3874.9 ±5% | 3504.3 ±3% | 2543.5 ±1% | 3033.0 ±1% | 0.83 | 0.91 |

### Workload a, warm (go n=15, ts n=15, pi n=15, nopig n=15, nopi n=15)

| Metric | go | ts | pi | nopig | nopi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - | - | - |
| cpu_total_ms_end | 50 ±20%! | 970 ±11% | 600 ±22%! | 20 ±50%! | 460 ±15%! | 0.05 | 0.08 |
| cpu_total_ms_ready | 50 ±20%! | 970 ±11% | 600 ±22%! | 20 ±50%! | 460 ±15%! | 0.05 | 0.08 |
| cpu_total_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - |
| cpu_wait4_ms | 64.7 ±23%! | 187.2 ±12% | 618.2 ±21%! | 28.8 ±34%! | 479.4 ±14% | 0.35 | 0.10 |
| ext_anon_end | 1.91 MiB ±7% | 117.43 MiB ±3% | 72.34 MiB ±9% | - | - | 0.02 | 0.03 |
| ext_anon_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_anon_ready | 1.91 MiB ±7% | 117.43 MiB ±3% | 72.34 MiB ±9% | - | - | 0.02 | 0.03 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 0 ±0% | 0 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 0 ±0% | 810 ±14% | 600 ±22%! | - | - | 0.00 | 0.00 |
| ext_cpu_ms_ready | 0 ±0% | 810 ±14% | 600 ±22%! | - | - | 0.00 | 0.00 |
| ext_fds_end | 7 ±0% | 24 ±0% | 18 ±0% | - | - | 0.29 | 0.39 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | - | - | 0.29 | 0.39 |
| ext_peak_rss_end | 9.18 MiB ±2% | 172.34 MiB ±2% | 124.60 MiB ±5% | - | - | 0.05 | 0.07 |
| ext_peak_rss_ready | 9.18 MiB ±2% | 172.34 MiB ±2% | 124.60 MiB ±5% | - | - | 0.05 | 0.07 |
| ext_pss_end | 9.18 MiB ±2% | 141.62 MiB ±13% | 93.94 MiB ±20%! | - | - | 0.06 | 0.10 |
| ext_pss_ready | 9.18 MiB ±2% | 135.69 MiB ±12% | 94.57 MiB ±22%! | - | - | 0.07 | 0.10 |
| ext_rss_end | 9.18 MiB ±2% | 172.34 MiB ±2% | 124.60 MiB ±5% | - | - | 0.05 | 0.07 |
| ext_rss_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_rss_ready | 9.18 MiB ±2% | 172.34 MiB ±2% | 124.60 MiB ±5% | - | - | 0.05 | 0.07 |
| ext_threads_end | 5 ±0% | 12 ±0% | 11 ±0% | - | - | 0.42 | 0.45 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | - | - | 0.42 | 0.45 |
| gc_pause_ms_end | 0.2 ±286%! | 45.3 ±27%! | 32.9 ±39%! | - | - | 0.00 | 0.01 |
| gc_pause_ms_ready | 0.0 ±0% | 13.8 ±61%! | 8.3 ±60%! | - | - | 0.00 | 0.00 |
| gc_pause_ms_workload | 0.2 ±286%! | 31.0 ±25%! | 24.2 ±31%! | - | - | 0.01 | 0.01 |
| goroutines_asis_end | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_gc_end | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| heap_growth_asis | -0.37 MiB ±3% | -7.02 MiB ±21%! | -8.84 MiB ±60%! | - | - | 0.05 | 0.04 |
| heap_growth_gc | 0.00 MiB ±4% | -0.21 MiB ±1% | -0.19 MiB ±7% | - | - | -0.02 | -0.03 |
| heap_inuse_asis_end | 1.11 MiB ±15%! | 109.43 MiB ±1% | 63.25 MiB ±3% | - | - | 0.01 | 0.02 |
| heap_inuse_asis_ready | 1.48 MiB ±12% | 104.18 MiB ±1% | 61.25 MiB ±3% | - | - | 0.01 | 0.02 |
| heap_inuse_gc_end | 1.10 MiB ±16%! | 106.43 MiB ±1% | 61.25 MiB ±1% | - | - | 0.01 | 0.02 |
| heap_inuse_gc_ready | 1.10 MiB ±16%! | 109.43 MiB ±1% | 63.25 MiB ±3% | - | - | 0.01 | 0.02 |
| heap_used_asis_end | 0.39 MiB ±1% | 39.04 MiB ±0% | 26.38 MiB ±0% | - | - | 0.01 | 0.01 |
| heap_used_asis_ready | 0.76 MiB ±1% | 46.06 MiB ±3% | 35.23 MiB ±15%! | - | - | 0.02 | 0.02 |
| heap_used_gc_end | 0.38 MiB ±1% | 38.77 MiB ±0% | 26.14 MiB ±0% | - | - | 0.01 | 0.01 |
| heap_used_gc_ready | 0.37 MiB ±1% | 38.98 MiB ±0% | 26.33 MiB ±0% | - | - | 0.01 | 0.01 |
| host_anon_end | 14.60 MiB ±4% | 42.82 MiB ±1% | 72.34 MiB ±9% | 6.49 MiB ±9% | 57.75 MiB ±5% | 0.34 | 0.20 |
| host_anon_ready | 14.60 MiB ±4% | 42.82 MiB ±1% | 72.34 MiB ±9% | 6.49 MiB ±9% | 57.75 MiB ±5% | 0.34 | 0.20 |
| host_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 50 ±20%! | 160 ±12% | 600 ±22%! | 20 ±50%! | 460 ±15%! | 0.31 | 0.08 |
| host_cpu_ms_ready | 50 ±20%! | 160 ±12% | 600 ±22%! | 20 ±50%! | 460 ±15%! | 0.31 | 0.08 |
| host_fds_end | 13 ±0% | 15 ±0% | 18 ±0% | 9 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 9 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 54.62 MiB ±2% | 83.56 MiB ±1% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.65 | 0.44 |
| host_peak_rss_ready | 54.62 MiB ±2% | 83.56 MiB ±1% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.65 | 0.44 |
| host_pss_end | 53.04 MiB ±38%! | 81.79 MiB ±25%! | 93.94 MiB ±20%! | 40.70 MiB ±44%! | 75.00 MiB ±55%! | 0.65 | 0.56 |
| host_pss_ready | 53.03 MiB ±37%! | 81.90 MiB ±24%! | 94.57 MiB ±22%! | 41.20 MiB ±44%! | 75.32 MiB ±22%! | 0.65 | 0.56 |
| host_rss_end | 54.62 MiB ±2% | 83.56 MiB ±1% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.65 | 0.44 |
| host_rss_ready | 54.62 MiB ±2% | 83.56 MiB ±1% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.65 | 0.44 |
| host_threads_end | 7 ±0% | 7 ±0% | 11 ±0% | 6 ±0% | 11 ±0% | 1.00 | 0.64 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 6 ±0% | 11 ±0% | 1.00 | 0.64 |
| mallocs_end | 10125 ±1% | - | - | - | - | - | - |
| mallocs_ready | 9966 ±1% | - | - | - | - | - | - |
| mallocs_workload | 157 ±4% | - | - | - | - | - | - |
| num_gc_end | 2 ±0% | 3 ±0% | 3 ±0% | - | - | 0.67 | 0.67 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±0% | - | - | 0.00 | 0.00 |
| num_gc_workload | 2 ±0% | 1 ±0% | 1 ±0% | - | - | 2.00 | 2.00 |
| peak_ext_anonymous | 1.91 MiB ±7% | 117.43 MiB ±3% | 72.34 MiB ±9% | 6.49 MiB ±9% | 57.75 MiB ±5% | 0.02 | 0.03 |
| peak_ext_pss | 9.18 MiB ±2% | 143.56 MiB ±22%! | 102.33 MiB ±27%! | 41.32 MiB ±39%! | 83.22 MiB ±41%! | 0.06 | 0.09 |
| peak_ext_rss | 9.18 MiB ±2% | 172.34 MiB ±2% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.05 | 0.07 |
| peak_tree_anonymous | 16.52 MiB ±4% | 160.41 MiB ±2% | 72.34 MiB ±9% | 6.49 MiB ±9% | 57.75 MiB ±5% | 0.10 | 0.23 |
| peak_tree_pss | 62.20 MiB ±32%! | 224.74 MiB ±20%! | 102.33 MiB ±27%! | 41.32 MiB ±39%! | 83.22 MiB ±41%! | 0.28 | 0.61 |
| peak_tree_rss | 63.88 MiB ±2% | 256.17 MiB ±2% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.25 | 0.51 |
| startup_ms | 86.2 ±21%! | 817.0 ±13% | 508.4 ±18%! | 30.9 ±15%! | 370.4 ±18%! | 0.11 | 0.17 |
| sys_asis_end | 8.66 MiB ±49%! | 169.27 MiB ±2% | 125.76 MiB ±6% | - | - | 0.05 | 0.07 |
| sys_asis_ready | 7.96 MiB ±50%! | 169.50 MiB ±3% | 125.05 MiB ±6% | - | - | 0.05 | 0.06 |
| sys_gc_end | 8.66 MiB ±49%! | 169.27 MiB ±2% | 125.76 MiB ±6% | - | - | 0.05 | 0.07 |
| sys_gc_ready | 8.66 MiB ±49%! | 169.27 MiB ±2% | 125.76 MiB ±6% | - | - | 0.05 | 0.07 |
| total_alloc_end | 0.77 MiB ±1% | - | - | - | - | - | - |
| total_alloc_ready | 0.76 MiB ±1% | - | - | - | - | - | - |
| total_alloc_workload | 0.01 MiB ±36%! | - | - | - | - | - | - |
| tree_pss_end | 62.20 MiB ±32%! | 214.33 MiB ±16%! | 93.94 MiB ±20%! | 40.70 MiB ±44%! | 75.00 MiB ±55%! | 0.29 | 0.66 |
| tree_pss_ready | 62.15 MiB ±32%! | 215.52 MiB ±16%! | 94.57 MiB ±22%! | 41.20 MiB ±44%! | 75.32 MiB ±22%! | 0.29 | 0.66 |
| tree_rss_end | 63.88 MiB ±2% | 256.17 MiB ±2% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.25 | 0.51 |
| tree_rss_ready | 63.88 MiB ±2% | 256.17 MiB ±2% | 124.60 MiB ±5% | 42.82 MiB ±3% | 109.66 MiB ±2% | 0.25 | 0.51 |
| wall_ms | 2606.2 ±1% | 3349.1 ±3% | 3040.3 ±3% | 2543.7 ±0% | 2893.8 ±2% | 0.78 | 0.86 |

### Workload b, cold (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - |
| cpu_total_ms_end | 800 ±10% | 1590 ±12% | 1190 ±8% | 0.50 | 0.67 |
| cpu_total_ms_ready | 800 ±10% | 1590 ±12% | 1190 ±8% | 0.50 | 0.67 |
| cpu_total_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - |
| cpu_wait4_ms | 831.5 ±9% | 498.3 ±11% | 1223.9 ±8% | 1.67 | 0.68 |
| ext_anon_end | 1.86 MiB ±10% | 115.71 MiB ±2% | 127.21 MiB ±3% | 0.02 | 0.01 |
| ext_anon_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_anon_ready | 1.86 MiB ±10% | 115.71 MiB ±2% | 127.21 MiB ±3% | 0.02 | 0.01 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 0 ±0% | 1110 ±13% | 1190 ±8% | 0.00 | 0.00 |
| ext_cpu_ms_ready | 0 ±0% | 1110 ±13% | 1190 ±8% | 0.00 | 0.00 |
| ext_fds_end | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 9.37 MiB ±2% | 170.59 MiB ±2% | 179.71 MiB ±2% | 0.05 | 0.05 |
| ext_peak_rss_ready | 9.37 MiB ±2% | 170.59 MiB ±2% | 179.71 MiB ±2% | 0.05 | 0.05 |
| ext_pss_end | 9.36 MiB ±2% | 134.83 MiB ±11% | 145.44 MiB ±9% | 0.07 | 0.06 |
| ext_pss_ready | 9.36 MiB ±2% | 134.92 MiB ±11% | 145.26 MiB ±8% | 0.07 | 0.06 |
| ext_rss_end | 9.37 MiB ±2% | 170.59 MiB ±2% | 179.71 MiB ±2% | 0.05 | 0.05 |
| ext_rss_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_rss_ready | 9.37 MiB ±2% | 170.59 MiB ±2% | 179.71 MiB ±2% | 0.05 | 0.05 |
| ext_threads_end | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| gc_pause_ms_end | 0.2 ±349%! | 44.7 ±15%! | 49.4 ±19%! | 0.00 | 0.00 |
| gc_pause_ms_ready | 0.0 ±0% | 12.7 ±13% | 18.5 ±25%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 0.2 ±349%! | 31.2 ±19%! | 31.5 ±25%! | 0.01 | 0.01 |
| goroutines_asis_end | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | -0.38 MiB ±2% | -8.39 MiB ±24%! | -17.42 MiB ±5% | 0.05 | 0.02 |
| heap_growth_gc | 0.00 MiB ±13% | -0.21 MiB ±1% | -0.49 MiB ±4% | -0.02 | -0.01 |
| heap_inuse_asis_end | 1.11 MiB ±18%! | 107.68 MiB ±2% | 110.90 MiB ±1% | 0.01 | 0.01 |
| heap_inuse_asis_ready | 1.58 MiB ±12% | 102.68 MiB ±2% | 109.40 MiB ±1% | 0.02 | 0.01 |
| heap_inuse_gc_end | 1.10 MiB ±18%! | 106.43 MiB ±1% | 103.40 MiB ±1% | 0.01 | 0.01 |
| heap_inuse_gc_ready | 1.10 MiB ±18%! | 107.68 MiB ±2% | 110.90 MiB ±1% | 0.01 | 0.01 |
| heap_used_asis_end | 0.39 MiB ±0% | 39.10 MiB ±0% | 36.19 MiB ±0% | 0.01 | 0.01 |
| heap_used_asis_ready | 0.77 MiB ±1% | 47.49 MiB ±4% | 53.61 MiB ±2% | 0.02 | 0.01 |
| heap_used_gc_end | 0.38 MiB ±0% | 38.83 MiB ±0% | 35.65 MiB ±0% | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±0% | 39.04 MiB ±0% | 36.14 MiB ±0% | 0.01 | 0.01 |
| host_anon_end | 26.47 MiB ±35%! | 88.04 MiB ±18%! | 127.21 MiB ±3% | 0.30 | 0.21 |
| host_anon_ready | 26.47 MiB ±35%! | 88.04 MiB ±18%! | 127.21 MiB ±3% | 0.30 | 0.21 |
| host_cchild_ms_end | 620 ±13% | 0 ±0% | 0 ±0% | inf | inf |
| host_cchild_ms_ready | 620 ±13% | 0 ±0% | 0 ±0% | inf | inf |
| host_cpu_ms_end | 180 ±11% | 470 ±13% | 1190 ±8% | 0.38 | 0.15 |
| host_cpu_ms_ready | 180 ±11% | 470 ±13% | 1190 ±8% | 0.38 | 0.15 |
| host_fds_end | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 67.60 MiB ±14% | 134.85 MiB ±12% | 179.71 MiB ±2% | 0.50 | 0.38 |
| host_peak_rss_ready | 67.60 MiB ±14% | 134.85 MiB ±12% | 179.71 MiB ±2% | 0.50 | 0.38 |
| host_pss_end | 56.06 MiB ±41%! | 132.38 MiB ±26%! | 145.44 MiB ±9% | 0.42 | 0.39 |
| host_pss_ready | 47.71 MiB ±46%! | 133.34 MiB ±13% | 145.26 MiB ±8% | 0.36 | 0.33 |
| host_rss_end | 67.60 MiB ±14% | 134.85 MiB ±12% | 179.71 MiB ±2% | 0.50 | 0.38 |
| host_rss_ready | 67.60 MiB ±14% | 134.85 MiB ±12% | 179.71 MiB ±2% | 0.50 | 0.38 |
| host_threads_end | 7 ±0% | 8 ±25%! | 11 ±0% | 0.88 | 0.64 |
| host_threads_ready | 7 ±0% | 8 ±25%! | 11 ±0% | 0.88 | 0.64 |
| ls_bytes | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_decoded_bytes | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_info | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_ingest_requests | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_multipart | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_requests | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| mallocs_end | 10301 ±1% | - | - | - | - |
| mallocs_ready | 10145 ±1% | - | - | - | - |
| mallocs_workload | 156 ±3% | - | - | - | - |
| num_gc_end | 2 ±0% | 3 ±0% | 4 ±0% | 0.67 | 0.50 |
| num_gc_ready | 0 ±0% | 2 ±0% | 3 ±0% | 0.00 | 0.00 |
| num_gc_workload | 2 ±0% | 1 ±0% | 1 ±0% | 2.00 | 2.00 |
| peak_ext_anonymous | 1.86 MiB ±10% | 115.71 MiB ±2% | 127.21 MiB ±3% | 0.02 | 0.01 |
| peak_ext_pss | 9.36 MiB ±2% | 142.85 MiB ±10% | 146.57 MiB ±9% | 0.07 | 0.06 |
| peak_ext_rss | 9.37 MiB ±2% | 170.59 MiB ±2% | 179.71 MiB ±2% | 0.05 | 0.05 |
| peak_tree_anonymous | 28.33 MiB ±33%! | 204.98 MiB ±8% | 127.21 MiB ±3% | 0.14 | 0.22 |
| peak_tree_pss | 75.04 MiB ±31%! | 274.01 MiB ±9% | 146.57 MiB ±9% | 0.27 | 0.51 |
| peak_tree_rss | 76.96 MiB ±13% | 306.90 MiB ±5% | 179.71 MiB ±2% | 0.25 | 0.43 |
| startup_ms | 691.4 ±14% | 1330.6 ±12% | 954.6 ±8% | 0.52 | 0.72 |
| sys_asis_end | 12.40 MiB ±35%! | 167.07 MiB ±2% | 177.76 MiB ±1% | 0.07 | 0.07 |
| sys_asis_ready | 11.71 MiB ±36%! | 167.07 MiB ±2% | 177.72 MiB ±1% | 0.07 | 0.07 |
| sys_gc_end | 12.40 MiB ±35%! | 167.27 MiB ±2% | 177.76 MiB ±1% | 0.07 | 0.07 |
| sys_gc_ready | 12.40 MiB ±35%! | 167.07 MiB ±2% | 177.76 MiB ±1% | 0.07 | 0.07 |
| total_alloc_end | 0.79 MiB ±1% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 0.01 MiB ±7% | - | - | - | - |
| tree_pss_end | 65.40 MiB ±35%! | 269.17 MiB ±16%! | 145.44 MiB ±9% | 0.24 | 0.45 |
| tree_pss_ready | 57.24 MiB ±39%! | 272.85 MiB ±8% | 145.26 MiB ±8% | 0.21 | 0.39 |
| tree_rss_end | 76.96 MiB ±13% | 306.90 MiB ±5% | 179.71 MiB ±2% | 0.25 | 0.43 |
| tree_rss_ready | 76.96 MiB ±13% | 306.90 MiB ±5% | 179.71 MiB ±2% | 0.25 | 0.43 |
| wall_ms | 3213.3 ±3% | 3889.2 ±4% | 3502.7 ±2% | 0.83 | 0.92 |

### Workload b, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - |
| cpu_total_ms_end | 50 ±20%! | 950 ±12% | 600 ±18%! | 0.05 | 0.08 |
| cpu_total_ms_ready | 50 ±20%! | 950 ±12% | 600 ±18%! | 0.05 | 0.08 |
| cpu_total_workload_ms | 0.0 ±0% | 0.0 ±0% | 0.0 ±0% | - | - |
| cpu_wait4_ms | 69.6 ±20%! | 186.1 ±24%! | 624.0 ±18%! | 0.37 | 0.11 |
| ext_anon_end | 1.95 MiB ±7% | 117.27 MiB ±3% | 75.57 MiB ±9% | 0.02 | 0.03 |
| ext_anon_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_anon_ready | 1.95 MiB ±7% | 117.27 MiB ±3% | 75.57 MiB ±9% | 0.02 | 0.03 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 0 ±0% | 790 ±9% | 600 ±18%! | 0.00 | 0.00 |
| ext_cpu_ms_ready | 0 ±0% | 790 ±9% | 600 ±18%! | 0.00 | 0.00 |
| ext_fds_end | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 9.47 MiB ±2% | 172.12 MiB ±2% | 127.89 MiB ±5% | 0.06 | 0.07 |
| ext_peak_rss_ready | 9.47 MiB ±2% | 172.12 MiB ±2% | 127.89 MiB ±5% | 0.06 | 0.07 |
| ext_pss_end | 9.46 MiB ±2% | 139.99 MiB ±10% | 93.22 MiB ±13% | 0.07 | 0.10 |
| ext_pss_ready | 9.46 MiB ±2% | 137.09 MiB ±11% | 91.81 MiB ±17%! | 0.07 | 0.10 |
| ext_rss_end | 9.47 MiB ±2% | 172.12 MiB ±2% | 127.89 MiB ±5% | 0.06 | 0.07 |
| ext_rss_growth | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_rss_ready | 9.47 MiB ±2% | 172.12 MiB ±2% | 127.89 MiB ±5% | 0.06 | 0.07 |
| ext_threads_end | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| gc_pause_ms_end | 0.1 ±202%! | 43.8 ±30%! | 32.7 ±35%! | 0.00 | 0.00 |
| gc_pause_ms_ready | 0.0 ±0% | 14.2 ±34%! | 8.0 ±100%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 0.1 ±202%! | 29.7 ±48%! | 22.3 ±28%! | 0.00 | 0.01 |
| goroutines_asis_end | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | -0.38 MiB ±2% | -7.07 MiB ±33%! | -8.82 MiB ±43%! | 0.05 | 0.04 |
| heap_growth_gc | 0.00 MiB ±7% | -0.21 MiB ±1% | -0.19 MiB ±8% | -0.02 | -0.02 |
| heap_inuse_asis_end | 1.15 MiB ±18%! | 109.43 MiB ±3% | 63.50 MiB ±4% | 0.01 | 0.02 |
| heap_inuse_asis_ready | 1.56 MiB ±14% | 104.43 MiB ±3% | 61.50 MiB ±3% | 0.01 | 0.03 |
| heap_inuse_gc_end | 1.14 MiB ±18%! | 106.43 MiB ±1% | 61.50 MiB ±1% | 0.01 | 0.02 |
| heap_inuse_gc_ready | 1.14 MiB ±18%! | 109.43 MiB ±3% | 63.50 MiB ±4% | 0.01 | 0.02 |
| heap_used_asis_end | 0.39 MiB ±0% | 39.10 MiB ±0% | 26.44 MiB ±0% | 0.01 | 0.01 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.17 MiB ±5% | 35.26 MiB ±11% | 0.02 | 0.02 |
| heap_used_gc_end | 0.38 MiB ±0% | 38.83 MiB ±0% | 26.20 MiB ±0% | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±0% | 39.04 MiB ±0% | 26.39 MiB ±0% | 0.01 | 0.01 |
| host_anon_end | 15.06 MiB ±3% | 43.22 MiB ±1% | 75.57 MiB ±9% | 0.35 | 0.20 |
| host_anon_ready | 15.06 MiB ±3% | 43.22 MiB ±1% | 75.57 MiB ±9% | 0.35 | 0.20 |
| host_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 50 ±20%! | 170 ±24%! | 600 ±18%! | 0.29 | 0.08 |
| host_cpu_ms_ready | 50 ±20%! | 170 ±24%! | 600 ±18%! | 0.29 | 0.08 |
| host_fds_end | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 55.20 MiB ±2% | 83.97 MiB ±1% | 127.89 MiB ±5% | 0.66 | 0.43 |
| host_peak_rss_ready | 55.20 MiB ±2% | 83.97 MiB ±1% | 127.89 MiB ±5% | 0.66 | 0.43 |
| host_pss_end | 53.71 MiB ±37%! | 82.15 MiB ±23%! | 93.22 MiB ±13% | 0.65 | 0.58 |
| host_pss_ready | 53.71 MiB ±37%! | 82.15 MiB ±24%! | 91.81 MiB ±17%! | 0.65 | 0.59 |
| host_rss_end | 55.20 MiB ±2% | 83.97 MiB ±1% | 127.89 MiB ±5% | 0.66 | 0.43 |
| host_rss_ready | 55.20 MiB ±2% | 83.97 MiB ±1% | 127.89 MiB ±5% | 0.66 | 0.43 |
| host_threads_end | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| ls_bytes | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_decoded_bytes | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_info | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_ingest_requests | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_multipart | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ls_requests | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| mallocs_end | 10256 ±1% | - | - | - | - |
| mallocs_ready | 10100 ±1% | - | - | - | - |
| mallocs_workload | 156 ±2% | - | - | - | - |
| num_gc_end | 2 ±0% | 3 ±0% | 3 ±0% | 0.67 | 0.67 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±0% | 0.00 | 0.00 |
| num_gc_workload | 2 ±0% | 1 ±0% | 1 ±0% | 2.00 | 2.00 |
| peak_ext_anonymous | 1.95 MiB ±7% | 117.27 MiB ±3% | 75.57 MiB ±9% | 0.02 | 0.03 |
| peak_ext_pss | 9.46 MiB ±2% | 140.59 MiB ±9% | 100.33 MiB ±15%! | 0.07 | 0.09 |
| peak_ext_rss | 9.47 MiB ±2% | 172.12 MiB ±2% | 127.89 MiB ±5% | 0.06 | 0.07 |
| peak_tree_anonymous | 16.93 MiB ±4% | 160.57 MiB ±2% | 75.57 MiB ±9% | 0.11 | 0.22 |
| peak_tree_pss | 63.19 MiB ±2% | 222.86 MiB ±8% | 100.33 MiB ±15%! | 0.28 | 0.63 |
| peak_tree_rss | 64.69 MiB ±1% | 256.43 MiB ±2% | 127.89 MiB ±5% | 0.25 | 0.51 |
| startup_ms | 81.9 ±24%! | 794.4 ±10% | 483.4 ±21%! | 0.10 | 0.17 |
| sys_asis_end | 12.40 MiB ±34%! | 169.26 MiB ±2% | 125.26 MiB ±4% | 0.07 | 0.10 |
| sys_asis_ready | 11.71 MiB ±34%! | 171.00 MiB ±3% | 125.26 MiB ±5% | 0.07 | 0.09 |
| sys_gc_end | 12.41 MiB ±34%! | 169.49 MiB ±2% | 125.32 MiB ±4% | 0.07 | 0.10 |
| sys_gc_ready | 12.40 MiB ±34%! | 169.26 MiB ±2% | 125.26 MiB ±4% | 0.07 | 0.10 |
| total_alloc_end | 0.78 MiB ±1% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 0.01 MiB ±5% | - | - | - | - |
| tree_pss_end | 63.19 MiB ±31%! | 217.46 MiB ±8% | 93.22 MiB ±13% | 0.29 | 0.68 |
| tree_pss_ready | 63.19 MiB ±31%! | 214.92 MiB ±13% | 91.81 MiB ±17%! | 0.29 | 0.69 |
| tree_rss_end | 64.69 MiB ±1% | 256.43 MiB ±2% | 127.89 MiB ±5% | 0.25 | 0.51 |
| tree_rss_ready | 64.69 MiB ±1% | 256.43 MiB ±2% | 127.89 MiB ±5% | 0.25 | 0.51 |
| wall_ms | 2600.9 ±1% | 3334.3 ±3% | 3017.9 ±3% | 0.78 | 0.86 |

### Workload c, warm (go n=15, ts n=15, pi n=15, nopig n=15, nopi n=15)

| Metric | go | ts | pi | nopig | nopi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 10.0 ±100%! | 100.0 ±10% | 110.0 ±9% | - | - | 0.10 | 0.09 |
| cpu_total_ms_end | 70 ±14% | 1050 ±4% | 700 ±4% | 20 ±0% | 520 ±8% | 0.07 | 0.10 |
| cpu_total_ms_ready | 50 ±20%! | 940 ±4% | 590 ±5% | 10 ±100%! | 460 ±9% | 0.05 | 0.08 |
| cpu_total_workload_ms | 20.0 ±50%! | 110.0 ±18%! | 110.0 ±9% | 10.0 ±100%! | 70.0 ±43%! | 0.18 | 0.18 |
| cpu_wait4_ms | 95.5 ±33%! | 199.1 ±3% | 730.5 ±3% | 33.2 ±19%! | 543.4 ±8% | 0.48 | 0.13 |
| ext_anon_end | 2.83 MiB ±3% | 120.69 MiB ±2% | 81.37 MiB ±5% | - | - | 0.02 | 0.03 |
| ext_anon_growth | 0.93 MiB ±15%! | 3.09 MiB ±7% | 6.36 MiB ±46%! | - | - | 0.30 | 0.15 |
| ext_anon_ready | 1.91 MiB ±7% | 117.61 MiB ±2% | 75.86 MiB ±7% | - | - | 0.02 | 0.03 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 0 ±0% | 0 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 10 ±100%! | 870 ±5% | 700 ±4% | - | - | 0.01 | 0.01 |
| ext_cpu_ms_ready | 0 ±0% | 780 ±4% | 590 ±5% | - | - | 0.00 | 0.00 |
| ext_fds_end | 8 ±0% | 25 ±0% | 20 ±0% | - | - | 0.32 | 0.40 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | - | - | 0.29 | 0.39 |
| ext_peak_rss_end | 10.82 MiB ±1% | 178.38 MiB ±1% | 135.22 MiB ±3% | - | - | 0.06 | 0.08 |
| ext_peak_rss_ready | 9.46 MiB ±2% | 172.54 MiB ±1% | 128.19 MiB ±4% | - | - | 0.05 | 0.07 |
| ext_pss_end | 10.82 MiB ±1% | 140.07 MiB ±10% | 97.37 MiB ±11% | - | - | 0.08 | 0.11 |
| ext_pss_ready | 9.45 MiB ±2% | 130.85 MiB ±11% | 91.21 MiB ±11% | - | - | 0.07 | 0.10 |
| ext_rss_end | 10.82 MiB ±1% | 178.38 MiB ±1% | 135.22 MiB ±3% | - | - | 0.06 | 0.08 |
| ext_rss_growth | 1.39 MiB ±16%! | 5.84 MiB ±4% | 7.99 MiB ±37%! | - | - | 0.24 | 0.17 |
| ext_rss_ready | 9.46 MiB ±2% | 172.54 MiB ±1% | 128.19 MiB ±4% | - | - | 0.05 | 0.07 |
| ext_threads_end | 5 ±20%! | 12 ±0% | 11 ±0% | - | - | 0.42 | 0.45 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | - | - | 0.42 | 0.45 |
| first_turn_ms | 19.7 ±12% | 76.2 ±11% | 86.4 ±5% | 7.0 ±29%! | 61.9 ±7% | 0.26 | 0.23 |
| flush_tail_ms | 258.0 ±1% | 309.7 ±1% | 258.6 ±1% | - | - | 0.83 | 1.00 |
| gc_pause_ms_end | 0.1 ±94%! | 45.1 ±52%! | 30.3 ±32%! | - | - | 0.00 | 0.00 |
| gc_pause_ms_ready | 0.0 ±0% | 16.0 ±37%! | 7.2 ±79%! | - | - | 0.00 | 0.00 |
| gc_pause_ms_workload | 0.1 ±94%! | 28.2 ±74%! | 23.4 ±20%! | - | - | 0.00 | 0.01 |
| goroutines_asis_end | 7 ±0% | 4 ±0% | 3 ±0% | - | - | 1.75 | 2.33 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| goroutines_gc_end | 7 ±0% | 4 ±0% | 3 ±0% | - | - | 1.75 | 2.33 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | - | - | 1.25 | 1.67 |
| heap_growth_asis | 0.30 MiB ±5% | -2.79 MiB ±32%! | -3.88 MiB ±94%! | - | - | -0.11 | -0.08 |
| heap_growth_gc | 0.04 MiB ±15% | 2.09 MiB ±0% | 2.76 MiB ±1% | - | - | 0.02 | 0.01 |
| heap_inuse_asis_end | 2.09 MiB ±5% | 110.68 MiB ±1% | 64.69 MiB ±3% | - | - | 0.02 | 0.03 |
| heap_inuse_asis_ready | 1.49 MiB ±13% | 104.43 MiB ±1% | 61.50 MiB ±4% | - | - | 0.01 | 0.02 |
| heap_inuse_gc_end | 1.19 MiB ±12% | 108.18 MiB ±1% | 64.19 MiB ±2% | - | - | 0.01 | 0.02 |
| heap_inuse_gc_ready | 1.10 MiB ±14% | 109.43 MiB ±1% | 63.25 MiB ±4% | - | - | 0.01 | 0.02 |
| heap_used_asis_end | 1.07 MiB ±1% | 43.37 MiB ±0% | 31.94 MiB ±0% | - | - | 0.02 | 0.03 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.16 MiB ±2% | 35.82 MiB ±10% | - | - | 0.02 | 0.02 |
| heap_used_gc_end | 0.42 MiB ±2% | 41.13 MiB ±0% | 29.16 MiB ±0% | - | - | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±2% | 39.04 MiB ±0% | 26.39 MiB ±0% | - | - | 0.01 | 0.01 |
| host_anon_end | 16.85 MiB ±3% | 45.00 MiB ±1% | 81.37 MiB ±5% | 7.50 MiB ±8% | 60.03 MiB ±6% | 0.37 | 0.21 |
| host_anon_ready | 15.05 MiB ±4% | 43.20 MiB ±1% | 75.86 MiB ±7% | 6.50 MiB ±9% | 57.82 MiB ±5% | 0.35 | 0.20 |
| host_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 60 ±0% | 180 ±6% | 700 ±4% | 20 ±0% | 520 ±8% | 0.33 | 0.09 |
| host_cpu_ms_ready | 50 ±20%! | 160 ±6% | 590 ±5% | 10 ±100%! | 460 ±9% | 0.31 | 0.08 |
| host_fds_end | 14 ±0% | 16 ±0% | 20 ±0% | 10 ±0% | 19 ±0% | 0.88 | 0.70 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 9 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 58.71 MiB ±2% | 87.15 MiB ±1% | 135.22 MiB ±3% | 45.36 MiB ±3% | 113.55 MiB ±3% | 0.67 | 0.43 |
| host_peak_rss_ready | 54.86 MiB ±2% | 83.95 MiB ±1% | 128.19 MiB ±4% | 42.61 MiB ±3% | 109.94 MiB ±3% | 0.65 | 0.43 |
| host_pss_end | 56.69 MiB ±35%! | 68.15 MiB ±30%! | 97.37 MiB ±11% | 26.42 MiB ±69%! | 73.98 MiB ±7% | 0.83 | 0.58 |
| host_pss_ready | 36.04 MiB ±54%! | 81.78 MiB ±24%! | 91.21 MiB ±11% | 24.38 MiB ±74%! | 70.74 MiB ±8% | 0.44 | 0.40 |
| host_rss_end | 58.71 MiB ±2% | 87.15 MiB ±1% | 135.22 MiB ±3% | 45.36 MiB ±3% | 113.55 MiB ±3% | 0.67 | 0.43 |
| host_rss_ready | 54.86 MiB ±2% | 83.95 MiB ±1% | 128.19 MiB ±4% | 42.61 MiB ±3% | 109.94 MiB ±3% | 0.65 | 0.43 |
| host_threads_end | 8 ±0% | 8 ±0% | 11 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.73 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 6 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 0.01 MiB ±0% | 0.01 MiB ±0% | 0.01 MiB ±0% | 0.01 MiB ±0% | 0.01 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 1 ±0% | 1 ±0% | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_bytes | 0.02 MiB ±0% | 0.02 MiB ±0% | 0.02 MiB ±0% | - | - | 1.00 | 1.00 |
| ls_decoded_bytes | 0.02 MiB ±0% | 0.02 MiB ±0% | 0.02 MiB ±0% | - | - | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | - | - | 1.00 | 1.00 |
| ls_ingest_requests | 1 ±0% | 1 ±0% | 1 ±0% | - | - | 1.00 | 1.00 |
| ls_multipart | 1 ±0% | 1 ±0% | 1 ±0% | - | - | 1.00 | 1.00 |
| ls_requests | 2 ±0% | 2 ±0% | 2 ±0% | - | - | 1.00 | 1.00 |
| mallocs_end | 17139 ±1% | - | - | - | - | - | - |
| mallocs_ready | 10099 ±1% | - | - | - | - | - | - |
| mallocs_workload | 7036 ±1% | - | - | - | - | - | - |
| num_gc_end | 2 ±0% | 3 ±0% | 3 ±0% | - | - | 0.67 | 0.67 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±0% | - | - | 0.00 | 0.00 |
| num_gc_workload | 2 ±0% | 1 ±0% | 1 ±0% | - | - | 2.00 | 2.00 |
| peak_ext_anonymous | 2.83 MiB ±3% | 120.69 MiB ±2% | 81.37 MiB ±5% | 7.50 MiB ±8% | 60.03 MiB ±6% | 0.02 | 0.03 |
| peak_ext_pss | 10.82 MiB ±1% | 140.07 MiB ±10% | 98.89 MiB ±11% | 26.42 MiB ±69%! | 74.11 MiB ±7% | 0.08 | 0.11 |
| peak_ext_rss | 10.82 MiB ±1% | 178.38 MiB ±1% | 135.22 MiB ±3% | 45.36 MiB ±3% | 113.55 MiB ±3% | 0.06 | 0.08 |
| peak_tree_anonymous | 19.68 MiB ±3% | 165.61 MiB ±1% | 81.37 MiB ±5% | 7.50 MiB ±8% | 60.03 MiB ±6% | 0.12 | 0.24 |
| peak_tree_pss | 67.57 MiB ±29%! | 225.22 MiB ±5% | 98.89 MiB ±11% | 26.42 MiB ±69%! | 74.11 MiB ±7% | 0.30 | 0.68 |
| peak_tree_rss | 69.52 MiB ±2% | 265.66 MiB ±1% | 135.22 MiB ±3% | 45.36 MiB ±3% | 113.55 MiB ±3% | 0.26 | 0.51 |
| startup_ms | 74.0 ±21%! | 790.7 ±3% | 475.0 ±7% | 31.7 ±9% | 363.5 ±13% | 0.09 | 0.16 |
| sys_asis_end | 12.59 MiB ±32%! | 175.50 MiB ±3% | 128.63 MiB ±4% | - | - | 0.07 | 0.10 |
| sys_asis_ready | 7.96 MiB ±53%! | 169.50 MiB ±2% | 125.21 MiB ±4% | - | - | 0.05 | 0.06 |
| sys_gc_end | 12.59 MiB ±34%! | 175.50 MiB ±2% | 130.07 MiB ±4% | - | - | 0.07 | 0.10 |
| sys_gc_ready | 12.34 MiB ±35%! | 169.50 MiB ±2% | 125.63 MiB ±4% | - | - | 0.07 | 0.10 |
| to_first_request_ms | 15.4 ±16%! | 68.7 ±11% | 66.7 ±6% | 6.0 ±36%! | 46.7 ±8% | 0.22 | 0.23 |
| total_alloc_end | 1.46 MiB ±0% | - | - | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - | - | - |
| total_alloc_workload | 0.69 MiB ±1% | - | - | - | - | - | - |
| tree_pss_end | 67.49 MiB ±29%! | 219.04 MiB ±9% | 97.37 MiB ±11% | 26.42 MiB ±69%! | 73.98 MiB ±7% | 0.31 | 0.69 |
| tree_pss_ready | 45.51 MiB ±43%! | 211.85 MiB ±11% | 91.21 MiB ±11% | 24.38 MiB ±74%! | 70.74 MiB ±8% | 0.21 | 0.50 |
| tree_rss_end | 69.52 MiB ±2% | 265.66 MiB ±1% | 135.22 MiB ±3% | 45.36 MiB ±3% | 113.55 MiB ±3% | 0.26 | 0.51 |
| tree_rss_ready | 64.41 MiB ±2% | 256.47 MiB ±1% | 128.19 MiB ±4% | 42.61 MiB ±3% | 109.94 MiB ±3% | 0.25 | 0.50 |
| turn_ms | 19.7 ±12% | 76.2 ±11% | 86.4 ±5% | 7.0 ±29%! | 61.9 ±7% | 0.26 | 0.23 |
| wall_ms | 1492.1 ±1% | 2326.2 ±2% | 1971.6 ±2% | 550.4 ±1% | 952.9 ±6% | 0.64 | 0.76 |
| work_ms | 19.7 ±12% | 76.2 ±11% | 86.4 ±5% | 7.0 ±29%! | 61.9 ±7% | 0.26 | 0.23 |

### Workload d10, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 60.0 ±17%! | 220.0 ±23%! | 310.0 ±10% | 0.27 | 0.19 |
| cpu_total_ms_end | 160 ±12% | 1240 ±9% | 900 ±7% | 0.13 | 0.18 |
| cpu_total_ms_ready | 50 ±20%! | 950 ±13% | 590 ±7% | 0.05 | 0.08 |
| cpu_total_workload_ms | 110.0 ±27%! | 270.0 ±22%! | 310.0 ±10% | 0.41 | 0.35 |
| cpu_wait4_ms | 189.1 ±44%! | 245.1 ±5% | 927.2 ±6% | 0.77 | 0.20 |
| ext_anon_end | 7.20 MiB ±6% | 131.93 MiB ±2% | 105.74 MiB ±12% | 0.05 | 0.07 |
| ext_anon_growth | 5.36 MiB ±7% | 14.15 MiB ±20%! | 31.72 MiB ±44%! | 0.38 | 0.17 |
| ext_anon_ready | 1.86 MiB ±6% | 117.55 MiB ±4% | 75.88 MiB ±8% | 0.02 | 0.02 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 60 ±17%! | 1020 ±11% | 900 ±7% | 0.06 | 0.07 |
| ext_cpu_ms_ready | 0 ±0% | 780 ±14% | 590 ±7% | 0.00 | 0.00 |
| ext_fds_end | 8 ±0% | 25 ±0% | 20 ±0% | 0.32 | 0.40 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 15.20 MiB ±3% | 189.63 MiB ±2% | 160.29 MiB ±3% | 0.08 | 0.09 |
| ext_peak_rss_ready | 9.37 MiB ±2% | 172.48 MiB ±3% | 128.17 MiB ±5% | 0.05 | 0.07 |
| ext_pss_end | 15.20 MiB ±3% | 145.39 MiB ±11% | 119.55 MiB ±6% | 0.10 | 0.13 |
| ext_pss_ready | 9.36 MiB ±2% | 129.04 MiB ±14% | 85.63 MiB ±12% | 0.07 | 0.11 |
| ext_rss_end | 15.20 MiB ±3% | 189.63 MiB ±2% | 160.29 MiB ±8% | 0.08 | 0.09 |
| ext_rss_growth | 5.86 MiB ±7% | 16.84 MiB ±17%! | 34.16 MiB ±41%! | 0.35 | 0.17 |
| ext_rss_ready | 9.37 MiB ±2% | 172.48 MiB ±3% | 128.17 MiB ±5% | 0.05 | 0.07 |
| ext_threads_end | 6 ±17%! | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 97.9 ±15% | 183.3 ±11% | 187.9 ±13% | 0.53 | 0.52 |
| flush_tail_ms | 260.7 ±20%! | 310.8 ±1% | 206.3 ±24%! | 0.84 | 1.26 |
| gc_pause_ms_end | 0.3 ±197%! | 46.9 ±28%! | 32.1 ±40%! | 0.01 | 0.01 |
| gc_pause_ms_ready | 0.0 ±0% | 13.8 ±40%! | 5.9 ±77%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 0.3 ±197%! | 31.0 ±45%! | 25.3 ±18%! | 0.01 | 0.01 |
| goroutines_asis_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | 2.08 MiB ±54%! | 7.39 MiB ±15%! | -2.83 MiB ±141%! | 0.28 | -0.73 |
| heap_growth_gc | 0.05 MiB ±14% | 2.60 MiB ±0% | 4.76 MiB ±0% | 0.02 | 0.01 |
| heap_inuse_asis_end | 4.30 MiB ±21%! | 111.18 MiB ±2% | 65.44 MiB ±3% | 0.04 | 0.07 |
| heap_inuse_asis_ready | 1.49 MiB ±13% | 104.43 MiB ±2% | 60.25 MiB ±3% | 0.01 | 0.02 |
| heap_inuse_gc_end | 1.25 MiB ±12% | 108.93 MiB ±1% | 65.94 MiB ±2% | 0.01 | 0.02 |
| heap_inuse_gc_ready | 1.12 MiB ±17%! | 109.68 MiB ±2% | 61.75 MiB ±4% | 0.01 | 0.02 |
| heap_used_asis_end | 2.84 MiB ±39%! | 53.43 MiB ±0% | 35.03 MiB ±0% | 0.05 | 0.08 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.06 MiB ±2% | 37.85 MiB ±11% | 0.02 | 0.02 |
| heap_used_gc_end | 0.42 MiB ±2% | 41.65 MiB ±0% | 31.14 MiB ±0% | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±1% | 39.04 MiB ±0% | 26.38 MiB ±0% | 0.01 | 0.01 |
| host_anon_end | 18.92 MiB ±5% | 51.25 MiB ±1% | 105.74 MiB ±12% | 0.37 | 0.18 |
| host_anon_ready | 15.05 MiB ±4% | 42.94 MiB ±1% | 75.88 MiB ±8% | 0.35 | 0.20 |
| host_cchild_ms_end | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 100 ±10% | 220 ±5% | 900 ±7% | 0.45 | 0.11 |
| host_cpu_ms_ready | 50 ±20%! | 170 ±6% | 590 ±7% | 0.29 | 0.08 |
| host_fds_end | 14 ±0% | 16 ±0% | 20 ±0% | 0.88 | 0.70 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 61.63 MiB ±2% | 93.97 MiB ±1% | 160.29 MiB ±3% | 0.66 | 0.38 |
| host_peak_rss_ready | 55.00 MiB ±2% | 83.95 MiB ±1% | 128.17 MiB ±5% | 0.66 | 0.43 |
| host_pss_end | 59.19 MiB ±34%! | 72.55 MiB ±29%! | 119.55 MiB ±6% | 0.82 | 0.50 |
| host_pss_ready | 52.80 MiB ±36%! | 63.19 MiB ±32%! | 85.63 MiB ±12% | 0.84 | 0.62 |
| host_rss_end | 61.63 MiB ±2% | 93.97 MiB ±1% | 160.29 MiB ±8% | 0.66 | 0.38 |
| host_rss_ready | 55.00 MiB ±2% | 83.95 MiB ±1% | 128.17 MiB ±5% | 0.66 | 0.43 |
| host_threads_end | 8 ±0% | 8 ±0% | 11 ±0% | 1.00 | 0.73 |
| host_threads_ready | 7 ±0% | 7 ±14% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 0.12 MiB ±0% | 0.12 MiB ±0% | 0.12 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 11 ±0% | 11 ±0% | 11 ±0% | 1.00 | 1.00 |
| ls_bytes | 0.25 MiB ±0% | 0.25 MiB ±0% | 0.25 MiB ±0% | 1.00 | 1.00 |
| ls_decoded_bytes | 0.25 MiB ±0% | 0.25 MiB ±0% | 0.25 MiB ±0% | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_multipart | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_requests | 2 ±0% | 2 ±0% | 2 ±0% | 1.00 | 1.00 |
| mallocs_end | 98521 ±0% | - | - | - | - |
| mallocs_ready | 10102 ±1% | - | - | - | - |
| mallocs_workload | 88415 ±0% | - | - | - | - |
| num_gc_end | 5 ±20%! | 3 ±0% | 4 ±0% | 1.67 | 1.25 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±0% | 0.00 | 0.00 |
| num_gc_workload | 5 ±20%! | 1 ±0% | 2 ±0% | 5.00 | 2.50 |
| peak_ext_anonymous | 7.20 MiB ±6% | 131.93 MiB ±2% | 105.78 MiB ±13% | 0.05 | 0.07 |
| peak_ext_pss | 15.20 MiB ±3% | 146.27 MiB ±13% | 119.72 MiB ±10% | 0.10 | 0.13 |
| peak_ext_rss | 15.20 MiB ±3% | 189.63 MiB ±2% | 160.38 MiB ±8% | 0.08 | 0.09 |
| peak_tree_anonymous | 25.98 MiB ±4% | 183.23 MiB ±2% | 105.78 MiB ±13% | 0.14 | 0.25 |
| peak_tree_pss | 74.92 MiB ±27%! | 235.67 MiB ±15% | 119.72 MiB ±10% | 0.32 | 0.63 |
| peak_tree_rss | 76.79 MiB ±2% | 283.67 MiB ±1% | 160.38 MiB ±8% | 0.27 | 0.48 |
| startup_ms | 86.0 ±24%! | 796.9 ±10% | 476.6 ±9% | 0.11 | 0.18 |
| sys_asis_end | 16.91 MiB ±24%! | 180.00 MiB ±2% | 147.11 MiB ±10% | 0.09 | 0.11 |
| sys_asis_ready | 11.96 MiB ±33%! | 171.00 MiB ±2% | 124.62 MiB ±5% | 0.07 | 0.10 |
| sys_gc_end | 16.91 MiB ±24%! | 181.19 MiB ±3% | 147.11 MiB ±10% | 0.09 | 0.11 |
| sys_gc_ready | 12.41 MiB ±33%! | 169.45 MiB ±2% | 124.62 MiB ±6% | 0.07 | 0.10 |
| to_first_request_ms | 15.9 ±36%! | 68.6 ±26%! | 67.9 ±7% | 0.23 | 0.23 |
| to_first_tool_ms | 17.8 ±34%! | 73.3 ±24%! | 86.8 ±5% | 0.24 | 0.20 |
| tool_ms:read | 0.5 ±55%! | 1.0 ±86%! | 0.9 ±647%! | 0.53 | 0.55 |
| total_alloc_end | 9.40 MiB ±0% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 8.63 MiB ±0% | - | - | - | - |
| tree_pss_end | 74.58 MiB ±27%! | 230.63 MiB ±10% | 119.55 MiB ±6% | 0.32 | 0.62 |
| tree_pss_ready | 62.19 MiB ±31%! | 196.43 MiB ±20%! | 85.63 MiB ±12% | 0.32 | 0.73 |
| tree_rss_end | 76.79 MiB ±2% | 283.67 MiB ±1% | 160.29 MiB ±8% | 0.27 | 0.48 |
| tree_rss_ready | 64.41 MiB ±2% | 256.02 MiB ±2% | 128.17 MiB ±5% | 0.25 | 0.50 |
| turn_ms | 97.9 ±15% | 183.3 ±11% | 187.9 ±13% | 0.53 | 0.52 |
| wall_ms | 1589.8 ±4% | 2457.3 ±4% | 2027.0 ±4% | 0.65 | 0.78 |
| work_ms | 97.9 ±15% | 183.3 ±11% | 187.9 ±13% | 0.53 | 0.52 |

### Workload d100, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 1780.0 ±9% | 1830.0 ±5% | 1630.0 ±7% | 0.97 | 1.09 |
| cpu_total_ms_end | 2480 ±9% | 3440 ±4% | 2300 ±7% | 0.72 | 1.08 |
| cpu_total_ms_ready | 50 ±20%! | 950 ±3% | 600 ±7% | 0.05 | 0.08 |
| cpu_total_workload_ms | 2430.0 ±9% | 2490.0 ±5% | 1700.0 ±7% | 0.98 | 1.43 |
| cpu_wait4_ms | 732.0 ±260%! | 853.7 ±5% | 2336.1 ±7% | 0.86 | 0.31 |
| ext_anon_end | 14.41 MiB ±12% | 261.21 MiB ±2% | 166.64 MiB ±9% | 0.06 | 0.09 |
| ext_anon_growth | 12.47 MiB ±15% | 143.90 MiB ±4% | 90.54 MiB ±21%! | 0.09 | 0.14 |
| ext_anon_ready | 1.86 MiB ±7% | 117.19 MiB ±2% | 76.18 MiB ±9% | 0.02 | 0.02 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 70 ±14% | - | 0.00 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 1780 ±9% | 2620 ±4% | 2230 ±7% | 0.68 | 0.80 |
| ext_cpu_ms_ready | 0 ±0% | 790 ±5% | 600 ±7% | 0.00 | 0.00 |
| ext_fds_end | 8 ±0% | 26 ±0% | 22 ±5% | 0.31 | 0.36 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 26.66 MiB ±11% | 319.51 MiB ±1% | 221.27 MiB ±7% | 0.08 | 0.12 |
| ext_peak_rss_ready | 9.38 MiB ±2% | 172.01 MiB ±1% | 128.46 MiB ±5% | 0.05 | 0.07 |
| ext_pss_end | 22.41 MiB ±8% | 284.13 MiB ±14% | 183.90 MiB ±19%! | 0.08 | 0.12 |
| ext_pss_ready | 9.37 MiB ±2% | 134.26 MiB ±29%! | 94.68 MiB ±47%! | 0.07 | 0.10 |
| ext_rss_end | 22.41 MiB ±8% | 319.51 MiB ±1% | 221.27 MiB ±7% | 0.07 | 0.10 |
| ext_rss_growth | 13.01 MiB ±14% | 147.40 MiB ±4% | 93.04 MiB ±21%! | 0.09 | 0.14 |
| ext_rss_ready | 9.38 MiB ±2% | 172.01 MiB ±1% | 128.46 MiB ±5% | 0.05 | 0.07 |
| ext_threads_end | 6 ±0% | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 96.7 ±15% | 183.5 ±9% | 189.0 ±5% | 0.53 | 0.51 |
| flush_tail_ms | 310.1 ±2% | 52.7 ±7% | 52.7 ±98%! | 5.89 | 5.88 |
| gc_pause_ms_end | 8.2 ±66%! | 120.2 ±20%! | 82.2 ±20%! | 0.07 | 0.10 |
| gc_pause_ms_ready | 0.0 ±0% | 13.8 ±39%! | 8.0 ±69%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 8.2 ±66%! | 107.5 ±20%! | 73.5 ±17%! | 0.08 | 0.11 |
| goroutines_asis_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | 5.40 MiB ±52%! | 52.09 MiB ±44%! | 25.66 MiB ±44%! | 0.10 | 0.21 |
| heap_growth_gc | 0.06 MiB ±9% | 4.22 MiB ±3% | 7.04 MiB ±2% | 0.02 | 0.01 |
| heap_inuse_asis_end | 8.50 MiB ±26%! | 202.57 MiB ±3% | 116.69 MiB ±3% | 0.04 | 0.07 |
| heap_inuse_asis_ready | 1.49 MiB ±12% | 104.18 MiB ±2% | 62.00 MiB ±4% | 0.01 | 0.02 |
| heap_inuse_gc_end | 1.68 MiB ±11% | 187.18 MiB ±3% | 115.94 MiB ±4% | 0.01 | 0.01 |
| heap_inuse_gc_ready | 1.12 MiB ±17%! | 109.18 MiB ±2% | 63.50 MiB ±4% | 0.01 | 0.02 |
| heap_used_asis_end | 6.17 MiB ±46%! | 98.29 MiB ±23%! | 60.15 MiB ±15% | 0.06 | 0.10 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.06 MiB ±3% | 34.40 MiB ±10% | 0.02 | 0.02 |
| heap_used_gc_end | 0.44 MiB ±1% | 43.26 MiB ±0% | 33.43 MiB ±0% | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±0% | 39.04 MiB ±0% | 26.39 MiB ±0% | 0.01 | 0.01 |
| host_anon_end | 37.40 MiB ±8% | 68.52 MiB ±7% | 166.64 MiB ±9% | 0.55 | 0.22 |
| host_anon_ready | 15.07 MiB ±4% | 43.23 MiB ±1% | 76.18 MiB ±9% | 0.35 | 0.20 |
| host_cchild_ms_end | 30 ±33%! | 30 ±33%! | 70 ±14% | 1.00 | 0.43 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 680 ±10% | 790 ±6% | 2230 ±7% | 0.86 | 0.30 |
| host_cpu_ms_ready | 50 ±20%! | 170 ±6% | 600 ±7% | 0.29 | 0.08 |
| host_fds_end | 14 ±0% | 16 ±0% | 22 ±5% | 0.88 | 0.64 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 80.55 MiB ±4% | 112.75 MiB ±5% | 221.27 MiB ±7% | 0.71 | 0.36 |
| host_peak_rss_ready | 55.04 MiB ±3% | 84.09 MiB ±1% | 128.46 MiB ±5% | 0.65 | 0.43 |
| host_pss_end | 60.21 MiB ±35%! | 108.55 MiB ±21%! | 183.90 MiB ±19%! | 0.55 | 0.33 |
| host_pss_ready | 37.28 MiB ±52%! | 65.43 MiB ±31%! | 94.68 MiB ±47%! | 0.57 | 0.39 |
| host_rss_end | 80.33 MiB ±4% | 111.47 MiB ±4% | 221.27 MiB ±7% | 0.72 | 0.36 |
| host_rss_ready | 55.04 MiB ±3% | 84.09 MiB ±1% | 128.46 MiB ±5% | 0.65 | 0.43 |
| host_threads_end | 10 ±0% | 10 ±0% | 11 ±0% | 1.00 | 0.91 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 5.34 MiB ±0% | 5.34 MiB ±0% | 5.35 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 110 ±0% | 110 ±0% | 110 ±0% | 1.00 | 1.00 |
| ls_bytes | 6.58 MiB ±0% | 6.71 MiB ±0% | 6.68 MiB ±1% | 0.98 | 0.99 |
| ls_decoded_bytes | 6.58 MiB ±0% | 6.71 MiB ±0% | 6.68 MiB ±1% | 0.98 | 0.99 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 15 ±0% | 33 ±15% | 23 ±22% | 0.45 | 0.65 |
| ls_multipart | 15 ±0% | 33 ±15% | 23 ±22% | 0.45 | 0.65 |
| ls_requests | 16 ±0% | 34 ±15% | 24 ±21% | 0.47 | 0.67 |
| mallocs_end | 4129143 ±0% | - | - | - | - |
| mallocs_ready | 10102 ±1% | - | - | - | - |
| mallocs_workload | 4119050 ±0% | - | - | - | - |
| num_gc_end | 133 ±6% | 10 ±0% | 15 ±13% | 13.30 | 8.87 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±0% | 0.00 | 0.00 |
| num_gc_workload | 133 ±6% | 8 ±0% | 13 ±15%! | 16.62 | 10.23 |
| peak_ext_anonymous | 18.73 MiB ±32%! | 262.06 MiB ±2% | 166.64 MiB ±9% | 0.07 | 0.11 |
| peak_ext_pss | 26.79 MiB ±22%! | 284.13 MiB ±15% | 183.90 MiB ±19%! | 0.09 | 0.15 |
| peak_ext_rss | 26.79 MiB ±22%! | 320.64 MiB ±1% | 221.27 MiB ±7% | 0.08 | 0.12 |
| peak_tree_anonymous | 56.48 MiB ±11% | 330.65 MiB ±2% | 166.64 MiB ±9% | 0.17 | 0.34 |
| peak_tree_pss | 99.82 MiB ±18%! | 396.97 MiB ±11% | 183.90 MiB ±19%! | 0.25 | 0.54 |
| peak_tree_rss | 107.26 MiB ±5% | 431.79 MiB ±1% | 221.27 MiB ±7% | 0.25 | 0.48 |
| startup_ms | 74.5 ±27%! | 799.1 ±5% | 482.2 ±8% | 0.09 | 0.15 |
| sys_asis_end | 29.16 MiB ±14% | 320.48 MiB ±4% | 221.25 MiB ±8% | 0.09 | 0.13 |
| sys_asis_ready | 11.96 MiB ±33%! | 169.50 MiB ±3% | 127.09 MiB ±4% | 0.07 | 0.09 |
| sys_gc_end | 29.16 MiB ±14% | 320.08 MiB ±5% | 221.25 MiB ±8% | 0.09 | 0.13 |
| sys_gc_ready | 12.41 MiB ±34%! | 169.50 MiB ±3% | 127.10 MiB ±4% | 0.07 | 0.10 |
| to_first_request_ms | 15.4 ±20%! | 69.7 ±8% | 66.2 ±5% | 0.22 | 0.23 |
| to_first_tool_ms | 17.3 ±18%! | 74.8 ±8% | 85.5 ±3% | 0.23 | 0.20 |
| tool_ms:bash | 2.8 ±51%! | 3.7 ±113%! | 5.9 ±94%! | 0.74 | 0.47 |
| tool_ms:read | 0.5 ±52%! | 0.8 ±76%! | 1.7 ±332%! | 0.61 | 0.28 |
| tool_ms:write | 0.5 ±53%! | 0.9 ±49%! | 1.8 ±399%! | 0.63 | 0.31 |
| total_alloc_end | 331.09 MiB ±0% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 330.32 MiB ±0% | - | - | - | - |
| tree_pss_end | 83.11 MiB ±25%! | 391.51 MiB ±10% | 183.90 MiB ±19%! | 0.21 | 0.45 |
| tree_pss_ready | 46.81 MiB ±41%! | 211.46 MiB ±18%! | 94.68 MiB ±47%! | 0.22 | 0.49 |
| tree_rss_end | 102.86 MiB ±4% | 431.79 MiB ±2% | 221.27 MiB ±7% | 0.24 | 0.46 |
| tree_rss_ready | 64.51 MiB ±2% | 256.14 MiB ±1% | 128.46 MiB ±5% | 0.25 | 0.50 |
| turn_ms | 2021.2 ±11% | 1889.6 ±4% | 1238.6 ±8% | 1.07 | 1.63 |
| wall_ms | 3570.6 ±6% | 3920.0 ±3% | 2957.9 ±5% | 0.91 | 1.21 |
| work_ms | 2024.5 ±11% | 1893.0 ±4% | 1240.7 ±8% | 1.07 | 1.63 |

### Workload d1000, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 89490.0 ±25%! | 49930.0 ±23%! | 24160.0 ±22%! | 1.79 | 3.70 |
| cpu_total_ms_end | 108010 ±26%! | 69720 ±22%! | 26020 ±22%! | 1.55 | 4.15 |
| cpu_total_ms_ready | 30 ±67%! | 720 ±28%! | 430 ±21%! | 0.04 | 0.07 |
| cpu_total_workload_ms | 107980.0 ±26%! | 68960.0 ±23%! | 25590.0 ±22%! | 1.57 | 4.22 |
| cpu_wait4_ms | 18757.5 ±37%! | 18914.8 ±24%! | 26080.8 ±22%! | 0.99 | 0.72 |
| ext_anon_end | 77.78 MiB ±93%! | 708.60 MiB ±12% | 570.40 MiB ±7% | 0.11 | 0.14 |
| ext_anon_growth | 75.83 MiB ±95%! | 589.62 MiB ±14% | 494.43 MiB ±9% | 0.13 | 0.15 |
| ext_anon_ready | 1.95 MiB ±3% | 117.95 MiB ±2% | 77.07 MiB ±7% | 0.02 | 0.03 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 1430 ±22%! | - | 0.00 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 89490 ±25%! | 50570 ±23%! | 24590 ±22%! | 1.77 | 3.64 |
| ext_cpu_ms_ready | 0 ±0% | 600 ±30%! | 430 ±21%! | 0.00 | 0.00 |
| ext_fds_end | 8 ±0% | 26 ±4% | 25 ±8% | 0.31 | 0.32 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 174.23 MiB ±7% | 790.61 MiB ±5% | 646.60 MiB ±9% | 0.22 | 0.27 |
| ext_peak_rss_ready | 9.52 MiB ±1% | 172.80 MiB ±1% | 129.29 MiB ±4% | 0.06 | 0.07 |
| ext_pss_end | 86.02 MiB ±84%! | 735.56 MiB ±16%! | 599.65 MiB ±12% | 0.12 | 0.14 |
| ext_pss_ready | 9.52 MiB ±1% | 134.45 MiB ±31%! | 92.52 MiB ±41%! | 0.07 | 0.10 |
| ext_rss_end | 86.02 MiB ±84%! | 767.12 MiB ±11% | 625.27 MiB ±7% | 0.11 | 0.14 |
| ext_rss_growth | 76.51 MiB ±94%! | 593.12 MiB ±14% | 496.93 MiB ±9% | 0.13 | 0.15 |
| ext_rss_ready | 9.52 MiB ±1% | 172.80 MiB ±1% | 129.29 MiB ±4% | 0.06 | 0.07 |
| ext_threads_end | 6 ±17%! | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_threads_ready | 5 ±20%! | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 72.7 ±29%! | 142.1 ±25%! | 142.6 ±36%! | 0.51 | 0.51 |
| flush_tail_ms | 309.3 ±2% | 102.9 ±101%! | 55.0 ±93%! | 3.01 | 5.63 |
| gc_pause_ms_end | 154.4 ±33%! | 3107.2 ±18%! | 1066.3 ±19%! | 0.05 | 0.14 |
| gc_pause_ms_ready | 0.0 ±0% | 10.9 ±44%! | 6.0 ±56%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 154.4 ±33%! | 3096.3 ±18%! | 1059.0 ±19%! | 0.05 | 0.15 |
| goroutines_asis_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | 10.63 MiB ±43%! | 55.65 MiB ±44%! | 117.51 MiB ±84%! | 0.19 | 0.09 |
| heap_growth_gc | 0.09 MiB ±15%! | 5.20 MiB ±4% | 10.16 MiB ±1% | 0.02 | 0.01 |
| heap_inuse_asis_end | 17.59 MiB ±56%! | 236.27 MiB ±5% | 271.73 MiB ±17%! | 0.07 | 0.06 |
| heap_inuse_asis_ready | 1.59 MiB ±12% | 104.68 MiB ±3% | 62.00 MiB ±3% | 0.02 | 0.03 |
| heap_inuse_gc_end | 1.81 MiB ±9% | 177.68 MiB ±25%! | 211.19 MiB ±23%! | 0.01 | 0.01 |
| heap_inuse_gc_ready | 1.16 MiB ±16%! | 109.68 MiB ±3% | 63.50 MiB ±4% | 0.01 | 0.02 |
| heap_used_asis_end | 11.39 MiB ±40%! | 101.58 MiB ±22%! | 152.49 MiB ±64%! | 0.11 | 0.07 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.12 MiB ±5% | 34.44 MiB ±10% | 0.02 | 0.02 |
| heap_used_gc_end | 0.47 MiB ±3% | 44.25 MiB ±0% | 36.56 MiB ±0% | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±0% | 39.04 MiB ±0% | 26.39 MiB ±0% | 0.01 | 0.01 |
| host_anon_end | 148.41 MiB ±33%! | 177.22 MiB ±39%! | 570.40 MiB ±7% | 0.84 | 0.26 |
| host_anon_ready | 15.08 MiB ±3% | 43.22 MiB ±1% | 77.07 MiB ±7% | 0.35 | 0.20 |
| host_cchild_ms_end | 350 ±20%! | 360 ±19%! | 1430 ±22%! | 0.97 | 0.24 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 18280 ±29%! | 18500 ±24%! | 24590 ±22%! | 0.99 | 0.74 |
| host_cpu_ms_ready | 30 ±67%! | 120 ±25%! | 430 ±21%! | 0.25 | 0.07 |
| host_fds_end | 14 ±0% | 16 ±0% | 25 ±8% | 0.88 | 0.56 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 204.56 MiB ±4% | 226.73 MiB ±7% | 646.60 MiB ±9% | 0.90 | 0.32 |
| host_peak_rss_ready | 55.22 MiB ±2% | 84.21 MiB ±2% | 129.29 MiB ±4% | 0.66 | 0.43 |
| host_pss_end | 189.91 MiB ±26%! | 202.25 MiB ±32%! | 599.84 MiB ±12% | 0.94 | 0.32 |
| host_pss_ready | 53.42 MiB ±36%! | 82.64 MiB ±2% | 92.52 MiB ±41%! | 0.65 | 0.58 |
| host_rss_end | 191.45 MiB ±26%! | 220.04 MiB ±31%! | 625.27 MiB ±7% | 0.87 | 0.31 |
| host_rss_ready | 55.22 MiB ±2% | 84.21 MiB ±2% | 129.29 MiB ±4% | 0.66 | 0.43 |
| host_threads_end | 10 ±0% | 10 ±0% | 11 ±0% | 1.00 | 0.91 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 467.96 MiB ±0% | 467.96 MiB ±0% | 467.97 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 1100 ±0% | 1100 ±0% | 1100 ±0% | 1.00 | 1.00 |
| ls_bytes | 480.53 MiB ±0% | 483.15 MiB ±0% | 483.60 MiB ±0% | 0.99 | 0.99 |
| ls_decoded_bytes | 480.53 MiB ±0% | 483.15 MiB ±0% | 483.60 MiB ±0% | 0.99 | 0.99 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 153 ±0% | 1089 ±11% | 723 ±15% | 0.14 | 0.21 |
| ls_multipart | 153 ±0% | 1089 ±11% | 723 ±15% | 0.14 | 0.21 |
| ls_requests | 154 ±0% | 1090 ±11% | 724 ±15% | 0.14 | 0.21 |
| mallocs_end | 364976011 ±0% | - | - | - | - |
| mallocs_ready | 10100 ±1% | - | - | - | - |
| mallocs_workload | 364965912 ±0% | - | - | - | - |
| num_gc_end | 1894 ±3% | 240 ±1% | 181 ±2% | 7.89 | 10.46 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±0% | 0.00 | 0.00 |
| num_gc_workload | 1894 ±3% | 238 ±1% | 179 ±2% | 7.96 | 10.58 |
| peak_ext_anonymous | 165.17 MiB ±8% | 732.93 MiB ±5% | 592.61 MiB ±10% | 0.23 | 0.28 |
| peak_ext_pss | 173.48 MiB ±7% | 752.33 MiB ±9% | 619.77 MiB ±10% | 0.23 | 0.28 |
| peak_ext_rss | 173.48 MiB ±7% | 791.35 MiB ±4% | 647.39 MiB ±9% | 0.22 | 0.27 |
| peak_tree_anonymous | 318.88 MiB ±8% | 909.23 MiB ±6% | 592.61 MiB ±10% | 0.35 | 0.54 |
| peak_tree_pss | 368.56 MiB ±7% | 974.91 MiB ±6% | 619.77 MiB ±10% | 0.38 | 0.59 |
| peak_tree_rss | 370.11 MiB ±7% | 1011.25 MiB ±6% | 647.39 MiB ±9% | 0.37 | 0.57 |
| startup_ms | 58.9 ±49%! | 595.7 ±27%! | 343.6 ±22%! | 0.10 | 0.17 |
| sys_asis_end | 188.82 MiB ±13% | 757.00 MiB ±6% | 636.29 MiB ±9% | 0.25 | 0.30 |
| sys_asis_ready | 11.71 MiB ±34%! | 171.00 MiB ±3% | 126.76 MiB ±6% | 0.07 | 0.09 |
| sys_gc_end | 188.82 MiB ±13% | 718.76 MiB ±9% | 606.40 MiB ±8% | 0.26 | 0.31 |
| sys_gc_ready | 12.35 MiB ±33%! | 171.00 MiB ±2% | 126.76 MiB ±6% | 0.07 | 0.10 |
| to_first_request_ms | 12.2 ±35%! | 53.4 ±28%! | 49.6 ±24%! | 0.23 | 0.25 |
| to_first_tool_ms | 13.6 ±32%! | 56.8 ±27%! | 62.1 ±26%! | 0.24 | 0.22 |
| tool_ms:bash | 2.6 ±48%! | 3.0 ±97%! | 12.8 ±103%! | 0.85 | 0.20 |
| tool_ms:read | 0.4 ±63%! | 0.6 ±85%! | 1.6 ±314%! | 0.73 | 0.26 |
| tool_ms:write | 0.5 ±58%! | 0.7 ±81%! | 2.1 ±243%! | 0.74 | 0.23 |
| total_alloc_end | 27830.10 MiB ±0% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 27829.33 MiB ±0% | - | - | - | - |
| tree_pss_end | 269.58 MiB ±39%! | 947.18 MiB ±9% | 599.84 MiB ±12% | 0.28 | 0.45 |
| tree_pss_ready | 62.95 MiB ±31%! | 215.38 MiB ±20%! | 92.52 MiB ±41%! | 0.29 | 0.68 |
| tree_rss_end | 271.10 MiB ±39%! | 978.94 MiB ±9% | 625.27 MiB ±7% | 0.28 | 0.43 |
| tree_rss_ready | 64.72 MiB ±2% | 257.08 MiB ±1% | 129.29 MiB ±4% | 0.25 | 0.50 |
| turn_ms | 90113.5 ±26%! | 58832.5 ±23%! | 23072.6 ±23%! | 1.53 | 3.91 |
| wall_ms | 91696.5 ±26%! | 60830.2 ±23%! | 24683.9 ±22%! | 1.51 | 3.71 |
| work_ms | 90156.8 ±26%! | 58870.1 ±23%! | 23094.2 ±23%! | 1.53 | 3.90 |

### Workload e, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 3300.0 ±9% | 2400.0 ±7% | 1910.0 ±15%! | 1.38 | 1.73 |
| cpu_total_ms_end | 4310 ±10% | 4070 ±6% | 2390 ±13% | 1.06 | 1.80 |
| cpu_total_ms_ready | 30 ±33%! | 670 ±12% | 420 ±14% | 0.04 | 0.07 |
| cpu_total_workload_ms | 4280.0 ±11% | 3370.0 ±7% | 1970.0 ±15%! | 1.27 | 2.17 |
| cpu_wait4_ms | 1035.8 ±13% | 1124.9 ±6% | 2437.9 ±13% | 0.92 | 0.42 |
| ext_anon_end | 102.93 MiB ±35%! | 569.83 MiB ±11% | 349.62 MiB ±8% | 0.18 | 0.29 |
| ext_anon_growth | 100.98 MiB ±36%! | 451.65 MiB ±14% | 269.27 MiB ±10% | 0.22 | 0.38 |
| ext_anon_ready | 1.94 MiB ±7% | 117.81 MiB ±2% | 76.24 MiB ±9% | 0.02 | 0.03 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 60 ±17%! | - | 0.00 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 3300 ±9% | 2970 ±6% | 2330 ±13% | 1.11 | 1.42 |
| ext_cpu_ms_ready | 0 ±0% | 550 ±13% | 420 ±14% | 0.00 | 0.00 |
| ext_fds_end | 8 ±0% | 30 ±0% | 25 ±0% | 0.27 | 0.32 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 111.70 MiB ±11% | 646.89 MiB ±8% | 421.95 MiB ±8% | 0.17 | 0.26 |
| ext_peak_rss_ready | 9.46 MiB ±2% | 172.85 MiB ±1% | 128.46 MiB ±5% | 0.05 | 0.07 |
| ext_pss_end | 110.92 MiB ±33%! | 600.16 MiB ±11% | 387.76 MiB ±14% | 0.18 | 0.29 |
| ext_pss_ready | 9.46 MiB ±2% | 136.99 MiB ±25%! | 96.55 MiB ±39%! | 0.07 | 0.10 |
| ext_rss_end | 110.92 MiB ±33%! | 630.07 MiB ±10% | 407.50 MiB ±7% | 0.18 | 0.27 |
| ext_rss_growth | 101.48 MiB ±36%! | 457.09 MiB ±14% | 274.77 MiB ±9% | 0.22 | 0.37 |
| ext_rss_ready | 9.46 MiB ±2% | 172.85 MiB ±1% | 128.46 MiB ±5% | 0.05 | 0.07 |
| ext_threads_end | 6 ±0% | 13 ±0% | 12 ±0% | 0.46 | 0.50 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 3777.6 ±10% | 2588.5 ±9% | 1446.4 ±17%! | 1.46 | 2.61 |
| flush_tail_ms | 269.5 ±5% | 278.5 ±37%! | 158.1 ±98%! | 0.97 | 1.70 |
| gc_pause_ms_end | 8.6 ±65%! | 217.7 ±21%! | 93.6 ±16%! | 0.04 | 0.09 |
| gc_pause_ms_ready | 0.0 ±0% | 10.5 ±43%! | 6.0 ±63%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 8.6 ±65%! | 206.8 ±19%! | 88.2 ±16%! | 0.04 | 0.10 |
| goroutines_asis_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | 61.75 MiB ±117%! | 29.27 MiB ±5% | 56.48 MiB ±58%! | 2.11 | 1.09 |
| heap_growth_gc | 0.06 MiB ±12% | 4.88 MiB ±0% | 7.57 MiB ±1% | 0.01 | 0.01 |
| heap_inuse_asis_end | 65.38 MiB ±107%! | 215.93 MiB ±4% | 206.93 MiB ±13% | 0.30 | 0.32 |
| heap_inuse_asis_ready | 1.56 MiB ±14% | 104.68 MiB ±2% | 62.00 MiB ±7% | 0.01 | 0.03 |
| heap_inuse_gc_end | 1.64 MiB ±7% | 181.18 MiB ±2% | 175.19 MiB ±7% | 0.01 | 0.01 |
| heap_inuse_gc_ready | 1.12 MiB ±17%! | 109.68 MiB ±2% | 63.50 MiB ±7% | 0.01 | 0.02 |
| heap_used_asis_end | 62.52 MiB ±115%! | 75.26 MiB ±1% | 94.36 MiB ±37%! | 0.83 | 0.66 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.02 MiB ±3% | 37.88 MiB ±21%! | 0.02 | 0.02 |
| heap_used_gc_end | 0.44 MiB ±2% | 43.92 MiB ±0% | 33.95 MiB ±0% | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±0% | 39.04 MiB ±0% | 26.39 MiB ±0% | 0.01 | 0.01 |
| host_anon_end | 99.25 MiB ±16%! | 121.71 MiB ±20%! | 349.62 MiB ±8% | 0.82 | 0.28 |
| host_anon_ready | 15.07 MiB ±3% | 43.25 MiB ±0% | 76.24 MiB ±9% | 0.35 | 0.20 |
| host_cchild_ms_end | 20 ±50%! | 30 ±33%! | 60 ±17%! | 0.67 | 0.33 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 990 ±13% | 1070 ±7% | 2330 ±13% | 0.93 | 0.42 |
| host_cpu_ms_ready | 30 ±33%! | 120 ±8% | 420 ±14% | 0.25 | 0.07 |
| host_fds_end | 14 ±0% | 16 ±0% | 25 ±0% | 0.88 | 0.56 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 142.24 MiB ±11% | 164.55 MiB ±7% | 421.95 MiB ±8% | 0.86 | 0.34 |
| host_peak_rss_ready | 55.27 MiB ±2% | 84.12 MiB ±1% | 128.46 MiB ±5% | 0.66 | 0.43 |
| host_pss_end | 140.70 MiB ±11% | 163.05 MiB ±16%! | 387.76 MiB ±14% | 0.86 | 0.36 |
| host_pss_ready | 53.73 MiB ±3% | 82.55 MiB ±21%! | 96.55 MiB ±39%! | 0.65 | 0.56 |
| host_rss_end | 142.24 MiB ±11% | 164.55 MiB ±14% | 407.50 MiB ±7% | 0.86 | 0.35 |
| host_rss_ready | 55.27 MiB ±2% | 84.12 MiB ±1% | 128.46 MiB ±5% | 0.66 | 0.43 |
| host_threads_end | 10 ±0% | 10 ±0% | 12 ±0% | 1.00 | 0.83 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 36.14 MiB ±0% | 36.14 MiB ±0% | 36.14 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 100 ±0% | 100 ±0% | 100 ±0% | 1.00 | 1.00 |
| ls_bytes | 41.45 MiB ±0% | 41.45 MiB ±0% | 41.46 MiB ±0% | 1.00 | 1.00 |
| ls_decoded_bytes | 41.45 MiB ±0% | 41.45 MiB ±0% | 41.46 MiB ±0% | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 13 ±0% | 13 ±15% | 14 ±7% | 1.00 | 0.93 |
| ls_multipart | 13 ±0% | 13 ±15% | 14 ±7% | 1.00 | 0.93 |
| ls_requests | 14 ±0% | 14 ±14% | 15 ±7% | 1.00 | 0.93 |
| mallocs_end | 3532346 ±0% | - | - | - | - |
| mallocs_ready | 10108 ±1% | - | - | - | - |
| mallocs_workload | 3522189 ±0% | - | - | - | - |
| num_gc_end | 128 ±5% | 19 ±0% | 15 ±13% | 6.74 | 8.53 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±50%! | 0.00 | 0.00 |
| num_gc_workload | 128 ±5% | 17 ±0% | 13 ±15%! | 7.53 | 9.85 |
| peak_ext_anonymous | 103.78 MiB ±17%! | 580.71 MiB ±10% | 363.24 MiB ±11% | 0.18 | 0.29 |
| peak_ext_pss | 111.83 MiB ±15%! | 612.41 MiB ±11% | 393.92 MiB ±11% | 0.18 | 0.28 |
| peak_ext_rss | 111.84 MiB ±15%! | 641.15 MiB ±9% | 420.91 MiB ±10% | 0.17 | 0.27 |
| peak_tree_anonymous | 202.85 MiB ±14% | 698.09 MiB ±8% | 363.24 MiB ±11% | 0.29 | 0.56 |
| peak_tree_pss | 252.26 MiB ±15%! | 769.96 MiB ±9% | 393.92 MiB ±11% | 0.33 | 0.64 |
| peak_tree_rss | 253.82 MiB ±11% | 801.29 MiB ±7% | 420.91 MiB ±10% | 0.32 | 0.60 |
| startup_ms | 55.1 ±39%! | 567.4 ±13% | 344.7 ±14% | 0.10 | 0.16 |
| sys_asis_end | 121.48 MiB ±20%! | 613.68 MiB ±8% | 422.23 MiB ±11% | 0.20 | 0.29 |
| sys_asis_ready | 11.71 MiB ±34%! | 171.00 MiB ±2% | 124.09 MiB ±7% | 0.07 | 0.09 |
| sys_gc_end | 121.48 MiB ±20%! | 596.80 MiB ±13% | 410.20 MiB ±9% | 0.20 | 0.30 |
| sys_gc_ready | 12.34 MiB ±34%! | 171.00 MiB ±2% | 125.04 MiB ±7% | 0.07 | 0.10 |
| to_first_request_ms | 12.3 ±19%! | 51.7 ±17%! | 49.3 ±32%! | 0.24 | 0.25 |
| to_first_tool_ms | 14.1 ±18%! | 55.4 ±17%! | 61.9 ±35%! | 0.26 | 0.23 |
| tool_ms:bash | 2.4 ±37%! | 3.0 ±235%! | 6.6 ±129%! | 0.80 | 0.37 |
| tool_ms:read | 0.4 ±742%! | 0.7 ±373%! | 0.9 ±1044%! | 0.57 | 0.41 |
| tool_ms:write | 0.4 ±53%! | 0.7 ±135%! | 1.1 ±456%! | 0.63 | 0.38 |
| total_alloc_end | 1049.76 MiB ±0% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 1048.99 MiB ±0% | - | - | - | - |
| tree_pss_end | 246.01 MiB ±16%! | 755.77 MiB ±6% | 387.76 MiB ±14% | 0.33 | 0.63 |
| tree_pss_ready | 63.17 MiB ±2% | 219.40 MiB ±20%! | 96.55 MiB ±39%! | 0.29 | 0.65 |
| tree_rss_end | 247.52 MiB ±16%! | 790.15 MiB ±7% | 407.50 MiB ±7% | 0.31 | 0.61 |
| tree_rss_ready | 64.72 MiB ±2% | 256.98 MiB ±1% | 128.46 MiB ±5% | 0.25 | 0.50 |
| turn_ms | 3777.6 ±10% | 2588.5 ±9% | 1446.4 ±17%! | 1.46 | 2.61 |
| wall_ms | 5269.1 ±5% | 4624.1 ±5% | 3121.8 ±7% | 1.14 | 1.69 |
| work_ms | 3777.6 ±10% | 2588.5 ±9% | 1446.4 ±17%! | 1.46 | 2.61 |

### Workload f0, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_total_ms_ready | 30 ±0% | 660 ±11% | 410 ±15% | 0.05 | 0.07 |
| cpu_wait4_ms | 92.2 ±37%! | 157.3 ±5% | 607.7 ±10% | 0.59 | 0.15 |
| end_to_close_ms | 2.1 ±32%! | 2.3 ±38%! | 1.8 ±41%! | 0.90 | 1.15 |
| ext_anon_ready | 1.85 MiB ±7% | 118.38 MiB ±2% | 74.64 MiB ±9% | 0.02 | 0.02 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_cpu_ms_ready | 0 ±0% | 540 ±11% | 410 ±15% | 0.00 | 0.00 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_gone_ms | 255.4 ±2% | 268.2 ±14% | 224.7 ±7% | 0.95 | 1.14 |
| ext_peak_rss_ready | 9.35 MiB ±2% | 173.34 MiB ±1% | 126.93 MiB ±5% | 0.05 | 0.07 |
| ext_pss_ready | 9.35 MiB ±2% | 169.27 MiB ±2% | 124.77 MiB ±6% | 0.06 | 0.07 |
| ext_rss_ready | 9.35 MiB ±2% | 173.34 MiB ±1% | 126.93 MiB ±5% | 0.05 | 0.07 |
| ext_threads_ready | 5 ±20%! | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 34.6 ±22%! | 86.6 ±13% | 105.7 ±22%! | 0.40 | 0.33 |
| host_anon_ready | 14.65 MiB ±4% | 43.23 MiB ±1% | 74.64 MiB ±9% | 0.34 | 0.20 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_ready | 30 ±0% | 120 ±8% | 410 ±15% | 0.25 | 0.07 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_ready | 55.01 MiB ±2% | 84.12 MiB ±1% | 126.93 MiB ±5% | 0.65 | 0.43 |
| host_pss_ready | 53.47 MiB ±2% | 82.60 MiB ±1% | 124.72 MiB ±6% | 0.65 | 0.43 |
| host_rss_ready | 55.01 MiB ±2% | 84.12 MiB ±1% | 126.93 MiB ±5% | 0.65 | 0.43 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 0.02 MiB ±0% | 0.02 MiB ±0% | 0.02 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 4 ±0% | 4 ±0% | 4 ±0% | 1.00 | 1.00 |
| ls_bytes | 0.07 MiB ±0% | 0.06 MiB ±0% | 0.07 MiB ±0% | 1.00 | 1.00 |
| ls_decoded_bytes | 0.07 MiB ±0% | 0.06 MiB ±0% | 0.07 MiB ±0% | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_multipart | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_requests | 2 ±0% | 2 ±0% | 2 ±0% | 1.00 | 1.00 |
| ls_requests_before_close | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| peak_ext_anonymous | 4.04 MiB ±1% | 122.34 MiB ±4% | 100.20 MiB ±7% | 0.03 | 0.04 |
| peak_ext_pss | 12.04 MiB ±1% | 175.68 MiB ±3% | 151.91 MiB ±6% | 0.07 | 0.08 |
| peak_ext_rss | 12.04 MiB ±1% | 180.05 MiB ±3% | 154.35 MiB ±4% | 0.07 | 0.08 |
| peak_tree_anonymous | 22.68 MiB ±3% | 168.89 MiB ±3% | 100.20 MiB ±7% | 0.13 | 0.23 |
| peak_tree_pss | 71.74 MiB ±2% | 263.14 MiB ±2% | 151.91 MiB ±6% | 0.27 | 0.47 |
| peak_tree_rss | 73.29 MiB ±2% | 268.98 MiB ±2% | 154.35 MiB ±4% | 0.27 | 0.47 |
| shutdown_cpu_ext_ms | 0.0 ±0% | 30.0 ±33%! | - | 0.00 | - |
| shutdown_cpu_host_ms | 92.2 ±37%! | 157.3 ±5% | 607.7 ±10% | 0.59 | 0.15 |
| shutdown_ms | 259.8 ±1% | 275.8 ±14% | 224.7 ±7% | 0.94 | 1.16 |
| startup_ms | 44.6 ±46%! | 558.8 ±11% | 338.0 ±13% | 0.08 | 0.13 |
| to_first_request_ms | 12.0 ±54%! | 51.4 ±17%! | 50.6 ±18%! | 0.23 | 0.24 |
| to_first_tool_ms | 13.5 ±52%! | 54.9 ±17%! | 63.9 ±17%! | 0.25 | 0.21 |
| tool_ms:bash | 2.6 ±130%! | 4.2 ±146%! | 7.1 ±49%! | 0.63 | 0.37 |
| tool_ms:read | 0.6 ±28%! | 1.3 ±19%! | 3.9 ±13% | 0.44 | 0.14 |
| tool_ms:write | 0.5 ±14% | 1.0 ±411%! | 9.6 ±95%! | 0.49 | 0.05 |
| tree_pss_ready | 62.81 MiB ±2% | 251.36 MiB ±1% | 124.72 MiB ±6% | 0.25 | 0.50 |
| tree_rss_ready | 64.35 MiB ±2% | 256.98 MiB ±1% | 126.93 MiB ±5% | 0.25 | 0.51 |
| turn_ms | 34.6 ±22%! | 86.6 ±13% | 105.7 ±22%! | 0.40 | 0.33 |
| wall_ms | 847.9 ±3% | 1434.1 ±4% | 1184.2 ±5% | 0.59 | 0.72 |
| work_ms | 34.6 ±22%! | 86.6 ±13% | 105.7 ±22%! | 0.40 | 0.33 |

### Workload f200, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_total_ms_ready | 30 ±33%! | 650 ±11% | 410 ±17%! | 0.05 | 0.07 |
| cpu_wait4_ms | 70.3 ±73%! | 155.6 ±10% | 599.4 ±16%! | 0.45 | 0.12 |
| end_to_close_ms | 2.0 ±24%! | 2.1 ±40%! | 2.2 ±65%! | 0.96 | 0.91 |
| ext_anon_ready | 1.93 MiB ±7% | 118.61 MiB ±2% | 74.07 MiB ±7% | 0.02 | 0.03 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_cpu_ms_ready | 0 ±0% | 540 ±11% | 410 ±17%! | 0.00 | 0.00 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_gone_ms | 629.7 ±1% | 642.0 ±3% | 636.4 ±2% | 0.98 | 0.99 |
| ext_peak_rss_ready | 9.45 MiB ±2% | 173.58 MiB ±2% | 126.50 MiB ±4% | 0.05 | 0.07 |
| ext_pss_ready | 9.45 MiB ±2% | 168.31 MiB ±21%! | 123.02 MiB ±30%! | 0.06 | 0.08 |
| ext_rss_ready | 9.45 MiB ±2% | 173.58 MiB ±2% | 126.50 MiB ±4% | 0.05 | 0.07 |
| ext_threads_ready | 5 ±20%! | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 34.4 ±19%! | 77.0 ±25%! | 91.5 ±19%! | 0.45 | 0.38 |
| host_anon_ready | 15.08 MiB ±4% | 43.20 MiB ±1% | 74.07 MiB ±7% | 0.35 | 0.20 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_ready | 30 ±33%! | 110 ±9% | 410 ±17%! | 0.27 | 0.07 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_ready | 55.12 MiB ±1% | 83.98 MiB ±1% | 126.50 MiB ±4% | 0.66 | 0.44 |
| host_pss_ready | 53.49 MiB ±35%! | 82.43 MiB ±1% | 122.97 MiB ±30%! | 0.65 | 0.43 |
| host_rss_ready | 55.12 MiB ±1% | 83.98 MiB ±1% | 126.50 MiB ±4% | 0.66 | 0.44 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 0.02 MiB ±0% | 0.02 MiB ±0% | 0.02 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 4 ±0% | 4 ±0% | 4 ±0% | 1.00 | 1.00 |
| ls_bytes | 0.07 MiB ±0% | 0.06 MiB ±0% | 0.07 MiB ±0% | 1.00 | 1.00 |
| ls_decoded_bytes | 0.07 MiB ±0% | 0.06 MiB ±0% | 0.07 MiB ±0% | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_multipart | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_requests | 2 ±0% | 2 ±0% | 2 ±0% | 1.00 | 1.00 |
| ls_requests_before_close | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| peak_ext_anonymous | 5.24 MiB ±4% | 122.59 MiB ±5% | 97.07 MiB ±6% | 0.04 | 0.05 |
| peak_ext_pss | 13.23 MiB ±2% | 175.29 MiB ±22%! | 147.93 MiB ±25%! | 0.08 | 0.09 |
| peak_ext_rss | 13.24 MiB ±2% | 180.30 MiB ±3% | 151.62 MiB ±3% | 0.07 | 0.09 |
| peak_tree_anonymous | 24.00 MiB ±3% | 169.27 MiB ±4% | 97.07 MiB ±6% | 0.14 | 0.25 |
| peak_tree_pss | 73.08 MiB ±27%! | 263.20 MiB ±15% | 147.93 MiB ±25%! | 0.28 | 0.49 |
| peak_tree_rss | 74.91 MiB ±1% | 269.73 MiB ±2% | 151.62 MiB ±3% | 0.28 | 0.49 |
| shutdown_cpu_ext_ms | 10.0 ±100%! | 50.0 ±40%! | - | 0.20 | - |
| shutdown_cpu_host_ms | 70.3 ±73%! | 155.6 ±10% | 599.4 ±16%! | 0.45 | 0.12 |
| shutdown_ms | 635.6 ±1% | 653.0 ±1% | 636.4 ±2% | 0.97 | 1.00 |
| startup_ms | 56.9 ±60%! | 554.8 ±9% | 336.1 ±21%! | 0.10 | 0.17 |
| to_first_request_ms | 11.8 ±22%! | 45.7 ±32%! | 48.4 ±28%! | 0.26 | 0.24 |
| to_first_tool_ms | 13.3 ±19%! | 48.8 ±26%! | 60.5 ±23%! | 0.27 | 0.22 |
| tool_ms:bash | 2.6 ±122%! | 4.0 ±22%! | 8.8 ±60%! | 0.65 | 0.30 |
| tool_ms:read | 0.5 ±41%! | 1.2 ±27%! | 4.0 ±10% | 0.46 | 0.14 |
| tool_ms:write | 0.4 ±51%! | 0.8 ±447%! | 1.3 ±10% | 0.53 | 0.34 |
| tree_pss_ready | 62.97 MiB ±30%! | 250.63 MiB ±14% | 122.97 MiB ±30%! | 0.25 | 0.51 |
| tree_rss_ready | 64.56 MiB ±1% | 257.04 MiB ±1% | 126.50 MiB ±4% | 0.25 | 0.51 |
| turn_ms | 34.4 ±19%! | 77.0 ±25%! | 91.5 ±19%! | 0.45 | 0.38 |
| wall_ms | 1229.0 ±3% | 1808.0 ±3% | 1578.3 ±5% | 0.68 | 0.78 |
| work_ms | 34.4 ±19%! | 77.0 ±25%! | 91.5 ±19%! | 0.45 | 0.38 |

### Workload f500, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_total_ms_ready | 40 ±50%! | 870 ±31%! | 490 ±27%! | 0.05 | 0.08 |
| cpu_wait4_ms | 87.2 ±52%! | 208.2 ±27%! | 956.4 ±17%! | 0.42 | 0.09 |
| end_to_close_ms | 2.0 ±40%! | 2.1 ±43%! | 2.1 ±44%! | 0.94 | 0.93 |
| ext_anon_ready | 1.84 MiB ±7% | 118.00 MiB ±6% | 77.35 MiB ±11% | 0.02 | 0.02 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_cpu_ms_ready | 0 ±0% | 730 ±32%! | 490 ±27%! | 0.00 | 0.00 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_gone_ms | 22803.2 ±26%! | 21483.7 ±34%! | 22086.1 ±32%! | 1.06 | 1.03 |
| ext_peak_rss_ready | 9.35 MiB ±2% | 172.87 MiB ±4% | 129.53 MiB ±7% | 0.05 | 0.07 |
| ext_pss_ready | 9.34 MiB ±2% | 168.01 MiB ±4% | 126.54 MiB ±9% | 0.06 | 0.07 |
| ext_rss_ready | 9.35 MiB ±2% | 172.87 MiB ±4% | 129.53 MiB ±7% | 0.05 | 0.07 |
| ext_threads_ready | 5 ±0% | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 41.2 ±25%! | 111.2 ±42%! | 136.7 ±38%! | 0.37 | 0.30 |
| host_anon_ready | 15.07 MiB ±3% | 43.24 MiB ±1% | 77.35 MiB ±11% | 0.35 | 0.19 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_ready | 40 ±50%! | 160 ±31%! | 490 ±27%! | 0.25 | 0.08 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_ready | 55.12 MiB ±2% | 83.94 MiB ±1% | 129.53 MiB ±7% | 0.66 | 0.43 |
| host_pss_ready | 53.56 MiB ±2% | 82.40 MiB ±1% | 126.53 MiB ±9% | 0.65 | 0.42 |
| host_rss_ready | 55.12 MiB ±2% | 83.94 MiB ±1% | 129.53 MiB ±7% | 0.66 | 0.43 |
| host_threads_ready | 7 ±0% | 7 ±0% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 0.02 MiB ±0% | 0.02 MiB ±0% | 0.02 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 4 ±0% | 4 ±0% | 4 ±0% | 1.00 | 1.00 |
| ls_bytes | 0.33 MiB ±0% | 0.32 MiB ±0% | 0.33 MiB ±0% | 1.00 | 1.00 |
| ls_decoded_bytes | 0.33 MiB ±0% | 0.32 MiB ±0% | 0.33 MiB ±0% | 1.00 | 1.00 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 5 ±0% | 5 ±0% | 5 ±0% | 1.00 | 1.00 |
| ls_multipart | 5 ±0% | 5 ±0% | 5 ±0% | 1.00 | 1.00 |
| ls_requests | 6 ±0% | 6 ±0% | 6 ±0% | 1.00 | 1.00 |
| ls_requests_before_close | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| peak_ext_anonymous | 5.17 MiB ±2% | 135.95 MiB ±6% | 104.04 MiB ±13% | 0.04 | 0.05 |
| peak_ext_pss | 13.16 MiB ±1% | 189.82 MiB ±4% | 154.46 MiB ±12% | 0.07 | 0.09 |
| peak_ext_rss | 13.17 MiB ±1% | 194.28 MiB ±4% | 158.60 MiB ±9% | 0.07 | 0.08 |
| peak_tree_anonymous | 23.87 MiB ±2% | 182.50 MiB ±4% | 104.04 MiB ±13% | 0.13 | 0.23 |
| peak_tree_pss | 73.16 MiB ±1% | 277.97 MiB ±3% | 154.46 MiB ±12% | 0.26 | 0.47 |
| peak_tree_rss | 74.68 MiB ±1% | 283.89 MiB ±3% | 158.60 MiB ±9% | 0.26 | 0.47 |
| shutdown_cpu_ext_ms | 10.0 ±200%! | 270.0 ±37%! | - | 0.04 | - |
| shutdown_cpu_host_ms | 87.2 ±52%! | 208.2 ±27%! | 956.4 ±17%! | 0.42 | 0.09 |
| shutdown_ms | 22809.4 ±26%! | 21502.3 ±34%! | 22086.1 ±32%! | 1.06 | 1.03 |
| startup_ms | 65.9 ±42%! | 754.0 ±31%! | 399.8 ±33%! | 0.09 | 0.16 |
| to_first_request_ms | 13.1 ±22%! | 63.8 ±49%! | 65.3 ±41%! | 0.21 | 0.20 |
| to_first_tool_ms | 15.0 ±22%! | 68.5 ±47%! | 85.7 ±35%! | 0.22 | 0.18 |
| tool_ms:bash | 3.2 ±50%! | 5.1 ±158%! | 8.8 ±71%! | 0.63 | 0.37 |
| tool_ms:read | 0.6 ±45%! | 1.4 ±53%! | 6.1 ±81%! | 0.42 | 0.10 |
| tool_ms:write | 0.6 ±46%! | 1.0 ±132%! | 8.6 ±114%! | 0.66 | 0.07 |
| tree_pss_ready | 62.91 MiB ±2% | 250.40 MiB ±9% | 126.53 MiB ±9% | 0.25 | 0.50 |
| tree_rss_ready | 64.47 MiB ±2% | 256.45 MiB ±2% | 129.53 MiB ±7% | 0.25 | 0.50 |
| turn_ms | 41.2 ±25%! | 111.2 ±42%! | 136.7 ±38%! | 0.37 | 0.30 |
| wall_ms | 23431.5 ±25%! | 22863.9 ±32%! | 23142.0 ±30%! | 1.02 | 1.01 |
| work_ms | 41.2 ±25%! | 111.2 ±42%! | 136.7 ±38%! | 0.37 | 0.30 |

### Workload g, warm (go n=15, ts n=15, pi n=15)

| Metric | go | ts | pi | go/ts | go/pi |
|---|---:|---:|---:|---:|---:|
| cpu_ext_workload_ms | 570.0 ±21%! | 1190.0 ±32%! | 1090.0 ±31%! | 0.48 | 0.52 |
| cpu_idle_ms | 0.0 ±0% | 170.0 ±35%! | 170.0 ±35%! | 0.00 | 0.00 |
| cpu_total_ms_end | 930 ±85%! | 2480 ±54%! | 1730 ±60%! | 0.38 | 0.54 |
| cpu_total_ms_post | 930 ±85%! | 2290 ±57%! | 1550 ±63%! | 0.41 | 0.60 |
| cpu_total_ms_ready | 50 ±1340%! | 960 ±91%! | 610 ±111%! | 0.05 | 0.08 |
| cpu_total_workload_ms | 880.0 ±19%! | 1520.0 ±30%! | 1110.0 ±32%! | 0.58 | 0.79 |
| cpu_wait4_ms | 857.9 ±87%! | 527.6 ±87%! | 1756.1 ±59%! | 1.63 | 0.49 |
| ext_anon_end | 7.16 MiB ±18%! | 116.57 MiB ±3% | 80.21 MiB ±36%! | 0.06 | 0.09 |
| ext_anon_growth | 5.20 MiB ±25%! | -1.30 MiB ±377%! | 2.57 MiB ±1555%! | -3.99 | 2.02 |
| ext_anon_idle_growth | 0 ±0% | -68.93 MiB ±5% | -62.94 MiB ±30%! | -0.00 | -0.00 |
| ext_anon_post | 7.16 MiB ±18%! | 185.46 MiB ±3% | 131.74 MiB ±36%! | 0.04 | 0.05 |
| ext_anon_ready | 1.91 MiB ±7% | 117.13 MiB ±2% | 73.46 MiB ±77%! | 0.02 | 0.03 |
| ext_cchild_ms_end | 0 ±0% | 0 ±0% | 20 ±50%! | - | 0.00 |
| ext_cchild_ms_post | 0 ±0% | 0 ±0% | 20 ±50%! | - | 0.00 |
| ext_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| ext_count | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ext_cpu_ms_end | 570 ±21%! | 1990 ±44%! | 1700 ±61%! | 0.29 | 0.34 |
| ext_cpu_ms_post | 570 ±21%! | 1800 ±49%! | 1530 ±63%! | 0.32 | 0.37 |
| ext_cpu_ms_ready | 0 ±0% | 790 ±63%! | 610 ±111%! | 0.00 | 0.00 |
| ext_fds_end | 8 ±0% | 24 ±0% | 18 ±0% | 0.33 | 0.44 |
| ext_fds_post | 8 ±0% | 26 ±0% | 24 ±8% | 0.31 | 0.33 |
| ext_fds_ready | 7 ±0% | 24 ±0% | 18 ±0% | 0.29 | 0.39 |
| ext_peak_rss_end | 17.97 MiB ±17%! | 240.65 MiB ±3% | 184.95 MiB ±25%! | 0.07 | 0.10 |
| ext_peak_rss_post | 17.97 MiB ±17%! | 243.77 MiB ±2% | 186.53 MiB ±25%! | 0.07 | 0.10 |
| ext_peak_rss_ready | 9.48 MiB ±2% | 172.08 MiB ±1% | 125.64 MiB ±45%! | 0.06 | 0.08 |
| ext_pss_end | 15.17 MiB ±9% | 170.60 MiB ±2% | 132.42 MiB ±22%! | 0.09 | 0.11 |
| ext_pss_post | 15.17 MiB ±9% | 239.42 MiB ±2% | 184.08 MiB ±26%! | 0.06 | 0.08 |
| ext_pss_ready | 9.48 MiB ±2% | 167.99 MiB ±2% | 123.41 MiB ±46%! | 0.06 | 0.08 |
| ext_rss_end | 15.18 MiB ±9% | 175.02 MiB ±2% | 134.88 MiB ±21%! | 0.09 | 0.11 |
| ext_rss_growth | 5.67 MiB ±23%! | 2.14 MiB ±233%! | 5.01 MiB ±802%! | 2.65 | 1.13 |
| ext_rss_idle_growth | 0 ±0% | -68.93 MiB ±5% | -62.94 MiB ±30%! | -0.00 | -0.00 |
| ext_rss_post | 15.18 MiB ±9% | 243.77 MiB ±2% | 186.53 MiB ±25%! | 0.06 | 0.08 |
| ext_rss_ready | 9.48 MiB ±2% | 172.08 MiB ±1% | 125.64 MiB ±45%! | 0.06 | 0.08 |
| ext_threads_end | 6 ±0% | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_threads_post | 6 ±0% | 12 ±0% | 11 ±0% | 0.50 | 0.55 |
| ext_threads_ready | 5 ±20%! | 12 ±0% | 11 ±0% | 0.42 | 0.45 |
| first_turn_ms | 34.7 ±24%! | 98.8 ±38%! | 120.5 ±55%! | 0.35 | 0.29 |
| flush_tail_ms | 259.8 ±2% | 52.4 ±3% | 52.4 ±2% | 4.95 | 4.96 |
| gc_pause_ms_end | 3.4 ±138%! | 144.3 ±30%! | 123.7 ±38%! | 0.02 | 0.03 |
| gc_pause_ms_post | 2.8 ±91%! | 74.2 ±34%! | 61.2 ±61%! | 0.04 | 0.05 |
| gc_pause_ms_ready | 0.0 ±0% | 12.9 ±53%! | 8.0 ±248%! | 0.00 | 0.00 |
| gc_pause_ms_workload | 3.4 ±138%! | 130.4 ±36%! | 113.6 ±26%! | 0.03 | 0.03 |
| goroutines_asis_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_asis_post | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_asis_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| goroutines_gc_end | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_gc_post | 7 ±0% | 4 ±0% | 3 ±0% | 1.75 | 2.33 |
| goroutines_gc_ready | 5 ±0% | 4 ±0% | 3 ±0% | 1.25 | 1.67 |
| heap_growth_asis | -0.32 MiB ±2% | -4.21 MiB ±72%! | -3.20 MiB ±308%! | 0.08 | 0.10 |
| heap_growth_gc | 0.06 MiB ±9% | 3.57 MiB ±1% | 5.81 MiB ±6% | 0.02 | 0.01 |
| heap_idle_growth_asis | -2.54 MiB ±78%! | -11.13 MiB ±117%! | -21.32 MiB ±87%! | 0.23 | 0.12 |
| heap_inuse_asis_end | 1.59 MiB ±7% | 45.18 MiB ±2% | 34.81 MiB ±26%! | 0.04 | 0.05 |
| heap_inuse_asis_post | 4.29 MiB ±46%! | 115.23 MiB ±60%! | 104.94 MiB ±8% | 0.04 | 0.04 |
| heap_inuse_asis_ready | 1.55 MiB ±10% | 103.30 MiB ±4% | 61.88 MiB ±79%! | 0.02 | 0.03 |
| heap_inuse_gc_end | 1.59 MiB ±7% | 46.18 MiB ±2% | 35.69 MiB ±27%! | 0.03 | 0.04 |
| heap_inuse_gc_post | 1.59 MiB ±7% | 111.55 MiB ±113%! | 104.56 MiB ±8% | 0.01 | 0.02 |
| heap_inuse_gc_ready | 1.13 MiB ±15%! | 108.68 MiB ±3% | 63.50 MiB ±77%! | 0.01 | 0.02 |
| heap_used_asis_end | 0.45 MiB ±1% | 42.75 MiB ±1% | 32.37 MiB ±29%! | 0.01 | 0.01 |
| heap_used_asis_post | 2.98 MiB ±66%! | 54.02 MiB ±24%! | 53.92 MiB ±33%! | 0.06 | 0.06 |
| heap_used_asis_ready | 0.77 MiB ±1% | 46.97 MiB ±6% | 35.58 MiB ±53%! | 0.02 | 0.02 |
| heap_used_gc_end | 0.44 MiB ±1% | 42.61 MiB ±0% | 32.22 MiB ±30%! | 0.01 | 0.01 |
| heap_used_gc_post | 0.44 MiB ±1% | 42.96 MiB ±0% | 32.85 MiB ±29%! | 0.01 | 0.01 |
| heap_used_gc_ready | 0.38 MiB ±1% | 39.04 MiB ±0% | 26.39 MiB ±37%! | 0.01 | 0.01 |
| host_anon_end | 33.08 MiB ±5% | 65.81 MiB ±15%! | 80.21 MiB ±36%! | 0.50 | 0.41 |
| host_anon_post | 33.08 MiB ±5% | 65.81 MiB ±15%! | 131.74 MiB ±36%! | 0.50 | 0.25 |
| host_anon_ready | 15.09 MiB ±34%! | 43.24 MiB ±97%! | 73.46 MiB ±77%! | 0.35 | 0.21 |
| host_cchild_ms_end | 10 ±6200%! | 10 ±0% | 20 ±50%! | 1.00 | 0.50 |
| host_cchild_ms_post | 10 ±6200%! | 10 ±0% | 20 ±50%! | 1.00 | 0.50 |
| host_cchild_ms_ready | 0 ±0% | 0 ±0% | 0 ±0% | - | - |
| host_cpu_ms_end | 350 ±26%! | 480 ±92%! | 1700 ±61%! | 0.73 | 0.21 |
| host_cpu_ms_post | 350 ±26%! | 480 ±92%! | 1530 ±63%! | 0.73 | 0.23 |
| host_cpu_ms_ready | 50 ±100%! | 170 ±218%! | 610 ±111%! | 0.29 | 0.08 |
| host_fds_end | 14 ±0% | 16 ±0% | 18 ±0% | 0.88 | 0.78 |
| host_fds_post | 14 ±0% | 16 ±0% | 24 ±8% | 0.88 | 0.58 |
| host_fds_ready | 13 ±0% | 15 ±0% | 18 ±0% | 0.87 | 0.72 |
| host_peak_rss_end | 75.86 MiB ±2% | 111.34 MiB ±21%! | 184.95 MiB ±25%! | 0.68 | 0.41 |
| host_peak_rss_post | 75.86 MiB ±2% | 111.34 MiB ±21%! | 186.53 MiB ±25%! | 0.68 | 0.41 |
| host_peak_rss_ready | 55.20 MiB ±11% | 84.25 MiB ±57%! | 125.64 MiB ±45%! | 0.66 | 0.44 |
| host_pss_end | 73.95 MiB ±30%! | 103.08 MiB ±24%! | 132.42 MiB ±22%! | 0.72 | 0.56 |
| host_pss_post | 56.77 MiB ±50%! | 93.49 MiB ±24%! | 184.07 MiB ±26%! | 0.61 | 0.31 |
| host_pss_ready | 39.32 MiB ±67%! | 81.82 MiB ±58%! | 123.46 MiB ±46%! | 0.48 | 0.32 |
| host_rss_end | 75.86 MiB ±2% | 109.83 MiB ±8% | 134.88 MiB ±21%! | 0.69 | 0.56 |
| host_rss_post | 75.86 MiB ±2% | 109.83 MiB ±8% | 186.53 MiB ±25%! | 0.69 | 0.41 |
| host_rss_ready | 55.20 MiB ±11% | 84.25 MiB ±57%! | 125.64 MiB ±45%! | 0.66 | 0.44 |
| host_threads_end | 10 ±0% | 10 ±0% | 11 ±0% | 1.00 | 0.91 |
| host_threads_post | 10 ±0% | 10 ±0% | 11 ±0% | 1.00 | 0.91 |
| host_threads_ready | 7 ±0% | 7 ±14% | 11 ±0% | 1.00 | 0.64 |
| llm_bytes | 1.48 MiB ±0% | 1.48 MiB ±0% | 1.48 MiB ±0% | 1.00 | 1.00 |
| llm_requests | 60 ±0% | 60 ±0% | 60 ±0% | 1.00 | 1.00 |
| ls_bytes | 2.07 MiB ±0% | 2.16 MiB ±2% | 2.15 MiB ±1% | 0.96 | 0.97 |
| ls_decoded_bytes | 2.07 MiB ±0% | 2.16 MiB ±2% | 2.15 MiB ±1% | 0.96 | 0.97 |
| ls_info | 1 ±0% | 1 ±0% | 1 ±0% | 1.00 | 1.00 |
| ls_ingest_requests | 9 ±0% | 26 ±38% | 18 ±28% | 0.35 | 0.50 |
| ls_multipart | 9 ±0% | 26 ±38% | 18 ±28% | 0.35 | 0.50 |
| ls_requests | 10 ±0% | 27 ±37% | 19 ±26% | 0.37 | 0.53 |
| mallocs_end | 1292816 ±11% | - | - | - | - |
| mallocs_post | 1292760 ±11% | - | - | - | - |
| mallocs_ready | 10104 ±1% | - | - | - | - |
| mallocs_workload | 1282655 ±11% | - | - | - | - |
| num_gc_end | 58 ±7% | 13 ±8% | 14.5 ±14% | 4.46 | 4.00 |
| num_gc_post | 56 ±7% | 8 ±12% | 9.5 ±21%! | 7.00 | 5.89 |
| num_gc_ready | 0 ±0% | 2 ±0% | 2 ±50%! | 0.00 | 0.00 |
| num_gc_workload | 58 ±7% | 11 ±9% | 12.5 ±24%! | 5.27 | 4.64 |
| peak_ext_anonymous | 9.54 MiB ±25%! | 185.51 MiB ±3% | 131.80 MiB ±36%! | 0.05 | 0.07 |
| peak_ext_pss | 17.53 MiB ±14% | 239.47 MiB ±2% | 184.16 MiB ±26%! | 0.07 | 0.10 |
| peak_ext_rss | 17.53 MiB ±14% | 243.81 MiB ±2% | 186.61 MiB ±25%! | 0.07 | 0.09 |
| peak_tree_anonymous | 41.68 MiB ±11% | 254.68 MiB ±3% | 131.80 MiB ±36%! | 0.16 | 0.32 |
| peak_tree_pss | 89.42 MiB ±28%! | 345.51 MiB ±6% | 184.16 MiB ±26%! | 0.26 | 0.49 |
| peak_tree_rss | 92.46 MiB ±5% | 355.65 MiB ±4% | 186.61 MiB ±25%! | 0.26 | 0.50 |
| startup_ms | 75.4 ±719%! | 811.6 ±93%! | 487.7 ±109%! | 0.09 | 0.15 |
| sys_asis_end | 21.10 MiB ±19%! | 173.20 MiB ±3% | 135.04 MiB ±18%! | 0.12 | 0.16 |
| sys_asis_post | 21.10 MiB ±19%! | 231.63 MiB ±3% | 187.81 MiB ±23%! | 0.09 | 0.11 |
| sys_asis_ready | 11.96 MiB ±33%! | 168.75 MiB ±3% | 126.14 MiB ±43%! | 0.07 | 0.09 |
| sys_gc_end | 21.10 MiB ±19%! | 173.20 MiB ±3% | 135.04 MiB ±18%! | 0.12 | 0.16 |
| sys_gc_post | 21.10 MiB ±19%! | 231.49 MiB ±3% | 187.81 MiB ±23%! | 0.09 | 0.11 |
| sys_gc_ready | 12.59 MiB ±34%! | 169.50 MiB ±3% | 126.93 MiB ±43%! | 0.07 | 0.10 |
| to_first_request_ms | 14.8 ±21%! | 68.8 ±29%! | 67.2 ±68%! | 0.22 | 0.22 |
| to_first_tool_ms | 16.8 ±19%! | 74.2 ±28%! | 86.5 ±71%! | 0.23 | 0.19 |
| tool_ms:bash | 2.8 ±38%! | 4.6 ±130%! | 6.7 ±114%! | 0.62 | 0.42 |
| tool_ms:read | 0.5 ±72%! | 0.9 ±218%! | 2.1 ±344%! | 0.52 | 0.22 |
| tool_ms:write | 0.5 ±230%! | 1.0 ±109%! | 3.5 ±298%! | 0.52 | 0.14 |
| total_alloc_end | 101.86 MiB ±6% | - | - | - | - |
| total_alloc_post | 101.85 MiB ±6% | - | - | - | - |
| total_alloc_ready | 0.77 MiB ±1% | - | - | - | - |
| total_alloc_workload | 101.09 MiB ±6% | - | - | - | - |
| tree_pss_end | 88.55 MiB ±25%! | 273.39 MiB ±9% | 132.42 MiB ±22%! | 0.32 | 0.67 |
| tree_pss_post | 72.45 MiB ±40%! | 331.41 MiB ±7% | 184.07 MiB ±26%! | 0.22 | 0.39 |
| tree_pss_ready | 48.78 MiB ±54%! | 249.92 MiB ±18%! | 123.46 MiB ±46%! | 0.20 | 0.40 |
| tree_rss_end | 90.95 MiB ±3% | 283.82 MiB ±3% | 134.88 MiB ±21%! | 0.32 | 0.67 |
| tree_rss_post | 90.95 MiB ±3% | 355.10 MiB ±3% | 186.53 MiB ±25%! | 0.26 | 0.49 |
| tree_rss_ready | 64.57 MiB ±9% | 256.73 MiB ±18%! | 125.64 MiB ±45%! | 0.25 | 0.51 |
| turn_ms | 761.5 ±21%! | 973.2 ±27%! | 683.2 ±27%! | 0.78 | 1.11 |
| wall_ms | 13257.7 ±5% | 14018.3 ±7% | 13403.2 ±6% | 0.95 | 0.99 |
| work_ms | 764.6 ±21%! | 976.7 ±27%! | 686.2 ±26%! | 0.78 | 1.11 |

