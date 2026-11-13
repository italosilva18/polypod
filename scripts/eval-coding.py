#!/usr/bin/env python3
"""Run one externally verified coding smoke test with Ollama or a free NVIDIA endpoint."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]


def run(*argv, cwd=ROOT):
    subprocess.run(argv, cwd=cwd, check=True)


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--provider", choices=["ollama", "nvidia"], default="ollama")
    parser.add_argument("--model", help="Provider model ID")
    parser.add_argument("--timeout", type=int, default=240)
    parser.add_argument("--max-tool-iterations", type=int, default=4)
    parser.add_argument("--max-steps", type=int, default=16, help="External budget for individual tool calls")
    parser.add_argument("--output", type=Path, help="Directory to keep inputs, results and report")
    args = parser.parse_args()
    if args.model is None:
        args.model = "qwen2.5-coder:1.5b" if args.provider == "ollama" else "nvidia/nemotron-3.5-super-vl-120b-a12b"
    if args.timeout < 30:
        parser.error("--timeout must be at least 30 seconds")
    if args.max_tool_iterations < 1 or args.max_tool_iterations > 20:
        parser.error("--max-tool-iterations must be between 1 and 20")
    if args.max_steps < 1:
        parser.error("--max-steps must be positive")
    if not shutil.which("go"):
        parser.error("go must be installed")
    if args.provider == "ollama":
        if not shutil.which("ollama"):
            parser.error("ollama must be installed")
        installed = subprocess.run(["ollama", "list"], text=True, capture_output=True, check=True).stdout
        if args.model not in installed:
            parser.error(f"model {args.model!r} is not installed locally; download it separately")
    else:
        # The Poly subprocess loads the existing private .env from this directory.
        # Neither the key nor its value is copied into the evaluation artifacts.
        os.environ["POLYPOD_ENV_DIR"] = str(ROOT)

    out = args.output or Path(tempfile.mkdtemp(prefix=f"polypod-{args.provider}-coding-eval-"))
    out = out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    workspace = out / "workspace"
    workspace.mkdir(exist_ok=False)
    data_dir = out / "data"
    data_dir.mkdir()
    (workspace / "go.mod").write_text("module example.com/localfixture\n\ngo 1.26\n")
    (workspace / "math.go").write_text("package localfixture\n\nfunc Sum(xs []int) int {\n\ttotal := 0\n\tfor _, n := range xs {\n\t\tif n > 0 { total += n }\n\t}\n\treturn total\n}\n")
    (workspace / "math_test.go").write_text("package localfixture\n\nimport \"testing\"\n\nfunc TestSumIncludesNegative(t *testing.T) {\n\tif got := Sum([]int{3, -7, 2}); got != -2 { t.Fatalf(\"Sum = %d, want -2\", got) }\n}\n")
    run("git", "init", "-q", str(workspace))
    run("git", "add", "go.mod", "math.go", "math_test.go", cwd=workspace)
    config = out / "config.yaml"
    max_tokens = 256 if args.provider == "ollama" else 2048
    api_key_line = "  api_key: ${NVIDIA_API_KEY:}\n" if args.provider == "nvidia" else ""
    config.write_text(
        "ai:\n"
        f"  provider: {args.provider}\n"
        f"  model: {args.model}\n"
        f"  base_url: {'http://127.0.0.1:11434' if args.provider == 'ollama' else 'https://integrate.api.nvidia.com/v1'}\n"
        f"{api_key_line}"
        f"  max_tokens: {max_tokens}\n"
        f"  max_tool_iterations: {args.max_tool_iterations}\n"
        "  tools: true\n"
        "cli:\n  enabled: true\n"
        f"data:\n  dir: {data_dir}\n"
        "database:\n  enabled: false\n"
        "permissions:\n  defaultMode: bypassPermissions\n  mode: bypassPermissions\n"
        "sandbox:\n  enabled: false\n"
    )
    binary = out / "polypod"
    run("go", "build", "-o", str(binary), ".")
    binary_digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    prompt = "Corrija a função Sum em math.go para somar todos os inteiros, inclusive negativos. Execute o teste Go e responda em uma frase."
    write_json(out / "manifest.json", {
        "schema_version": 1, "id": "poly-local-coding-smoke", "version": "1",
        "tasks": [{"id": "sum-negative", "category": "coding", "criterion": "go test ./... passa após editar a função", "benchmark": "fixture-local-v1", "recovery": False, "prompt": prompt, "timeout_seconds": args.timeout}],
    })
    revision = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip()
    write_json(out / "metadata.json", {
        "run_id": f"local-{int(time.time())}", "agent": "polypod", "agent_version": f"binary-sha256:{binary_digest}",
        "model": args.model, "model_settings": f"max_tokens={max_tokens},max_tool_iterations={args.max_tool_iterations},tools=true",
        "hardware": platform.processor() or platform.machine(),
        "environment": f"{platform.system()} {platform.release()}, {args.provider}, Python {platform.python_version()}, git-head={revision}",
        "dataset_revision": "fixture-local-v1", "evaluator_version": "go-test-local-v1", "seed": 0,
        "budget": {"max_steps": args.max_steps, "max_seconds": args.timeout, "max_usd": 0.01}, "synthetic": True,
    })
    write_json(out / "plan.json", {"cases": [{
        "task_id": "sum-negative", "working_dir": str(workspace),
        "command": [str(binary), str(config), "-p", "{prompt}", "--output-format", "json"],
        "verify_command": [shutil.which("go"), "test", "./..."],
    }]})
    run("go", "run", "./internal/evaluation/runcli", "-manifest", str(out / "manifest.json"),
        "-metadata", str(out / "metadata.json"), "-plan", str(out / "plan.json"),
        "-output", str(out / "results.json"))
    with (out / "report.json").open("w") as report:
        subprocess.run(["go", "run", "./internal/evaluation/cmd", "-manifest", str(out / "manifest.json"),
            "-results", str(out / "results.json"), "-format", "json"], cwd=ROOT, stdout=report, check=True)
    print(out)
    summary = json.loads((out / "report.json").read_text())
    print(json.dumps({"overall": summary["overall"], "latency_ms": summary["latency_ms"],
                      "tokens": summary["tokens"], "tool_failures": summary["tool_failures"],
                      "cost_usd": summary["cost_usd"], "outcomes": summary["outcomes"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
