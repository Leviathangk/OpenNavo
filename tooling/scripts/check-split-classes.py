#!/usr/bin/env python3
"""Detect a class that a bad conflict resolution split in two.

A merge conflict whose block boundary falls *inside* a class can leave two
perfectly-named classes where the helper methods stayed with one and the
`test_` methods that call them moved to the other. The class names never
collide, so `grep '^class'` sees nothing wrong; the damage only shows up as
`AttributeError: '<SomeTests>' object has no attribute '<helper>'` at runtime --
or not at all, when the affected test is skipped, which is why this is a static
check rather than a test run.

Run it after resolving any conflict that touches a Python file.

Usage:
    python3 tooling/scripts/check-split-classes.py ops/test_e2e.py
    python3 tooling/scripts/check-split-classes.py ops/            # a directory

Exit 0 when clean, 1 when a method uses a helper its own class does not define.

Limits, deliberately: a helper supplied by a base class imported from another
file, or one assigned in `setUp`, is reported as missing. Neither shape exists
in `ops/` today (checked 2026-11-03), and reporting a false positive is cheaper
than missing a real split -- but a future suite that grows one of those shapes
has to teach this checker about it first.
"""
from __future__ import annotations

import ast
import sys
import unittest
from pathlib import Path

# Names every unittest.TestCase gives us, plus the ones a class-level attribute
# assignment can supply. Anything else a test calls as `self.x(...)` must be
# defined in that same class body.
BUILTIN = {name for name in dir(unittest.TestCase) if not name.startswith("_")}
BUILTIN |= {"addCleanup", "fail", "subTest", "skipTest"}


def class_attributes(cls: ast.ClassDef) -> set[str]:
    names: set[str] = set()
    for node in cls.body:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name):
                    names.add(target.id)
                elif isinstance(target, ast.Attribute) and isinstance(target.value, ast.Name):
                    if target.value.id == "self":
                        names.add(target.attr)
        elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
            names.add(node.target.id)
    return names


def self_calls(function: ast.AST) -> set[str]:
    """`self.x(...)` calls made by this function, not by classes nested inside it.

    A test that defines a local `class Handler(http.server.BaseHTTPRequestHandler)`
    calls `self.send_response` from inside that nested class; that receiver is the
    handler, not the test case, so walking into it would blame the wrong class.
    """
    called: set[str] = set()

    def walk(node: ast.AST) -> None:
        for child in ast.iter_child_nodes(node):
            # A nested class opens a new `self`; a nested function keeps the outer one.
            if isinstance(child, ast.ClassDef):
                continue
            if (
                isinstance(child, ast.Call)
                and isinstance(child.func, ast.Attribute)
                and isinstance(child.func.value, ast.Name)
                and child.func.value.id == "self"
            ):
                called.add(child.func.attr)
            walk(child)

    walk(function)
    return called


def is_method(node: ast.AST) -> bool:
    return isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))


def check(source: str, path: str) -> list[str]:
    try:
        tree = ast.parse(source, filename=path)
    except SyntaxError as error:
        return [f"{path}:{error.lineno}: does not parse: {error.msg}"]

    # Resolve what each class in this file provides, following the simple
    # single-file inheritance these suites use. Without this, every helper a
    # subclass inherits from a shared TestCase base looks like a split.
    classes = {
        node.name: node
        for node in ast.walk(tree)
        if isinstance(node, ast.ClassDef)
    }

    def provided(name: str, seen: frozenset[str] = frozenset()) -> set[str]:
        cls = classes.get(name)
        if cls is None or name in seen:
            return set()
        names = {node.name for node in cls.body if is_method(node)}
        names |= class_attributes(cls)
        for base in cls.bases:
            if isinstance(base, ast.Name):
                names |= provided(base.id, seen | {name})
        return names

    problems: list[str] = []
    for cls in (node for node in ast.walk(tree) if isinstance(node, ast.ClassDef)):
        defined = provided(cls.name)
        for method in cls.body:
            if not is_method(method):
                continue
            if not method.name.startswith("test_"):
                continue
            missing = self_calls(method) - defined - BUILTIN
            if missing:
                problems.append(
                    f"{path}:{method.lineno}: {cls.name}.{method.name} calls "
                    f"{', '.join(sorted('self.' + m for m in missing))}, "
                    f"which {cls.name} does not define "
                    f"(a conflict may have split this class in two)"
                )
    return problems


def targets(argv: list[str]) -> tuple[list[Path], list[str]]:
    found: list[Path] = []
    problems: list[str] = []
    for arg in argv:
        path = Path(arg)
        if path.is_dir():
            found.extend(sorted(path.rglob("*.py")))
        elif path.is_file():
            found.append(path)
        else:
            problems.append(f"{arg}: not found")
    return found, problems


def main(argv: list[str]) -> int:
    if not argv:
        print(__doc__.strip().splitlines()[0], file=sys.stderr)
        return 1
    found, problems = targets(argv)
    # A path that does not exist is an error, not a clean run: silently passing
    # on a typo is how a checker ends up protecting nothing.
    for path in found:
        try:
            source = path.read_text(encoding="utf-8")
        except OSError as error:
            problems.append(f"{path}: {error}")
            continue
        problems.extend(check(source, str(path)))
    for problem in problems:
        print(problem)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
