# Raw performance numbers (port/perf)

host: c78fe6992b94, 8 CPUs, Intel(R) Xeon(R) Platinum 8468
started: 2026-10-20T21:33:33Z
go: go version go1.27.2 linux/amd64
node: v24.19.0
pig: 0.5.0+1.3.0.1402973a
kernel: 7.0.0-38-generic
finished: 2026-10-20T21:47:01Z
load average after the run (1, 5, 15 min): 44.28, 29.71, 23.63
CPUs for the measured processes: 64-71 (taskset), in a container limited to those CPUs; other jobs ran on the host meanwhile

## Binary sizes

```
/mycache/final/bin:
total 50532
-rw-r--r-- 1 160150820 users      897 Oct 20 21:33 deps-new.txt
-rw-r--r-- 1 160150820 users      897 Oct 20 21:33 deps-old.txt
-rwxr-xr-x 1 160150820 users 15556871 Oct 20 21:33 ext-new
-rwxr-xr-x 1 160150820 users 19601478 Oct 20 21:33 ext-new-sym
-rw-r--r-- 1 160150820 users   677892 Oct 20 21:33 ext-new.nm
-rwxr-xr-x 1 160150820 users  6242567 Oct 20 21:33 ext-old
-rwxr-xr-x 1 160150820 users  9153511 Oct 20 21:33 ext-old-sym
-rw-r--r-- 1 160150820 users   472849 Oct 20 21:33 ext-old.nm
-rw-r--r-- 1 160150820 users        7 Oct 20 21:33 orig-ts-bytes
-rw-r--r-- 1 160150820 users      522 Oct 20 21:33 pitui-dictionaries.txt
-rw-r--r-- 1 160150820 users     3909 Oct 20 21:33 sections-new.txt
-rw-r--r-- 1 160150820 users     3903 Oct 20 21:33 sections-old.txt

/mycache/final/pl/bin:
total 210376
-rwxr-xr-x 1 160150820 users 70861063 Oct 20 21:34 control
-rwxr-xr-x 1 160150820 users 73359623 Oct 20 21:34 new
-rwxr-xr-x 1 160150820 users 71192839 Oct 20 21:34 old
```

ELF section sizes of the standalone extension (stripped build as PiG makes it is the file size above; these are the unstripped build's sections, bytes):

| section | port | 0.1.1 |
|---|---|---|
| `.text` | 4,339,889 | 2,885,713 |
| `.rodata` | 6,652,281 | 176,185 |
| `.gopclntab` | 3,030,713 | 2,160,861 |
| `.go.buildinfo` | 784 | 464 |
| `.noptrdata` | 543,649 | 297,729 |
| `.data` | 115,362 | 58,322 |
| `.bss` | 144,952 | 139,992 |
| `.noptrbss` | 33,813,888 | 33,682,616 |

Symbol bytes by package (unstripped build, `go tool nm -size`, text, read-only and data symbols; total 39,328,229 port, 37,300,297 0.1.1):

| package | port | 0.1.1 |
|---|---|---|
| `crypto/internal/fips140/drbg` | 33,558,584 | 33,558,584 |
| `github.com/MichaelKinsy/pigpen/pi-goal-x` | 1,118,757 | 176,006 |
| `runtime` | 731,643 | 719,041 |
| `func` | 566,680 | 387,240 |
| `github.com/MichaelKinsy/PiG/extensions/sdk` | 265,015 | 214,995 |
| `github.com/MichaelKinsy/pigpen/components/pitui/widthx` | 235,286 | 0 |
| `encoding/json/v2` | 163,629 | 163,629 |
| `slices` | 162,317 | 144,208 |
| `net` | 143,547 | 137,613 |
| `crypto/internal/fips140/nistec` | 143,013 | 143,013 |
| `github.com/MichaelKinsy/pigpen/components/pitui` | 123,250 | 0 |
| `github.com/clipperhouse/uax29/v2/words` | 109,129 | 0 |
| `github.com/MichaelKinsy/PiG/extensions/sdk/json` | 105,787 | 105,787 |
| `reflect` | 98,860 | 98,850 |
| `math/big` | 79,592 | 79,592 |
| `golang.org/x/text/unicode/norm` | 76,619 | 0 |

pitui's embedded word-segmentation dictionaries (`go:embed`, linked through `pitui.Editor`): 6,320,964 bytes in `.rodata`:

```
-rw-r--r-- 1 160150820 users  537893 Oct 20 21:08 /w/components/pitui/wordsegmenter/burmese_dictionary.bin
-rw-r--r-- 1 160150820 users 4212243 Oct 20 21:08 /w/components/pitui/wordsegmenter/cjk_dictionary.bin
-rw-r--r-- 1 160150820 users  974071 Oct 20 21:08 /w/components/pitui/wordsegmenter/khmer_dictionary.bin
-rw-r--r-- 1 160150820 users  335860 Oct 20 21:08 /w/components/pitui/wordsegmenter/lao_dictionary.bin
-rw-r--r-- 1 160150820 users  260897 Oct 20 21:08 /w/components/pitui/wordsegmenter/thai_dictionary.bin
```

The extension binary as PiG builds and caches it (`$PIG_HOME/cache/cells/go/<hash>/runner`, bytes): new 15,532,192, old 6,213,792.

## In-process (median of 12 interleaved rounds)

Go: ns, B and allocs per operation (`go test -bench -benchmem`). Original: ns per operation; B is the growth of the V8 used heap over the timed sections (a lower bound; `gc` rows were not measurable). 0.1.1: the commands it implemented.

| operation | port | B/op | allocs/op | original | B/op (V8) | port/original | 0.1.1 | port/0.1.1 |
|---|---|---|---|---|---|---|---|---|
| SessionStart | 341.3 µs | 155.2 KiB | 2723 | 294.6 µs | 80.2 KiB | +16% | 84.8 µs | +302% |
| ToolGetGoalSummary | 40.0 µs | 18.6 KiB | 257 | 50.4 µs | 29.8 KiB | -21% | n/a (refuses) | n/a |
| ToolGetGoalTasks | 41.3 µs | 32.7 KiB | 238 | 28.2 µs | 21.0 KiB | +47% | n/a (refuses) | n/a |
| ToolCreateGoal | 1.11 ms | 322.6 KiB | 4764 | 1.11 ms | 166.3 KiB | +0% | n/a (refuses) | n/a |
| ToolUpdateGoalPause | 857.5 µs | 252.9 KiB | 3286 | 799.9 µs | 112.2 KiB | +7% | n/a (refuses) | n/a |
| ToolSetGoalTasks | 922.8 µs | 316.2 KiB | 4035 | 851.5 µs | 150.9 KiB | +8% | n/a (refuses) | n/a |
| ToolUpdateGoalTask | 866.5 µs | 249.2 KiB | 3286 | 782.7 µs | 122.6 KiB | +11% | n/a (refuses) | n/a |
| CmdGoalList | 53.4 µs | 10.3 KiB | 183 | 7.6 µs | 11.2 KiB | +607% | 10.9 µs | +391% |
| CmdGoalStatus | 85.1 µs | 23.5 KiB | 295 | 55.6 µs | 42.5 KiB | +53% | n/a | n/a |
| CmdGoalStatusVerbose | 83.5 µs | 28.8 KiB | 412 | 61.9 µs | 55.7 KiB | +35% | n/a | n/a |
| CmdGoalRefresh | 244.7 µs | 110.7 KiB | 1785 | 77.7 µs | 36.3 KiB | +215% | n/a | n/a |
| CmdGoalDirect | 1.06 ms | 316.1 KiB | 4705 | 1.01 ms | 153.2 KiB | +5% | 839.4 µs | +27% |
| RenderDashboardCompact | 47.7 µs | 6.6 KiB | 88 | 47.2 µs | 25.8 KiB | +1% | n/a | n/a |
| RenderDashboardExpanded | 63.6 µs | 10.9 KiB | 121 | 61.0 µs | 36.6 KiB | +4% | n/a | n/a |
| KeyPlain | 1.7 µs | 184 B | 4 | 12.6 µs | 8.6 KiB | -86% | n/a | n/a |
| KeyToggleExpand | 54.5 µs | 8.5 KiB | 115 | 7.0 µs | 4.2 KiB | +675% | n/a | n/a |
| SchedulerTick | 892.6 µs | 287.5 KiB | 4129 | 834.9 µs | 142.7 KiB | +7% | n/a | n/a |
| BigLedgerSessionStart | 638.9 µs | 385.0 KiB | 7604 | 306.3 µs | 80.2 KiB | +109% | n/a | n/a |
| BigLedgerGetGoalSummary | 46.9 µs | 18.5 KiB | 257 | 46.6 µs | 36.0 KiB | +1% | n/a | n/a |
| BigLedgerGoalStatus | 2.17 ms | 1256.0 KiB | 2321 | 1.48 ms | 657.9 KiB | +46% | n/a | n/a |
| BigLedgerRender | 44.9 µs | 7.3 KiB | 94 | 62.6 µs | 27.8 KiB | -28% | n/a | n/a |
| BigLedgerKeyToggleExpand | 53.0 µs | 9.2 KiB | 120 | 7.7 µs | 4.4 KiB | +592% | n/a | n/a |
| BigLedgerUpdateGoalTask | 1.02 ms | 258.3 KiB | 3292 | 830.6 µs | 126.3 KiB | +23% | n/a | n/a |

RenderPush (the port's dashboard layout plus the push of its lines, done on every UI update; the original lays out in the host): 40.7 µs, 6.0 KiB, 88 allocs.

Per-round values (ns/op), port | original | 0.1.1:

- SessionStart: 320147 274948 302943 395802 354873 296855 482358 327686 631961 397849 567004 269710 | 300593 347180 304313 387289 285118 272514 346258 346714 271358 277433 264668 288623 | 88354 82007 86665 86432 73042 113753 99820 83219 89899 81011 81795 80856
- ToolGetGoalSummary: 38794 38172 42122 59825 39202 39873 63584 106381 40048 39940 46739 39607 | 49514 46422 53718 48403 54400 49376 70798 79753 58058 48872 51241 41988 | -
- ToolGetGoalTasks: 38864 66551 37360 73396 41949 37060 58497 79747 40519 40699 43478 38596 | 30375 24647 27345 28414 27933 30372 39627 45082 29316 27973 23749 23429 | -
- ToolCreateGoal: 1042287 881259 1028341 1232945 1075346 988943 1340666 1152122 1282704 1219913 1292881 1031482 | 1077765 1019273 1187187 1112389 1154697 990636 1449266 1522532 1293324 1107292 1114980 1061151 | -
- ToolUpdateGoalPause: 762546 651730 855921 1214488 778721 768888 1048523 1089998 1089661 859161 999765 761724 | 871433 730495 805681 731009 765785 639310 964209 902390 889308 794186 915617 726320 | -
- ToolSetGoalTasks: 1034125 790215 991028 871210 882597 894748 1027088 1160938 1033181 846157 950809 891976 | 854759 906339 824197 865772 838918 757910 715832 962726 1066484 848305 923551 723177 | -
- ToolUpdateGoalTask: 862456 760121 845461 954328 890310 754300 1058082 986983 910753 733436 870589 841851 | 807125 717720 795843 792647 758066 640082 637545 1082347 852956 768428 791070 774428 | -
- CmdGoalList: 60793 51922 46670 58112 57086 53020 67474 94824 51414 49721 53706 50795 | 7718 7386 7732 8260 7035 7345 11105 11789 8993 7073 6586 6698 | 13028 8882 9331 10721 11399 9958 13595 11001 12196 11214 10071 10391
- CmdGoalStatus: 87267 82516 88169 95272 88738 78243 118947 122909 81981 82825 82886 75649 | 53961 49873 53188 59968 45557 47351 77137 77504 87337 57266 74580 43336 | -
- CmdGoalStatusVerbose: 83485 82429 111813 83575 107113 81192 142399 103324 85765 81499 81942 70659 | 64361 53638 61720 62039 59659 58419 89519 80899 100896 61681 83315 58728 | -
- CmdGoalRefresh: 236749 213850 231389 229868 248899 240519 352151 265558 287932 254146 252343 196087 | 82902 77618 75076 78002 77741 74927 111658 76877 143515 66903 79080 67698 | -
- CmdGoalDirect: 1028331 893028 1122660 1077605 1046606 927405 1275422 1164912 1450289 876671 1037419 1145207 | 997972 942633 1024871 1229877 1062350 891159 923528 1358727 928921 1354451 1093585 894514 | 822737 818315 916871 923872 806766 1002817 1010429 874083 775130 855972 752563 765048
- RenderDashboardCompact: 48011 41774 45970 46451 42788 47345 56221 50692 49328 71065 61107 40075 | 67200 42161 45854 46132 46843 44864 66366 49079 72104 47664 47522 38316 | -
- RenderDashboardExpanded: 62398 95468 61651 62433 60979 60702 84875 80630 68944 64826 66459 54702 | 65344 51013 57865 62242 57246 60469 90583 56379 80526 74512 61459 53538 | -
- KeyPlain: 1541 1534 1705 1807 1844 1703 2724 3013 1521 1646 1688 1611 | 11624 10683 20416 12807 13313 11737 10547 14388 12568 12550 14349 12340 | -
- KeyToggleExpand: 51536 50498 49325 53601 57143 58871 69558 72446 51383 48030 61782 55412 | 7365 6569 8451 7010 6952 8068 5996 7053 6237 7241 7420 6152 | -
- SchedulerTick: 877532 879369 1365818 923706 862838 790528 1196484 1381492 905820 828321 877239 1050336 | 837115 731605 919144 758457 859225 748550 866905 1037259 836565 833193 778448 758818 | -
- BigLedgerSessionStart: 650114 638588 1146526 633908 606338 591053 900454 639196 693895 648641 545996 569405 | 308434 304187 319147 258593 290671 366441 426117 341920 246687 388751 277578 245742 | -
- BigLedgerGetGoalSummary: 39804 41722 64458 55145 46441 42087 61114 49546 46954 46811 33509 51165 | 45588 43327 47576 36228 42757 64454 63141 54670 38433 67490 58976 35952 | -
- BigLedgerGoalStatus: 2045468 1994942 2455900 2260302 2157537 2176970 2745337 2255400 2051482 2024388 2124348 2286314 | 1759495 1507832 1525806 1454703 1402364 1297194 2197566 1574273 1297999 1672514 1410879 1279897 | -
- BigLedgerRender: 43634 41819 44283 45600 68748 67114 67330 39077 70322 49723 37496 37930 | 66223 62200 67467 53983 60327 64292 90173 62586 62710 60965 62885 52481 | -
- BigLedgerKeyToggleExpand: 50850 76115 47831 55974 52802 89659 79917 47404 75138 53117 40967 44797 | 8475 7993 8303 6597 7740 6527 11652 7724 7580 7304 7567 6484 | -
- BigLedgerUpdateGoalTask: 1085431 1063270 1023160 992419 1004671 1134408 1228643 1001119 1025781 1106556 887909 943141 | 935825 775116 921680 756438 812292 703963 1109256 1068382 848910 871280 753324 745113 | -

## Through a real pig (RPC mode, 12 interleaved runs, CPUs 64-71)

### Cold start: spawn of pig to the `get_commands` response (ms)

| subject | median | min | max | extension's share over the control | pig's trace: extension spawn to handshake |
|---|---|---|---|---|---|
| control | 18.9 | 16.3 | 24.1 |  | n/a |
| new | 93.6 | 88.1 | 137.4 | +74.7 ms | 16.0 ms |
| old | 47.6 | 32.9 | 58.4 | +28.7 ms | 18.0 ms |
| orig | 521.6 | 435.6 | 728.3 | +502.7 ms | 398.5 ms |
| control-piglet | 31.3 | 29.2 | 37.6 |  | 1.0 ms |
| new-piglet | 40.2 | 38.9 | 55.0 | +8.9 ms | 2.5 ms |
| old-piglet | 40.5 | 38.2 | 45.9 | +9.2 ms | 2.0 ms |

First start of each subject (the warm-up round: PiG builds a Go extension from source, jiti transpiles the TypeScript; a Piglet needs neither), ms: control 30, new 1898, old 288, orig 1944, control-piglet 47, new-piglet 48, old-piglet 50


Every round's cold start (ms): 

- control: 18.7 18.0 17.9 19.2 16.3 19.0 20.8 17.2 19.9 18.5 20.9 24.1
- new: 95.5 113.4 92.6 90.6 88.1 117.4 88.9 137.4 93.0 90.1 94.2 120.3
- old: 58.4 47.2 53.6 47.6 49.6 46.8 53.7 36.3 46.3 50.2 32.9 47.6
- orig: 728.3 538.7 494.3 435.6 500.7 522.9 500.7 520.2 629.7 592.7 540.7 489.6
- control-piglet: 35.3 32.4 29.2 29.5 29.6 29.2 33.1 30.1 30.2 33.1 36.5 37.6
- new-piglet: 39.1 42.1 45.6 38.9 39.3 40.0 39.9 40.5 39.6 40.3 42.2 55.0
- old-piglet: 38.4 38.2 41.0 38.5 41.7 40.0 39.1 45.9 39.4 40.9 45.4 41.1

Pig's own marks, median (ms since process start): 

| subject | extension-discovery-done | extensions-loaded | extension phases |
|---|---|---|---|
| control | 3 | 4 |  |
| new | 35 | 80 | discover-start 38; discover-done 45; build-check-start 45; build-check-done 60; spawn-start 60; spawn-done 60; handshake-start 77; handshake-done 79 |
| old | 9 | 34 | discover-start 12; discover-done 12; build-check-start 12; build-check-done 16; spawn-start 16; spawn-done 16; handshake-start 32; handshake-done 33 |
| orig | 3 | 410 | discover-start 5; discover-done 5; build-check-start 5; build-check-done 6; spawn-start 10; spawn-done 10; handshake-start 406; handshake-done 410 |
| control-piglet | 4 | 9 | spawn-start 6; spawn-done 6; handshake-start 6; handshake-done 7 |
| new-piglet | 4 | 10 | spawn-start 7; spawn-done 7; handshake-start 7; handshake-done 9 |
| old-piglet | 4 | 10 | spawn-start 7; spawn-done 7; handshake-start 7; handshake-done 8 |

### Tools and commands (ms; each run's median over its calls, then the median over runs; the first call of each tool in a run is dropped, except update_goal, which runs once per run)

| operation | new | orig | new-piglet | new/orig (fused vs orig where both) |
|---|---|---|---|---|
| create_goal | 2.30 | 4.37 | 1.92 | -47% |
| set_goal_tasks | 3.33 | 4.23 | 2.92 | -21% |
| get_goal | 1.45 | 2.17 | 0.98 | -33% |
| get_goal tasks | 1.79 | 2.48 | 1.26 | -28% |
| update_goal_task | 0.48 | 0.68 | 0.29 | -30% |
| update_goal | 2.54 | 4.32 | 1.95 | -41% |
| goal-list | 1.38 | 2.12 | 1.16 | -35% |
| goal-status | 2.24 | 2.94 | 1.97 | -24% |
| goal-status verbose | 2.17 | 3.33 | 1.51 | -35% |
| goal-refresh | 1.49 | 1.86 | 1.45 | -20% |


## Control: the cache against the previous head (same host session, interleaved)

`go test -bench` of goal-pr-7's tests (no detail page cache) and this branch's, 12 interleaved rounds after a warm-up, 3,000 iterations each,
CPUs 64-71 (medians). Only `get_goal` tasks reads the cache; the others are unchanged code and show the host's noise (load average 44 at the time).

| operation | pr-7 | this branch | change |
|---|---|---|---|
| ToolGetGoalTasks | 95.7 µs | 38.3 µs | -60% |
| ToolGetGoalSummary | 40.0 µs | 39.0 µs | -3% |
| CmdGoalList | 49.8 µs | 51.7 µs | +4% |
| CmdGoalStatus | 80.1 µs | 81.7 µs | +2% |
| CmdGoalRefresh | 222.7 µs | 236.1 µs | +6% |
| RenderDashboardCompact | 44.6 µs | 49.0 µs | +10% (code not touched: noise) |

## Terminal key wait while a completion audit runs

`BenchmarkKeyWaitDuringAudit` (`perf_keywait_test.go`, `-benchtime 10x`, CPUs 64-95,160-191 under the heavy lock): `update_goal(complete)` runs
with a scripted auditor that reports progress every `progress` for 600 ms; from the audit's start until `update_goal` returns, a key (plain,
then the toggle chord, alternating) goes through the listener's `core.locked` every 300 µs. `status` is a sleep inside `SetStatus`, standing
for the SDK's blocking round trip. Wait = time of the listener call, all keys of the ten audits pooled.

| progress | SetStatus | keys | median | p99 | max |
|---|---|---|---|---|---|
| 1 ms | 0 | 5152 | 64.9 µs | 259 µs | 2.98 ms |
| 1 ms | 200 µs | 5169 | 63.5 µs | 232 µs | 3.19 ms |
| 1 ms | 1 ms | 5204 | 65.9 µs | 267 µs | 6.63 ms |
| 50 ms | 200 µs | 5405 | 26.0 µs | 160 µs | 3.34 ms |
