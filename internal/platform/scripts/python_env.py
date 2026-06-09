#!/usr/bin/env python
import json
import sys
import platform

tags = {
    "runtime.name": "python",
    "runtime.version": platform.python_version(),
    "os.platform": sys.platform,
    "os.architecture": platform.machine(),
    "os.version": platform.release(),
}

# Write to file path passed as argument
with open(sys.argv[1], 'w') as f:
    json.dump(tags, f)
