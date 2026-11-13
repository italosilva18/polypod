#!/usr/bin/env python3
"""Validate Phase 5 (/pr_comments + verifier-gated /autofix-pr) on a real GitHub PR."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_REPO = "https://github.com/github/gh-aw.git"
DEFAULT_PR_URL = "https://github.com/github/gh-aw/pull/77334"
DEFAULT_SHA = "975779adacb94c31efd9a073c631b3e7d3fd4c9a"
DEFAULT_VERIFIER = "go test -count=1 ./pkg/workflow -run TestDefaultModelRoutingActivationHonorsModelVariables"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace", type=Path, help="Existing or target PR checkout directory")
    parser.add_argument("--pr-url", default=DEFAULT_PR_URL)
    parser.add_argument("--repo-url", default=DEFAULT_REPO)
    parser.add_argument("--head-sha", default=DEFAULT_SHA)
    parser.add_argument("--verifier", default=DEFAULT_VERIFIER)
    parser.add_argument("--model", default="nvidia/nemotron-3.5-super-vl-120b-a12b")
    parser.add_argument("--result", type=Path, required=True)
    args = parser.parse_args()

    env = dict(os.environ)
    env["PATH"] = f"/usr/local/go/bin:/home/costa/go/bin:/home/costa/.local/bin:{env.get('PATH', '')}"
    env["POLYPOD_ENV_DIR"] = str(ROOT)

    tmp_root = Path(tempfile.mkdtemp(prefix="polypod-phase5-run-"))
    home_dir = tmp_root / "home"
    home_dir.mkdir(parents=True, exist_ok=True)
    orig_home = Path(os.environ.get("HOME", "/root"))
    env["HOME"] = str(home_dir)
    env.setdefault("GOPATH", str(orig_home / "go"))
    env.setdefault("GOMODCACHE", str(orig_home / "go" / "pkg" / "mod"))
    env.setdefault("GOCACHE", str(orig_home / ".cache" / "go-build"))
    binary = tmp_root / "polypod"
    subprocess.run(["go", "build", "-o", str(binary), "."], cwd=ROOT, env=env, check=True)

    ws = (args.workspace or (tmp_root / "pr-workspace")).resolve()
    if not (ws / ".git").exists():
        ws.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "init", "-q"], cwd=ws, env=env, check=True)
        subprocess.run(["git", "remote", "add", "origin", args.repo_url], cwd=ws, env=env, check=True)
        subprocess.run(
            ["git", "fetch", "--depth", "1", "--filter=blob:none", "origin", args.head_sha],
            cwd=ws, env=env, check=True,
        )
    subprocess.run(["git", "checkout", "-fq", args.head_sha], cwd=ws, env=env, check=True)
    subprocess.run(["git", "clean", "-fdq"], cwd=ws, env=env, check=True)

    pre_test = subprocess.run(["sh", "-c", args.verifier], cwd=ws, env=env, text=True, capture_output=True)

    data_dir = tmp_root / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    cfg_path = tmp_root / "config.yaml"
    cfg_path.write_text(
        "ai:\n"
        "  provider: nvidia\n"
        f"  model: {args.model}\n"
        "  base_url: https://integrate.api.nvidia.com/v1\n"
        "  api_key: ${NVIDIA_API_KEY:}\n"
        "  max_tokens: 8192\n"
        "  temperature: 0.2\n"
        "  max_tool_iterations: 36\n"
        "  tools: true\n"
        "cli:\n  enabled: true\n"
        f"data:\n  dir: {data_dir}\n  agents_dir: {ROOT / 'agents'}\n"
        f"database:\n  enabled: true\n  driver: sqlite\n  path: {data_dir / 'polypod.db'}\n"
        "scheduler:\n  enabled: false\n  heartbeat_enabled: false\n"
        f"plugins:\n  dir: {data_dir / 'plugins'}\n"
        f"templates:\n  dir: {ROOT / 'templates'}\n"
        "permissions:\n  defaultMode: bypassPermissions\n  mode: bypassPermissions\n"
        "sandbox:\n  enabled: false\n"
    )

    def run_poly(cmd):
        proc = subprocess.run(cmd, cwd=ws, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            log_file = data_dir / "polypod.log"
            log_tail = log_file.read_text()[-2000:] if log_file.exists() else ""
            raise RuntimeError(
                f"Command failed ({proc.returncode}): {cmd}\n"
                f"STDOUT:\n{proc.stdout[-1500:]}\n"
                f"STDERR:\n{proc.stderr[-1500:]}\n"
                f"LOG:\n{log_tail}"
            )
        return proc

    pr_comments_run = run_poly(
        [str(binary), str(cfg_path), "-p", f"/pr_comments {args.pr_url}", "--output-format", "json"],
    )
    pr_comments_json = json.loads(pr_comments_run.stdout)

    polypod_dir = ws / ".polypod"
    polypod_dir.mkdir(exist_ok=True)
    (polypod_dir / "verifiers.yaml").write_text(
        "verifiers:\n"
        f"  pr_regression: {json.dumps(args.verifier)}\n"
    )

    autofix_run = run_poly(
        [str(binary), str(cfg_path), "-p", f"/autofix-pr {args.pr_url}", "--output-format", "json"],
    )
    autofix_json = json.loads(autofix_run.stdout)

    post_test = subprocess.run(["sh", "-c", args.verifier], cwd=ws, env=env, text=True, capture_output=True)
    diff_out = subprocess.run(["git", "diff"], cwd=ws, env=env, text=True, capture_output=True, check=True).stdout

    summary = {
        "pr_url": args.pr_url,
        "head_sha": args.head_sha,
        "model": args.model,
        "pre_fix_verifier_exit_code": pre_test.returncode,
        "pr_comments_mapped_in_diff": "[no diff atual]" in pr_comments_json.get("content", ""),
        "pr_comments_excerpt": pr_comments_json.get("content", "")[:1200],
        "autofix_verified_banner": "CORREÇÃO VERIFICADA" in autofix_json.get("content", ""),
        "autofix_steps": autofix_json.get("steps"),
        "autofix_tokens": autofix_json.get("tokens"),
        "autofix_duration_ms": autofix_json.get("duration_ms"),
        "autofix_cost_measured": autofix_json.get("cost_measured"),
        "autofix_content": autofix_json.get("content", ""),
        "post_fix_verifier_exit_code": post_test.returncode,
        "post_fix_verifier_output": (post_test.stdout + post_test.stderr)[-1200:],
        "git_diff": diff_out,
    }
    args.result.parent.mkdir(parents=True, exist_ok=True)
    args.result.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    shutil.rmtree(tmp_root, ignore_errors=True)
    print(json.dumps({
        "pr_url": summary["pr_url"],
        "pre_fix_verifier_exit_code": summary["pre_fix_verifier_exit_code"],
        "pr_comments_mapped_in_diff": summary["pr_comments_mapped_in_diff"],
        "autofix_verified_banner": summary["autofix_verified_banner"],
        "post_fix_verifier_exit_code": summary["post_fix_verifier_exit_code"],
        "autofix_steps": summary["autofix_steps"],
        "autofix_tokens": summary["autofix_tokens"],
        "autofix_duration_ms": summary["autofix_duration_ms"],
    }, ensure_ascii=False, indent=2))
    if not (
        summary["pre_fix_verifier_exit_code"] != 0
        and summary["pr_comments_mapped_in_diff"]
        and summary["autofix_verified_banner"]
        and summary["post_fix_verifier_exit_code"] == 0
    ):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
