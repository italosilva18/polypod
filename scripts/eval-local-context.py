#!/usr/bin/env python3
"""Compare code retrieval variants on a fixed subset of Poly source files."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / "docs/evaluation/context-fixture.v1.json"
CORPUS = [
    "internal/acp/server.go", "internal/codesearch/search.go",
    "internal/codesearch/hybrid.go", "internal/evaluation/runner.go",
    "internal/evaluation/command.go", "internal/skill/plugin_install.go",
    "internal/skill/plugin_manager.go", "internal/commands/pr_comments_native.go",
    "internal/commands/work_overview.go", "internal/headless/headless.go",
    "internal/mcp/client.go", "internal/workdir/store.go",
    "internal/tasks/agent.go", "internal/turncheckpoint/diff.go",
    "internal/tool/codesearch.go", "internal/commands/plugin_runtime.go",
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--embedding-model", default="nomic-embed-text:latest")
    parser.add_argument("--fixture", type=Path, default=FIXTURE)
    parser.add_argument("--token-budget", type=int, default=2000)
    parser.add_argument("--corpus-from-git", help="Optional git revision (e.g. HEAD) to read corpus files from")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    fixture = args.fixture.resolve()
    if not fixture.is_file():
        parser.error(f"fixture does not exist: {fixture}")
    if not shutil.which("ollama") or not shutil.which("go"):
        parser.error("ollama and go must be installed")
    if args.embedding_model not in subprocess.run(["ollama", "list"], text=True, capture_output=True, check=True).stdout:
        parser.error("embedding model is not installed locally")
    out = (args.output or Path(tempfile.mkdtemp(prefix="polypod-context-eval-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    corpus = out / "corpus"
    corpus.mkdir(exist_ok=False)
    for name in CORPUS:
        destination = corpus / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        # A fixed prefix keeps this read-only retrieval audit fast on CPU.
        if args.corpus_from_git:
            shown = subprocess.run(
                ["git", "show", f"{args.corpus_from_git}:{name}"],
                cwd=ROOT, text=True, capture_output=True,
            )
            source_text = shown.stdout if shown.returncode == 0 else (ROOT / name).read_text()
        else:
            source_text = (ROOT / name).read_text()
        destination.write_text("".join(source_text.splitlines(keepends=True)[:200]))
    subprocess.run(["git", "init", "-q", str(corpus)], check=True)
    subprocess.run(["git", "add", "internal"], cwd=corpus, check=True)
    config_home = out / "xdg"
    config_home.mkdir()
    env = dict(os.environ, XDG_CONFIG_HOME=str(config_home))
    report = out / "retrieval.json"
    with report.open("w") as handle:
        subprocess.run(["go", "run", "./internal/codesearch/cmd", "-root", str(corpus),
            "-fixture", str(fixture), "-model", args.embedding_model,
            "-token-budget", str(args.token_budget), "-enable-index"],
            cwd=ROOT, env=env, stdout=handle, check=True)
    metadata = {
        "corpus": CORPUS, "fixture": str(fixture),
        "source_revision": subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT,
            capture_output=True, text=True, check=True).stdout.strip(),
        "embedding_model": args.embedding_model, "token_budget": args.token_budget,
        "max_lines_per_file": 200,
    }
    (out / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print(out)
    print(report.read_text().strip())


if __name__ == "__main__":
    main()
