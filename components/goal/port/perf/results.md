# Raw performance numbers (port/perf)

host: ab244769f391, 8 CPUs, Intel(R) Xeon(R) Platinum 8468
started: 2026-10-20T18:44:25Z
go: go version go1.27.2 linux/amd64
node: v24.19.0
pig: 0.5.0+1.3.0.1402973a
kernel: 7.0.0-38-generic
finished: 2026-10-20T19:01:21Z
load average at the end: 14.56, 12.49, 13.85
CPUs for the measured processes: 64-71 (taskset), in a container limited to those CPUs; other jobs ran on the host meanwhile

## Binary sizes

```
/mycache/final/bin:
total 50504
-rw-r--r-- 1 160150820 users      897 Oct 20 18:44 deps-new.txt
-rw-r--r-- 1 160150820 users      897 Oct 20 18:44 deps-old.txt
-rwxr-xr-x 1 160150820 users 15544583 Oct 20 18:44 ext-new
-rwxr-xr-x 1 160150820 users 19585338 Oct 20 18:44 ext-new-sym
-rw-r--r-- 1 160150820 users   676914 Oct 20 18:44 ext-new.nm
-rwxr-xr-x 1 160150820 users  6242567 Oct 20 18:44 ext-old
-rwxr-xr-x 1 160150820 users  9153511 Oct 20 18:44 ext-old-sym
-rw-r--r-- 1 160150820 users   472849 Oct 20 18:44 ext-old.nm
-rw-r--r-- 1 160150820 users        7 Oct 20 18:44 orig-ts-bytes
-rw-r--r-- 1 160150820 users      522 Oct 20 18:44 pitui-dictionaries.txt
-rw-r--r-- 1 160150820 users     3909 Oct 20 18:44 sections-new.txt
-rw-r--r-- 1 160150820 users     3903 Oct 20 18:44 sections-old.txt

/mycache/final/pl/bin:
total 210364
-rwxr-xr-x 1 160150820 users 70861063 Oct 20 18:44 control
-rwxr-xr-x 1 160150820 users 73347335 Oct 20 18:44 new
-rwxr-xr-x 1 160150820 users 71192839 Oct 20 18:44 old
```

ELF section sizes of the standalone extension (stripped build as PiG makes it is the file size above; these are the unstripped build's sections, bytes):

| section | port | 0.1.1 |
|---|---|---|
| `.text` | 4,333,489 | 2,885,713 |
| `.rodata` | 6,652,281 | 176,185 |
| `.gopclntab` | 3,027,096 | 2,160,861 |
| `.go.buildinfo` | 784 | 464 |
| `.noptrdata` | 543,649 | 297,729 |
| `.data` | 115,330 | 58,322 |
| `.bss` | 144,920 | 139,992 |
| `.noptrbss` | 33,813,888 | 33,682,616 |

Symbol bytes by package (unstripped build, `go tool nm -size`, text, read-only and data symbols; total 39,321,121 port, 37,300,297 0.1.1):

| package | port | 0.1.1 |
|---|---|---|
| `crypto/internal/fips140/drbg` | 33,558,584 | 33,558,584 |
| `github.com/MichaelKinsy/pigpen/pi-goal-x` | 1,113,267 | 176,006 |
| `runtime` | 731,609 | 719,041 |
| `func` | 565,936 | 387,240 |
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
-rw-r--r-- 1 160150820 users  537893 Oct 20 17:12 /w/components/pitui/wordsegmenter/burmese_dictionary.bin
-rw-r--r-- 1 160150820 users 4212243 Oct 20 17:12 /w/components/pitui/wordsegmenter/cjk_dictionary.bin
-rw-r--r-- 1 160150820 users  974071 Oct 20 17:12 /w/components/pitui/wordsegmenter/khmer_dictionary.bin
-rw-r--r-- 1 160150820 users  335860 Oct 20 17:12 /w/components/pitui/wordsegmenter/lao_dictionary.bin
-rw-r--r-- 1 160150820 users  260897 Oct 20 17:12 /w/components/pitui/wordsegmenter/thai_dictionary.bin
```

The extension binary as PiG builds and caches it (`$PIG_HOME/cache/cells/go/<hash>/runner`, bytes): new 15,519,904, old 6,213,792.

## In-process (median of 12 interleaved rounds)

Go: ns, B and allocs per operation (`go test -bench -benchmem`). Original: ns per operation; B is the growth of the V8 used heap over the timed sections (a lower bound; `gc` rows were not measurable). 0.1.1: the commands it implemented.

| operation | port | B/op | allocs/op | original | B/op (V8) | port/original | 0.1.1 | port/0.1.1 |
|---|---|---|---|---|---|---|---|---|
| SessionStart | 505.8 µs | 155.4 KiB | 2723 | 1.00 ms | 80.3 KiB | -50% | 130.9 µs | +286% |
| ToolGetGoalSummary | 64.8 µs | 18.6 KiB | 257 | 71.4 µs | 29.8 KiB | -9% | n/a (refuses) | n/a |
| ToolGetGoalTasks | 150.4 µs | 70.5 KiB | 622 | 40.6 µs | 21.0 KiB | +270% | n/a (refuses) | n/a |
| ToolCreateGoal | 1.49 ms | 322.5 KiB | 4764 | 1.56 ms | 166.4 KiB | -4% | n/a (refuses) | n/a |
| ToolUpdateGoalPause | 1.11 ms | 252.9 KiB | 3286 | 1.02 ms | 111.8 KiB | +9% | n/a (refuses) | n/a |
| ToolSetGoalTasks | 1.22 ms | 316.1 KiB | 4035 | 1.03 ms | 150.9 KiB | +18% | n/a (refuses) | n/a |
| ToolUpdateGoalTask | 1.16 ms | 249.3 KiB | 3286 | 1.06 ms | 122.6 KiB | +10% | n/a (refuses) | n/a |
| CmdGoalList | 78.9 µs | 10.3 KiB | 183 | 12.6 µs | 11.2 KiB | +528% | 16.7 µs | +373% |
| CmdGoalStatus | 137.3 µs | 23.5 KiB | 295 | 77.6 µs | 42.5 KiB | +77% | n/a | n/a |
| CmdGoalStatusVerbose | 142.3 µs | 28.8 KiB | 412 | 90.4 µs | 55.7 KiB | +58% | n/a | n/a |
| CmdGoalRefresh | 383.3 µs | 110.9 KiB | 1792 | 111.2 µs | 36.3 KiB | +245% | n/a | n/a |
| CmdGoalDirect | 1.43 ms | 316.1 KiB | 4705 | 1.39 ms | 153.2 KiB | +3% | 1.14 ms | +25% |
| RenderDashboardCompact | 73.6 µs | 6.5 KiB | 88 | 67.3 µs | 25.8 KiB | +9% | n/a | n/a |
| RenderDashboardExpanded | 101.3 µs | 10.8 KiB | 121 | 90.8 µs | 36.6 KiB | +11% | n/a | n/a |
| KeyPlain | 2.5 µs | 184 B | 4 | 17.6 µs | 8.6 KiB | -86% | n/a | n/a |
| KeyToggleExpand | 81.0 µs | 10.1 KiB | 130 | 10.5 µs | 4.2 KiB | +673% | n/a | n/a |
| SchedulerTick | 1.20 ms | 287.5 KiB | 4129 | 1.15 ms | 142.7 KiB | +5% | n/a | n/a |
| BigLedgerSessionStart | 1.01 ms | 384.9 KiB | 7604 | 1.28 ms | 80.3 KiB | -21% | n/a | n/a |
| BigLedgerGetGoalSummary | 58.0 µs | 18.4 KiB | 257 | 64.1 µs | 36.1 KiB | -10% | n/a | n/a |
| BigLedgerGoalStatus | 2.92 ms | 1255.6 KiB | 2321 | 2.09 ms | 657.9 KiB | +39% | n/a | n/a |
| BigLedgerRender | 65.4 µs | 7.3 KiB | 94 | 91.2 µs | 27.8 KiB | -28% | n/a | n/a |
| BigLedgerKeyToggleExpand | 78.1 µs | 9.2 KiB | 120 | 11.5 µs | 4.4 KiB | +581% | n/a | n/a |
| BigLedgerUpdateGoalTask | 1.34 ms | 258.0 KiB | 3292 | 1.06 ms | 125.9 KiB | +27% | n/a | n/a |

RenderPush (the port's dashboard layout plus the push of its lines, done on every UI update; the original lays out in the host): 62.8 µs, 6.0 KiB, 88 allocs.

Per-round values (ns/op), port | original | 0.1.1:

- SessionStart: 508508 516092 492483 610748 503160 569009 501234 561669 508728 497698 488306 489544 | 509648 1767005 1225119 418751 1136046 628564 1935564 636272 833799 868031 1747184 1749380 | 137338 155915 137309 119248 187009 113738 120587 127463 145156 134364 125987 124609
- ToolGetGoalSummary: 84022 67317 67043 60094 64398 61500 65163 65174 63282 62275 58983 72043 | 44303 71270 71448 69217 71394 72855 70995 72005 71492 71654 71235 74037 | -
- ToolGetGoalTasks: 151343 151116 161944 133281 142032 143549 149585 156931 146821 151851 159538 148782 | 23549 40718 39932 39163 41437 41098 41497 39916 42333 41714 40443 40170 | -
- ToolCreateGoal: 1424258 1527860 1403522 1874607 1293506 1509167 1434592 1501399 1457173 1634854 1498927 1489410 | 1068878 1622013 1516196 1665586 1402805 1471626 1239005 1477303 1727278 1646352 1601485 1597256 | -
- ToolUpdateGoalPause: 1136212 1118392 887118 1347148 897241 980095 1101683 1092139 1147851 1127191 1106366 1125469 | 700458 1054835 781597 1076774 853196 983747 659039 1001451 1064494 1149303 1134541 1041898 | -
- ToolSetGoalTasks: 1252948 1228567 883534 1131125 1110468 987900 1142839 1241630 1215957 1288974 1286222 1245211 | 830080 1172879 885513 840729 945614 811151 849218 1145786 1203899 1219162 1119822 1195112 | -
- ToolUpdateGoalTask: 1120973 1268192 1179095 868523 1054537 1282977 865104 1136740 1174172 1177431 1153626 1176324 | 847617 1169275 1169247 749981 972656 692122 776061 1028427 1165038 1085599 1082996 1088914 | -
- CmdGoalList: 75860 81953 76051 78338 74244 80388 78138 87864 76895 79412 86418 83160 | 7302 11859 12601 12507 12815 11492 12890 13382 12713 11860 13277 11480 | 15208 17597 17251 14026 15401 16852 16540 18564 16811 15734 16941 16041
- CmdGoalStatus: 131502 136175 141951 130173 139209 135061 137718 139259 136890 140720 136281 149158 | 45031 78542 77422 76735 77535 47116 77596 80202 78392 77172 77725 81122 | -
- CmdGoalStatusVerbose: 159906 146466 147822 143753 135776 135866 145151 152011 140944 129303 138123 140626 | 57102 89574 90932 89369 89674 55156 90450 90292 92157 90703 92508 96451 | -
- CmdGoalRefresh: 397716 386576 405984 532411 373601 416370 368909 381122 379677 371033 370048 385389 | 67997 110024 110642 118508 115926 70871 110833 115068 114120 111602 109597 111778 | -
- CmdGoalDirect: 1406706 1461616 1426582 1093935 1457541 1202714 1042751 1412694 1581379 1487352 1424294 1493620 | 1143015 1342822 1483812 1178965 1318271 1195503 962261 1431956 1706623 1489888 1471278 1451712 | 895200 1250696 1069792 1149024 1304714 1138650 985124 1061941 1326083 1151913 1046746 1177525
- RenderDashboardCompact: 71553 86422 74837 67479 73242 76215 71074 69103 73935 69961 82047 78297 | 39071 66842 73350 55386 68895 38494 68536 65934 67706 71502 66734 69347 | -
- RenderDashboardExpanded: 104584 101198 104552 90909 113630 96077 101158 105110 95442 97361 101514 101306 | 54136 91004 96087 67865 114655 51024 92555 90470 92139 90656 90696 97976 | -
- KeyPlain: 2659 2668 2685 2523 2507 2536 2584 2497 2532 2586 2524 2492 | 17635 18184 17415 17581 18717 21208 17486 17350 17608 17590 19114 17497 | -
- KeyToggleExpand: 81030 87343 84945 75646 79184 82106 81040 85484 77720 80448 80933 83126 | 10760 10820 10848 10497 10376 10485 10345 10347 10468 10457 11155 10427 | -
- SchedulerTick: 1047493 1207986 1221110 953120 1053344 1247189 973015 1227144 1296972 1189892 1201710 1223688 | 861503 1165092 1153121 1150705 1106405 1120596 769696 1166466 1348940 1186647 1186111 1139577 | -
- BigLedgerSessionStart: 1008324 974084 1017551 967624 1003585 1000191 1067207 1052069 1067352 966353 1005640 1254153 | 796993 2091247 376835 279497 397682 1235625 1812891 1320269 1045314 1383806 1613911 1651665 | -
- BigLedgerGetGoalSummary: 70120 60233 52338 58115 55347 56375 61650 57410 51081 59786 80147 57875 | 64800 49249 65320 43981 38073 62358 63161 63486 65847 66177 65128 64721 | -
- BigLedgerGoalStatus: 3020818 3072457 2868389 2761787 2977922 2759319 2751581 2819967 3195524 2911465 2931023 3006843 | 2140303 1364281 2105075 1375446 1398249 2037640 2045742 2105248 2305002 2151476 2198580 2083127 | -
- BigLedgerRender: 64898 71980 65917 80348 65215 63611 64560 65507 62876 69910 64391 85138 | 90872 53026 91607 60844 52271 90638 92210 92712 92688 97400 96663 90734 | -
- BigLedgerKeyToggleExpand: 78354 81281 83776 86676 76674 75041 105806 77048 77863 74342 82135 74445 | 11228 6909 11939 7694 6561 11493 12023 10946 11523 11623 11710 11446 | -
- BigLedgerUpdateGoalTask: 1400401 1382938 1272202 1231316 1422940 1292228 1283766 1288116 1417385 1343494 1345132 1369798 | 936044 1045708 966691 944795 1037694 1070260 1049566 1079110 1110126 1123530 1130855 1065517 | -

## Through a real pig (RPC mode, 12 interleaved runs, CPUs 64-71)

### Cold start: spawn of pig to the `get_commands` response (ms)

| subject | median | min | max | extension's share over the control | pig's trace: extension spawn to handshake |
|---|---|---|---|---|---|
| control | 29.9 | 19.4 | 36.3 |  | n/a |
| new | 121.8 | 91.5 | 160.5 | +91.9 ms | 19.0 ms |
| old | 51.4 | 38.8 | 74.3 | +21.5 ms | 15.0 ms |
| orig | 683.5 | 520.1 | 752.8 | +653.6 ms | 532.0 ms |
| control-piglet | 33.9 | 29.2 | 42.5 |  | 2.0 ms |
| new-piglet | 50.1 | 38.9 | 51.8 | +16.2 ms | 3.0 ms |
| old-piglet | 40.9 | 39.1 | 50.8 | +7.0 ms | 2.0 ms |

First start of each subject (the warm-up round: PiG builds a Go extension from source, jiti transpiles the TypeScript; a Piglet needs neither), ms: control 19, new 593, old 410, orig 2064, control-piglet 50, new-piglet 49, old-piglet 60


Every round's cold start (ms): 

- control: 29.3 23.6 29.4 30.4 30.3 36.3 32.4 30.3 29.5 19.4 31.4 28.9
- new: 128.3 117.7 138.8 122.3 117.7 160.5 129.8 132.4 121.3 91.5 108.6 112.7
- old: 51.0 49.2 47.8 74.3 51.9 64.7 38.8 68.3 66.5 49.5 49.4 62.3
- orig: 686.7 667.4 529.1 520.1 680.9 752.8 719.3 723.3 686.1 537.7 564.3 692.0
- control-piglet: 40.7 41.7 33.3 40.2 34.5 32.3 30.6 29.2 41.1 42.5 31.0 32.1
- new-piglet: 50.8 42.1 51.7 49.5 50.7 41.1 38.9 49.0 51.4 51.7 44.5 51.8
- old-piglet: 40.8 39.1 40.8 39.3 49.5 50.8 41.8 39.6 40.9 41.7 39.8 42.0

Pig's own marks, median (ms since process start): 

| subject | extension-discovery-done | extensions-loaded | extension phases |
|---|---|---|---|
| control | 4 | 5 |  |
| new | 48 | 101 | discover-start 52; discover-done 59; build-check-start 60; build-check-done 81; spawn-start 82; spawn-done 82; handshake-start 98; handshake-done 100 |
| old | 12 | 34 | discover-start 14; discover-done 16; build-check-start 16; build-check-done 20; spawn-start 20; spawn-done 20; handshake-start 31; handshake-done 33 |
| orig | 4 | 545 | discover-start 7; discover-done 7; build-check-start 7; build-check-done 8; spawn-start 14; spawn-done 14; handshake-start 536; handshake-done 544 |
| control-piglet | 5 | 12 | spawn-start 8; spawn-done 8; handshake-start 8; handshake-done 10 |
| new-piglet | 5 | 13 | spawn-start 8; spawn-done 9; handshake-start 9; handshake-done 12 |
| old-piglet | 6 | 13 | spawn-start 9; spawn-done 9; handshake-start 9; handshake-done 11 |

### Tools and commands (ms; each run's median over its calls, then the median over runs; the first call of each tool in a run is dropped, except update_goal, which runs once per run)

| operation | new | orig | new-piglet | new/orig (fused vs orig where both) |
|---|---|---|---|---|
| create_goal | 2.79 | 5.01 | 2.48 | -44% |
| set_goal_tasks | 4.15 | 5.01 | 3.45 | -17% |
| get_goal | 1.74 | 2.94 | 1.33 | -41% |
| get_goal tasks | 2.31 | 3.24 | 1.73 | -29% |
| update_goal_task | 0.52 | 0.84 | 0.34 | -38% |
| update_goal | 2.20 | 3.79 | 2.29 | -42% |
| goal-list | 1.69 | 2.62 | 1.65 | -36% |
| goal-status | 2.70 | 3.72 | 2.34 | -27% |
| goal-status verbose | 2.43 | 4.06 | 2.35 | -40% |
| goal-refresh | 1.94 | 2.53 | 2.01 | -23% |

