"""Merge repeated Go coverprofile blocks without changing their coverage."""

from pathlib import Path
import re
import sys


BLOCK = re.compile(r"(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)")


def normalize(source, destination):
    lines = Path(source).read_text().splitlines()
    if not lines or lines[0] not in ('mode: set', 'mode: count', 'mode: atomic'):
        raise ValueError('Expected a Go coverage mode header')
    blocks = {}
    for line in lines[1:]:
        match = BLOCK.fullmatch(line)
        if match is None:
            raise ValueError(f'Invalid coverage block: {line!r}')
        name, *numbers = match.groups()
        start_line, start_col, end_line, end_col, statements, count = map(int, numbers)
        key = (name, start_line, start_col, end_line, end_col)
        if key in blocks:
            previous_statements, previous_count = blocks[key]
            if statements != previous_statements:
                raise ValueError(f'Conflicting statement counts for {name}')
            # Match go tool cover: set counters are ORed; other modes are summed.
            count = count | previous_count if lines[0] == 'mode: set' else count + previous_count
        blocks[key] = (statements, count)
    if not blocks:
        raise ValueError('Coverage report contains no blocks')
    result = [lines[0]]
    for (name, start_line, start_col, end_line, end_col), (statements, count) in sorted(blocks.items()):
        result.append(f'{name}:{start_line}.{start_col},{end_line}.{end_col} {statements} {count}')
    Path(destination).write_text('\n'.join(result) + '\n')


if __name__ == '__main__':
    if len(sys.argv) != 3:
        sys.exit('usage: normalize_go_coverage.py INPUT OUTPUT')
    normalize(sys.argv[1], sys.argv[2])
