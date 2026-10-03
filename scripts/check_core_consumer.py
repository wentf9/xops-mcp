#!/usr/bin/env python3
"""Validate the core consumer in a disposable module, never changing pins."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

UPSTREAM = "github.com/wentf9/xops-cli"


def run(root, *args, capture=False, target=None):
    environment = dict(os.environ, GOWORK="off")
    if target:
        environment.update(GOOS=target, GOARCH="amd64", CGO_ENABLED="0")
    return subprocess.run(args, cwd=root, env=environment, text=True,
                          stdout=subprocess.PIPE if capture else None,
                          stderr=subprocess.PIPE if capture else None,
                          timeout=600, check=True).stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--upstream", type=Path, help="local preview only; temporary replacement")
    source.add_argument("--version", help="published upstream version for release acceptance")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if args.upstream:
        args.upstream = args.upstream.resolve(strict=True)
        if not (args.upstream / "core/mcp/runtime").is_dir():
            parser.error("upstream checkout does not contain core/mcp/runtime")
    with tempfile.TemporaryDirectory(prefix="xops-mcp-core-consumer-") as directory:
        work = Path(directory)
        for name in ("go.mod", "go.sum", ".golangci.yml"):
            shutil.copy2(root / name, work / name)
        for path in (root / "internal/coreconsumer").glob("*.go"):
            shutil.copy2(path, work / path.name)
        if args.upstream:
            run(work, "go", "mod", "edit", f"-replace={UPSTREAM}={args.upstream}")
        else:
            run(work, "go", "mod", "edit", f"-require={UPSTREAM}@{args.version}")
        run(work, "go", "mod", "tidy")
        module = json.loads(run(work, "go", "list", "-m", "-json", UPSTREAM, capture=True))
        if args.version and (module.get("Replace") or module.get("Version") != args.version):
            raise ValueError("release check requires the exact public version without replacement")
        for target in ("linux", "windows", "darwin"):
            dependencies = run(work, "go", "list", "-deps", "-test", "./...", capture=True, target=target)
            for package in dependencies.splitlines():
                if package.startswith(UPSTREAM + "/") and not package.startswith(UPSTREAM + "/core/"):
                    raise ValueError(f"{target}: application dependency entered core consumer: {package}")
        run(work, "go", "build", "./...")
        run(work, "go", "test", "-race", "-count=1", "-timeout=120s", "./...")
        run(work, "golangci-lint", "run", "./...")
    print("Core consumer passed: " + ("local preview; publication not verified" if args.upstream else args.version))


if __name__ == "__main__":
    main()
