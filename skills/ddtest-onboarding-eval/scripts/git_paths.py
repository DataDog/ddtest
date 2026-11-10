"""Decode Git C-style paths consistently during retention and review."""
import os
import re

# Git's C-style path quoting (quote_c_style): these escapes plus \ooo octal bytes.
GIT_ESCAPES = {"a": 7, "b": 8, "t": 9, "n": 10, "v": 11, "f": 12, "r": 13, '"': 34, "\\": 92}


def unquote_path(text):
    """Decode a path as git status prints it: verbatim, or quoted with escapes."""
    if not (len(text) >= 2 and text[0] == text[-1] == '"'):
        return text
    body, decoded, index = text[1:-1], bytearray(), 0
    while index < len(body):
        character = body[index]
        if character != "\\":
            decoded += character.encode()
            index += 1
        elif body[index + 1:index + 2] in GIT_ESCAPES:
            decoded.append(GIT_ESCAPES[body[index + 1]])
            index += 2
        elif re.fullmatch(r"[0-3][0-7]{2}", body[index + 1:index + 4]):
            decoded.append(int(body[index + 1:index + 4], 8))
            index += 4
        else:
            raise ValueError(f"unknown escape in {text}")
    return os.fsdecode(bytes(decoded))


def untracked_paths(status):
    """(as printed, decoded) for each untracked entry of git status --short."""
    entries = []
    for line in status.splitlines():
        if line.startswith("?? "):
            try:
                path = unquote_path(line[3:])
            except ValueError:
                path = None
            entries.append((line[3:], path))
    return entries
