# philhofer/fwd source subset

- Repository: https://github.com/philhofer/fwd
- Version: `v1.2.0`
- Commit: `20a13a1f6b7cb47a126dcb75152e21e1383bbaba`
- Original license: [LICENSE.md](LICENSE.md).
- File/source hashes and local adaptations: [SOURCE.json](SOURCE.json).

MessagePack encoding and decoding use this subset's buffered reader and writer.
The sources and tests keep their upstream filenames and root package layout.
Mini imports this internal copy without requiring the external module.

Follow the [shared audit and update procedure](../README.md#audit-and-update).
