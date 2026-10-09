# Compile-only comparisons

Mini: `20caa458420f567c65a15ca5124696a09714556a`, collected 2026-10-09. Native/Orchestrion: retained from 2026-10-04 at `9d4786fbd27f573bb68c5516bf23b336e8d7bdaa`.

All variants use `go version go1.27.1 linux/amd64`, the recorded subject versions, flags, CPU affinity and repetition counts. Each observation includes wall time, aggregate cgroup CPU and memory, command and exit status. Cold builds start with an empty Go build cache; module downloads and the OS page cache remain warm. The other scenarios reuse unchanged output, force a fresh link, edit a reachable test body or edit an unused constant.

Mini traces and symbols are checked again. Native and Orchestrion retain their original qualification. Those variants were not rerun. Dates differ, so small timing differences need the recorded ranges and uncertainty. Orchestrion's percentage compares time against Native. Mini's percentages compare Orchestrion first, then Native. All time cells are seconds.
