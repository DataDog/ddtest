#!/usr/bin/env python
import json
import platform
import sys

tags = {
    "runtime.name": platform.python_implementation(),
    "runtime.version": platform.python_version(),
    "os.platform": platform.system(),
    "os.architecture": platform.machine(),
    "os.version": platform.release(),
}

# Write to file path passed as argument
with open(sys.argv[1], 'w') as f:
    json.dump(tags, f)
