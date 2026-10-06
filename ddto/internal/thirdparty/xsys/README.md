# golang/sys source subset

- Repository: https://github.com/golang/sys
- Version: `v0.47.0`
- Commit: `9e7e939dcafac07e8ab4cffa6e5fc74908413f00`
- Original license: [LICENSE](LICENSE).
- File/source hashes and local adaptations: [SOURCE.json](SOURCE.json).

Windows retry processes use the Job Object, thread and timer functions in
`windows/`. OS metadata uses read-only registry operations in `windows/registry/`.
Unix kernel metadata lives in `unix/`. Only the required declarations are copied;
this directory does not provide the complete `x/sys` API.

BSD metadata keeps the upstream fixed-buffer reads and partial-result behavior.
Solaris keeps its small runtime trampoline; Linux and AIX use standard-library
syscalls. [EXTRACTION.json](EXTRACTION.json) records each selected declaration
and its source hash. ABI, registry and metadata tests stay beside the code.

Follow the [shared audit and update procedure](../README.md#audit-and-update).
