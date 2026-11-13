#!/usr/bin/env python3
"""Run the frozen Polypod coding suite across local/free CLIs with held-out external verifiers."""

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
DEFAULT_MANIFEST = ROOT / "docs/evaluation/coding-suite.v1.json"
DEFAULT_CASES = ROOT / "docs/evaluation/coding-suite-cases.v1.json"
DEFAULT_NVIDIA_MODEL = "nvidia/nemotron-3.5-super-vl-120b-a12b"
DEFAULT_OLLAMA_MODEL = "qwen2.5-coder:7b"


def build_search_path() -> str:
    parts = [
        "/usr/local/go/bin",
        "/home/costa/go/bin",
        "/home/costa/.local/bin",
        "/home/costa/.npm-global/bin",
        "/home/costa/.opencode/bin",
        "/usr/local/bin",
        "/usr/bin",
        "/bin",
    ]
    for nvm_base in ("/home/costa/.nvm/versions/node", "/root/.nvm/versions/node"):
        base = Path(nvm_base)
        if base.is_dir():
            for child in sorted(base.iterdir()):
                bin_dir = child / "bin"
                if bin_dir.is_dir():
                    parts.insert(0, str(bin_dir))
    existing = os.environ.get("PATH", "")
    if existing:
        parts.append(existing)
    return ":".join(parts)


def load_dotenv_key(dotenv_path: Path, key: str) -> str:
    val = os.environ.get(key, "").strip()
    if val:
        return val
    if not dotenv_path.is_file():
        return ""
    for raw in dotenv_path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        if k.strip() == key:
            return v.strip().strip('"').strip("'")
    return ""


def cli_version(binary: str | None, env: dict[str, str]) -> str:
    if not binary:
        return "missing"
    try:
        proc = subprocess.run([binary, "--version"], env=env, text=True, capture_output=True, timeout=8)
        out = (proc.stdout + proc.stderr).strip()
        return out.splitlines()[0].strip() if out else f"rc={proc.returncode}"
    except Exception as exc:
        return f"error:{type(exc).__name__}"


ADAPTER_SCRIPT = r'''#!/usr/bin/env python3
"""Adapter translating headless CLI runs into Polypod CommandRunner JSON."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys


def parse_qwen_output(stdout: str) -> dict:
    content = ""
    steps = 1
    failures = 0
    total_tokens = 0
    text = stdout.strip()
    parsed = None
    if text:
        try:
            parsed = json.loads(text)
        except Exception:
            lines = [ln for ln in text.splitlines() if ln.strip().startswith("{")]
            objs = []
            for ln in lines:
                try:
                    objs.append(json.loads(ln))
                except Exception:
                    pass
            parsed = objs if objs else None

    if isinstance(parsed, dict):
        content = str(parsed.get("result") or parsed.get("content") or parsed.get("response") or "")
        if isinstance(parsed.get("num_turns"), int):
            steps = max(1, parsed["num_turns"])
        stats = parsed.get("stats") or parsed.get("usage") or {}
        if isinstance(stats, dict):
            models = stats.get("models")
            if isinstance(models, dict):
                for m in models.values():
                    tok = (m or {}).get("tokens") or {}
                    total_tokens += int(tok.get("total") or (int(tok.get("prompt", 0)) + int(tok.get("candidates", 0))))
            if total_tokens == 0:
                total_tokens = int(
                    stats.get("total_tokens")
                    or stats.get("totalTokens")
                    or (int(stats.get("input_tokens", 0)) + int(stats.get("output_tokens", 0)))
                )
            tools = stats.get("tools")
            if isinstance(tools, dict):
                calls = int(tools.get("totalCalls", 0) or 0)
                if calls > 0:
                    steps = calls
                failures = int(tools.get("totalFail", 0) or 0)
    elif isinstance(parsed, list):
        tool_uses = 0
        for item in parsed:
            if not isinstance(item, dict):
                continue
            typ = item.get("type", "")
            if typ == "result":
                content = str(item.get("result") or content)
                if isinstance(item.get("num_turns"), int):
                    steps = max(steps, item["num_turns"])
                usage = item.get("usage") or item.get("stats") or {}
                if isinstance(usage, dict):
                    total_tokens = max(
                        total_tokens,
                        int(usage.get("total_tokens") or (int(usage.get("input_tokens", 0)) + int(usage.get("output_tokens", 0)))),
                    )
            elif typ == "assistant":
                msg = item.get("message") or {}
                for part in (msg.get("content") or []):
                    if isinstance(part, dict):
                        if part.get("type") == "tool_use":
                            tool_uses += 1
                        elif part.get("type") == "text":
                            content = str(part.get("text") or content)
                usage = msg.get("usage") or {}
                if isinstance(usage, dict):
                    total_tokens += int(usage.get("input_tokens", 0)) + int(usage.get("output_tokens", 0))
            elif typ == "user":
                msg = item.get("message") or {}
                for part in (msg.get("content") or []):
                    if isinstance(part, dict) and part.get("type") == "tool_result" and part.get("is_error"):
                        failures += 1
        if tool_uses > 0:
            steps = max(steps, tool_uses)
    if not content:
        content = text[-1000:] if text else "completed"
    return {
        "content": content,
        "cost_measured": False,
        "cost_usd": 0.0,
        "steps": steps,
        "tool_failures": failures,
        "tokens": {"total": total_tokens},
    }


def parse_opencode_output(stdout: str) -> dict:
    content_parts = []
    steps = 0
    failures = 0
    total_tokens = 0
    for raw in stdout.splitlines():
        line = raw.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except Exception:
            continue
        typ = ev.get("type", "")
        part = ev.get("part") or {}
        if typ == "text":
            txt = part.get("text") or ev.get("text") or ""
            if txt:
                content_parts.append(str(txt))
        elif typ in ("tool_use", "tool"):
            steps += 1
            state = part.get("state") or {}
            if isinstance(state, dict) and state.get("status") == "error":
                failures += 1
        elif typ == "step_finish":
            steps = max(steps, 1)
            toks = part.get("tokens") or ev.get("tokens") or {}
            if isinstance(toks, dict):
                total_tokens += int(toks.get("input", 0)) + int(toks.get("output", 0)) + int(toks.get("reasoning", 0))
    return {
        "content": "\n".join(content_parts).strip() or "completed",
        "cost_measured": False,
        "cost_usd": 0.0,
        "steps": max(1, steps),
        "tool_failures": failures,
        "tokens": {"total": total_tokens},
    }


def parse_aider_output(stdout: str) -> dict:
    total_tokens = 0
    # Example Aider summary: "Tokens: 1.2k sent, 340 received."
    for m in re.finditer(r"Tokens:\s*([0-9.]+)(k?)\s*sent,\s*([0-9.]+)(k?)\s*received", stdout, re.IGNORECASE):
        sent = float(m.group(1)) * (1000.0 if m.group(2).lower() == "k" else 1.0)
        recv = float(m.group(3)) * (1000.0 if m.group(4).lower() == "k" else 1.0)
        total_tokens += int(round(sent + recv))
    steps = max(1, len(re.findall(r"Applied edit to", stdout)))
    failures = len(re.findall(r"Failed to apply|SearchReplaceNoExactMatch", stdout))
    return {
        "content": stdout[-1200:].strip() or "completed",
        "cost_measured": False,
        "cost_usd": 0.0,
        "steps": steps,
        "tool_failures": failures,
        "tokens": {"total": total_tokens},
    }


def parse_codex_output(stdout: str, cost_measured: bool) -> dict:
    content = ""
    steps = 0
    failures = 0
    total_tokens = 0
    for raw in stdout.splitlines():
        line = raw.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except Exception:
            continue
        typ = ev.get("type", "")
        if typ == "item.completed":
            item = ev.get("item") or {}
            itype = item.get("type", "")
            if itype == "agent_message":
                content = str(item.get("text") or content)
            elif itype in ("command_execution", "file_change", "mcp_tool_call"):
                steps += 1
                if item.get("status") == "failed" or (isinstance(item.get("exit_code"), int) and item["exit_code"] != 0):
                    failures += 1
        elif typ == "turn.completed":
            usage = ev.get("usage") or {}
            total_tokens += int(usage.get("input_tokens", 0)) + int(usage.get("output_tokens", 0))
    return {
        "content": content or "completed",
        "cost_measured": cost_measured,
        "cost_usd": 0.0,
        "steps": max(1, steps),
        "tool_failures": failures,
        "tokens": {"total": total_tokens},
    }


def main():
    mode = sys.argv[1]
    spec_path = Path(sys.argv[2])
    prompt = sys.argv[3]
    spec = json.loads(spec_path.read_text())

    env = dict(os.environ)
    env.update(spec.get("env", {}))
    secret = ""
    key_file = spec.get("nvidia_key_file", "")
    if key_file and Path(key_file).is_file():
        secret = Path(key_file).read_text().strip()
        for var_name in spec.get("secret_env_vars", []):
            env[var_name] = secret

    def fail_proc(proc: subprocess.CompletedProcess):
        err_txt = (proc.stderr or proc.stdout or "")[-400:]
        if secret:
            err_txt = err_txt.replace(secret, "[REDACTED]")
        if err_txt:
            sys.stderr.write(err_txt + "\n")
        sys.exit(proc.returncode or 1)

    cwd = Path.cwd()
    if mode == "poly":
        cmd = [spec["binary"], spec["config_path"], "-p", prompt, "--output-format", "json"]
        proc = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            fail_proc(proc)
        # Emit Polypod JSON directly
        data = json.loads(proc.stdout)
        sys.stdout.write(json.dumps({
            "content": data.get("content", ""),
            "cost_measured": bool(data.get("cost_measured", False)),
            "cost_usd": float(data.get("cost_usd", 0.0)),
            "steps": int(data.get("steps", 1)),
            "tool_failures": int(data.get("tool_failures", 0)),
            "tokens": {"total": int((data.get("tokens") or {}).get("total", 0))},
        }))
        return

    if mode == "qwen":
        cmd = [
            spec["binary"],
            "--bare",
            "-y",
            "--chat-recording=false",
            "--auth-type", "openai",
            "--openai-base-url", spec["base_url"],
            "-m", spec["model"],
            "--max-tool-calls", str(spec.get("max_steps", 16)),
            "--output-format", "json",
            prompt,
        ]
        proc = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            fail_proc(proc)
        sys.stdout.write(json.dumps(parse_qwen_output(proc.stdout)))
        return

    if mode == "opencode":
        cmd = [
            spec["binary"],
            "run",
            "--pure",
            "--dangerously-skip-permissions",
            "--format", "json",
            "-m", spec["model"],
            prompt,
        ]
        proc = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            fail_proc(proc)
        sys.stdout.write(json.dumps(parse_opencode_output(proc.stdout)))
        return

    if mode == "aider":
        go_files = sorted(str(p.name) for p in cwd.glob("*.go"))
        cmd = [
            spec["binary"],
            "--yes-always",
            "--no-auto-commits",
            "--no-suggest-shell-commands",
            "--no-check-update",
            "--no-show-model-warnings",
            "--no-pretty",
            "--no-analytics",
            "--model", f"openai/{spec['model']}",
            "--message", prompt,
            *go_files,
        ]
        proc = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            # Retry without --no-analytics in case older/newer flag differs
            if "unrecognized arguments: --no-analytics" in proc.stderr:
                cmd = [x for x in cmd if x != "--no-analytics"]
                proc = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            fail_proc(proc)
        sys.stdout.write(json.dumps(parse_aider_output(proc.stdout + "\n" + proc.stderr)))
        return

    if mode == "codex":
        cmd = [*spec["cmd_prefix"], prompt]
        proc = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
        if proc.returncode != 0:
            fail_proc(proc)
        sys.stdout.write(json.dumps(parse_codex_output(proc.stdout, bool(spec.get("cost_measured", False)))))
        return

    raise SystemExit(f"unknown mode: {mode}")


if __name__ == "__main__":
    main()
'''


def write_poly_config(cfg_path: Path, data_dir: Path, provider: str, model: str, base_url: str, api_key_expr: str):
    data_dir.mkdir(parents=True, exist_ok=True)
    cfg_path.write_text(
        "ai:\n"
        f"  provider: {provider}\n"
        f"  model: {model}\n"
        f"  base_url: {base_url}\n"
        f"  api_key: {json.dumps(api_key_expr)}\n"
        "  max_tokens: 8192\n"
        "  temperature: 0.2\n"
        "  max_tool_iterations: 16\n"
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


def prepare_case_workspaces(cases_doc: dict, run_root: Path, heldout_root: Path, go_bin: str, base_env: dict[str, str]):
    ws_root = run_root / "workspaces"
    verifiers_root = run_root / "verifiers"
    ws_root.mkdir(parents=True, exist_ok=True)
    verifiers_root.mkdir(parents=True, exist_ok=True)
    plan_cases = []

    for case in cases_doc["cases"]:
        task_id = case["task_id"]
        ws = ws_root / task_id
        ws.mkdir(parents=True, exist_ok=True)
        for rel_path, content in case["workspace_files"].items():
            dst = ws / rel_path
            dst.parent.mkdir(parents=True, exist_ok=True)
            dst.write_text(content)

        subprocess.run(["git", "init", "-q"], cwd=ws, env=base_env, check=True)
        subprocess.run(["git", "config", "user.email", "eval@polypod.local"], cwd=ws, env=base_env, check=True)
        subprocess.run(["git", "config", "user.name", "Polypod Eval"], cwd=ws, env=base_env, check=True)
        subprocess.run(["git", "add", "."], cwd=ws, env=base_env, check=True)
        subprocess.run(["git", "commit", "-qm", "initial buggy fixture"], cwd=ws, env=base_env, check=True)

        task_heldout = heldout_root / task_id
        task_heldout.mkdir(parents=True, exist_ok=True)
        gomod_backup = task_heldout / "go.mod.orig"
        gomod_backup.write_text(case["workspace_files"]["go.mod"])
        copy_lines = [f'cp "{gomod_backup}" "$PWD/go.mod"']
        for rel_path, content in case["external_verifier_files"].items():
            src = task_heldout / rel_path
            src.parent.mkdir(parents=True, exist_ok=True)
            src.write_text(content)
            copy_lines.append(f'cp "{src}" "$PWD/{rel_path}"')

        verify_script = verifiers_root / f"verify-{task_id}.sh"
        verify_script.write_text(
            "#!/bin/sh\n"
            "set -eu\n"
            + "\n".join(copy_lines)
            + f'\nexec "{go_bin}" test -count=1 ./...\n'
        )
        verify_script.chmod(0o700)
        plan_cases.append({
            "task_id": task_id,
            "working_dir": str(ws),
            "verify_command": ["/bin/sh", str(verify_script)],
        })
    return plan_cases


def preflight_adapter(adapter_py: Path, mode: str, spec_path: Path, base_env: dict[str, str], redact_secret: str = "", timeout_s: int = 40) -> tuple[bool, str]:
    probe_dir = Path(tempfile.mkdtemp(prefix=f"eval-preflight-{mode}-"))
    try:
        (probe_dir / "hello.go").write_text("package hello\n")
        subprocess.run(["git", "init", "-q"], cwd=probe_dir, env=base_env, check=True)
        subprocess.run(["git", "config", "user.email", "eval@polypod.local"], cwd=probe_dir, env=base_env, check=True)
        subprocess.run(["git", "config", "user.name", "Polypod Eval"], cwd=probe_dir, env=base_env, check=True)
        subprocess.run(["git", "add", "."], cwd=probe_dir, env=base_env, check=True)
        subprocess.run(["git", "commit", "-qm", "probe"], cwd=probe_dir, env=base_env, check=True)
        proc = subprocess.run(
            ["python3", str(adapter_py), mode, str(spec_path), "Responda apenas OK sem alterar arquivos."],
            cwd=probe_dir,
            env=base_env,
            text=True,
            capture_output=True,
            timeout=timeout_s,
        )
        if proc.returncode != 0:
            raw_err = proc.stderr or proc.stdout
            if redact_secret:
                raw_err = raw_err.replace(redact_secret, "[REDACTED]")
            reason = raw_err.strip().splitlines()
            tail = reason[-1][:180] if reason else f"exit_code={proc.returncode}"
            return False, f"preflight_failed:{tail}"
        json.loads(proc.stdout)
        return True, "ready"
    except Exception as exc:
        return False, f"preflight_exception:{type(exc).__name__}"
    finally:
        shutil.rmtree(probe_dir, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--cases", type=Path, default=DEFAULT_CASES)
    parser.add_argument("--nvidia-model", default=DEFAULT_NVIDIA_MODEL)
    parser.add_argument("--ollama-model", default="qwen2.5-coder:1.5b")
    parser.add_argument("--include-ollama", action="store_true", help="Also run CPU-local Ollama contenders")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()

    search_path = build_search_path()
    orig_home = Path(os.environ.get("HOME", "/root"))
    base_env = dict(os.environ)
    base_env["PATH"] = search_path
    base_env.setdefault("GOPATH", str(orig_home / "go"))
    base_env.setdefault("GOMODCACHE", str(orig_home / "go" / "pkg" / "mod"))
    base_env.setdefault("GOCACHE", str(orig_home / ".cache" / "go-build"))

    go_bin = shutil.which("go", path=search_path)
    if not go_bin:
        raise SystemExit("go toolchain not found")

    manifest_bytes = args.manifest.read_bytes()
    cases_bytes = args.cases.read_bytes()
    manifest_sha256 = hashlib.sha256(manifest_bytes).hexdigest()
    cases_sha256 = hashlib.sha256(cases_bytes).hexdigest()
    cases_doc = json.loads(cases_bytes)

    git_rev = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=ROOT, env=base_env, text=True, capture_output=True, check=True
    ).stdout.strip()

    nvidia_key = load_dotenv_key(ROOT / ".env", "NVIDIA_API_KEY")
    if not nvidia_key:
        raise SystemExit("NVIDIA_API_KEY not available in environment or .env")

    tmp_root = Path(tempfile.mkdtemp(prefix="polypod-phase1-suite-"))
    try:
        bin_dir = tmp_root / "bin"
        bin_dir.mkdir(parents=True, exist_ok=True)
        poly_bin = bin_dir / "polypod"
        eval_run_bin = bin_dir / "polypod-eval-run"
        eval_report_bin = bin_dir / "polypod-eval"

        subprocess.run([go_bin, "build", "-o", str(poly_bin), "."], cwd=ROOT, env=base_env, check=True)
        subprocess.run([go_bin, "build", "-o", str(eval_run_bin), "./internal/evaluation/runcli"], cwd=ROOT, env=base_env, check=True)
        subprocess.run([go_bin, "build", "-o", str(eval_report_bin), "./internal/evaluation/cmd"], cwd=ROOT, env=base_env, check=True)

        key_file = tmp_root / "nvidia.key"
        key_file.write_text(nvidia_key)
        key_file.chmod(0o600)

        adapter_py = tmp_root / "cli_adapter.py"
        adapter_py.write_text(ADAPTER_SCRIPT)
        adapter_py.chmod(0o700)

        poly_ver = cli_version(str(poly_bin), base_env)
        qwen_bin = shutil.which("qwen", path=search_path)
        aider_bin = shutil.which("aider", path=search_path)
        opencode_bin = shutil.which("opencode", path=search_path)
        codex_bin = shutil.which("codex", path=search_path)
        claude_bin = shutil.which("claude", path=search_path)
        gemini_bin = shutil.which("gemini", path=search_path)
        kilo_bin = shutil.which("kilo", path=search_path)
        ollama_bin = shutil.which("ollama", path=search_path)

        cli_inventory = {
            "polypod": {"binary": str(poly_bin), "version": poly_ver},
            "qwen": {"binary": qwen_bin, "version": cli_version(qwen_bin, base_env)},
            "aider": {"binary": aider_bin, "version": cli_version(aider_bin, base_env)},
            "opencode": {"binary": opencode_bin, "version": cli_version(opencode_bin, base_env)},
            "codex": {"binary": codex_bin, "version": cli_version(codex_bin, base_env)},
            "claude": {"binary": claude_bin, "version": cli_version(claude_bin, base_env)},
            "gemini": {"binary": gemini_bin, "version": cli_version(gemini_bin, base_env)},
            "kilo": {"binary": kilo_bin, "version": cli_version(kilo_bin, base_env)},
            "ollama": {"binary": ollama_bin, "version": cli_version(ollama_bin, base_env)},
        }

        contenders = []

        # 1. Poly on free NVIDIA endpoint
        poly_nv_home = tmp_root / "homes/poly-nvidia"
        poly_nv_home.mkdir(parents=True, exist_ok=True)
        poly_nv_cfg = tmp_root / "configs/poly-nvidia.yaml"
        poly_nv_cfg.parent.mkdir(parents=True, exist_ok=True)
        write_poly_config(
            poly_nv_cfg,
            tmp_root / "data/poly-nvidia",
            "nvidia",
            args.nvidia_model,
            "https://integrate.api.nvidia.com/v1",
            "${NVIDIA_API_KEY:}",
        )
        contenders.append({
            "id": "polypod-nvidia",
            "agent": "polypod",
            "agent_version": poly_ver,
            "mode": "poly",
            "model": args.nvidia_model,
            "model_settings": "provider=nvidia;temperature=0.2;max_tokens=8192;max_tool_iterations=16",
            "skip_preflight": True,
            "spec": {
                "binary": str(poly_bin),
                "config_path": str(poly_nv_cfg),
                "nvidia_key_file": str(key_file),
                "secret_env_vars": ["NVIDIA_API_KEY"],
                "env": {
                    "HOME": str(poly_nv_home),
                    "GOPATH": base_env["GOPATH"],
                    "GOMODCACHE": base_env["GOMODCACHE"],
                    "GOCACHE": base_env["GOCACHE"],
                    "PATH": search_path,
                },
            },
        })

        # 2. Qwen Code CLI on identical free NVIDIA endpoint
        if qwen_bin:
            qwen_home = tmp_root / "homes/qwen-nvidia"
            qwen_home.mkdir(parents=True, exist_ok=True)
            contenders.append({
                "id": "qwen-code-nvidia",
                "agent": "qwen-code",
                "agent_version": cli_inventory["qwen"]["version"],
                "mode": "qwen",
                "model": args.nvidia_model,
                "model_settings": "auth_type=openai;base_url=integrate.api.nvidia.com;yolo=true;max_tool_calls=16",
                "spec": {
                    "binary": qwen_bin,
                    "base_url": "https://integrate.api.nvidia.com/v1",
                    "model": args.nvidia_model,
                    "max_steps": 16,
                    "nvidia_key_file": str(key_file),
                    "secret_env_vars": ["OPENAI_API_KEY"],
                    "env": {
                        "HOME": str(qwen_home),
                        "GOPATH": base_env["GOPATH"],
                        "GOMODCACHE": base_env["GOMODCACHE"],
                        "GOCACHE": base_env["GOCACHE"],
                        "PATH": search_path,
                    },
                },
            })

        # 3. Aider on identical free NVIDIA endpoint
        if aider_bin:
            aider_home = tmp_root / "homes/aider-nvidia"
            aider_home.mkdir(parents=True, exist_ok=True)
            contenders.append({
                "id": "aider-nvidia",
                "agent": "aider",
                "agent_version": cli_inventory["aider"]["version"],
                "mode": "aider",
                "model": args.nvidia_model,
                "model_settings": "provider=openai-compat;base_url=integrate.api.nvidia.com;yes_always=true",
                "spec": {
                    "binary": aider_bin,
                    "model": args.nvidia_model,
                    "nvidia_key_file": str(key_file),
                    "secret_env_vars": ["OPENAI_API_KEY"],
                    "env": {
                        "HOME": str(aider_home),
                        "OPENAI_API_BASE": "https://integrate.api.nvidia.com/v1",
                        "OPENAI_BASE_URL": "https://integrate.api.nvidia.com/v1",
                        "AIDER_ANALYTICS": "false",
                        "GOPATH": base_env["GOPATH"],
                        "GOMODCACHE": base_env["GOMODCACHE"],
                        "GOCACHE": base_env["GOCACHE"],
                        "PATH": search_path,
                    },
                },
            })

        # 4. OpenCode CLI on identical free NVIDIA endpoint
        if opencode_bin:
            oc_home = tmp_root / "homes/opencode-nvidia"
            oc_home.mkdir(parents=True, exist_ok=True)
            oc_config = {
                "$schema": "https://opencode.ai/config.json",
                "provider": {
                    "nvidia": {
                        "npm": "@ai-sdk/openai-compatible",
                        "name": "NVIDIA NIM",
                        "options": {
                            "baseURL": "https://integrate.api.nvidia.com/v1",
                            "apiKey": "{env:NVIDIA_API_KEY}",
                        },
                        "models": {
                            args.nvidia_model: {
                                "name": args.nvidia_model,
                            }
                        },
                    }
                },
            }
            contenders.append({
                "id": "opencode-nvidia",
                "agent": "opencode",
                "agent_version": cli_inventory["opencode"]["version"],
                "mode": "opencode",
                "model": args.nvidia_model,
                "model_settings": "provider=nvidia-openai-compat;dangerously_skip_permissions=true;pure=true",
                "spec": {
                    "binary": opencode_bin,
                    "model": f"nvidia/{args.nvidia_model}",
                    "nvidia_key_file": str(key_file),
                    "secret_env_vars": ["NVIDIA_API_KEY", "OPENAI_API_KEY"],
                    "env": {
                        "HOME": str(oc_home),
                        "XDG_CONFIG_HOME": str(oc_home / ".config"),
                        "XDG_DATA_HOME": str(oc_home / ".local/share"),
                        "OPENCODE_CONFIG_CONTENT": json.dumps(oc_config),
                        "GOPATH": base_env["GOPATH"],
                        "GOMODCACHE": base_env["GOMODCACHE"],
                        "GOCACHE": base_env["GOCACHE"],
                        "PATH": search_path,
                    },
                },
            })

        # 5. Codex CLI on identical free NVIDIA endpoint (isolated CODEX_HOME, zero paid calls)
        if codex_bin:
            cx_nv_home = tmp_root / "homes/codex-nvidia"
            cx_nv_codex = cx_nv_home / ".codex"
            cx_nv_codex.mkdir(parents=True, exist_ok=True)
            (cx_nv_codex / "config.toml").write_text(
                f'model = "{args.nvidia_model}"\n'
                'model_provider = "nvidia"\n\n'
                '[model_providers.nvidia]\n'
                'name = "NVIDIA NIM"\n'
                'base_url = "https://integrate.api.nvidia.com/v1"\n'
                'env_key = "NVIDIA_API_KEY"\n'
                'wire_api = "chat"\n'
            )
            contenders.append({
                "id": "codex-cli-nvidia",
                "agent": "codex-cli",
                "agent_version": cli_inventory["codex"]["version"],
                "mode": "codex",
                "model": args.nvidia_model,
                "model_settings": "provider=nvidia;wire_api=chat;dangerously_bypass_approvals_and_sandbox=true",
                "spec": {
                    "cmd_prefix": [
                        codex_bin,
                        "exec",
                        "--ephemeral",
                        "--dangerously-bypass-approvals-and-sandbox",
                        "--json",
                    ],
                    "cost_measured": False,
                    "nvidia_key_file": str(key_file),
                    "secret_env_vars": ["NVIDIA_API_KEY", "OPENAI_API_KEY"],
                    "env": {
                        "HOME": str(cx_nv_home),
                        "CODEX_HOME": str(cx_nv_codex),
                        "GOPATH": base_env["GOPATH"],
                        "GOMODCACHE": base_env["GOMODCACHE"],
                        "GOCACHE": base_env["GOCACHE"],
                        "PATH": search_path,
                    },
                },
            })

        # 6. Optional CPU-local Ollama contenders (measured US$0.0000 cost)
        if args.include_ollama and ollama_bin:
            poly_ol_home = tmp_root / "homes/poly-ollama"
            poly_ol_home.mkdir(parents=True, exist_ok=True)
            poly_ol_cfg = tmp_root / "configs/poly-ollama.yaml"
            write_poly_config(
                poly_ol_cfg,
                tmp_root / "data/poly-ollama",
                "ollama",
                args.ollama_model,
                "http://127.0.0.1:11434/v1",
                "ollama",
            )
            contenders.append({
                "id": "polypod-ollama-local",
                "agent": "polypod",
                "agent_version": poly_ver,
                "mode": "poly",
                "model": args.ollama_model,
                "model_settings": "provider=ollama;local=true;temperature=0.2;max_tokens=4096;max_tool_iterations=12",
                "skip_preflight": True,
                "spec": {
                    "binary": str(poly_bin),
                    "config_path": str(poly_ol_cfg),
                    "env": {
                        "HOME": str(poly_ol_home),
                        "GOPATH": base_env["GOPATH"],
                        "GOMODCACHE": base_env["GOMODCACHE"],
                        "GOCACHE": base_env["GOCACHE"],
                        "PATH": search_path,
                    },
                },
            })

        heldout_root = tmp_root / "heldout"
        heldout_root.mkdir(parents=True, exist_ok=True)

        non_comparable = [
            {
                "cli": "claude",
                "version": cli_inventory["claude"]["version"],
                "status": "excluded_zero_paid_policy",
                "reason": "Requer API proprietária Anthropic Messages (/v1/messages) ou sessão OAuth paga; excluída sob política estrita de zero chamadas pagas com HOME isolado.",
            },
            {
                "cli": "gemini",
                "version": cli_inventory["gemini"]["version"],
                "status": "excluded_zero_paid_policy",
                "reason": "Requer API Google Gemini / Vertex AI ou OAuth Google; excluída sob política estrita de zero chamadas pagas com HOME isolado.",
            },
            {
                "cli": "kilo",
                "version": cli_inventory["kilo"]["version"],
                "status": "covered_by_opencode_engine",
                "reason": "Derivado da mesma engine CLI de `opencode run`; inventariado localmente e representado na rodada OpenAI-compatível por `opencode`.",
            },
        ]

        runs_summary = []
        hw_desc = f"{platform.system()}-{platform.machine()}-cpu{os.cpu_count() or 1}"
        env_desc = f"linux-isolated-home-go-{subprocess.run([go_bin, 'env', 'GOVERSION'], text=True, capture_output=True).stdout.strip()}"

        for contender in contenders:
            cid = contender["id"]
            run_dir = tmp_root / "runs" / cid
            run_dir.mkdir(parents=True, exist_ok=True)
            spec_path = run_dir / "adapter_spec.json"
            spec_path.write_text(json.dumps(contender["spec"], indent=2))

            if not contender.get("skip_preflight"):
                ok, pf_status = preflight_adapter(adapter_py, contender["mode"], spec_path, base_env, redact_secret=nvidia_key)
                if not ok:
                    non_comparable.append({
                        "cli": contender["agent"],
                        "contender_id": cid,
                        "version": contender["agent_version"],
                        "model": contender["model"],
                        "status": "preflight_unavailable",
                        "reason": pf_status,
                    })
                    continue

            plan_cases = prepare_case_workspaces(cases_doc, run_dir, heldout_root, go_bin, base_env)
            for pc in plan_cases:
                pc["command"] = ["python3", str(adapter_py), contender["mode"], str(spec_path), "{prompt}"]

            plan_path = run_dir / "plan.json"
            plan_path.write_text(json.dumps({"cases": plan_cases}, indent=2))

            metadata = {
                "run_id": f"{cid}-{int(time.time())}",
                "agent": contender["agent"],
                "agent_version": contender["agent_version"],
                "model": contender["model"],
                "model_settings": contender["model_settings"],
                "hardware": hw_desc,
                "environment": env_desc,
                "dataset_revision": f"{git_rev[:12]}:cases={cases_sha256[:12]}",
                "evaluator_version": "polypod-evaluation-v1",
                "seed": 20260417,
                "budget": {
                    "max_steps": 16,
                    "max_seconds": 180.0,
                    "max_usd": 1.0,
                },
                "synthetic": False,
            }
            metadata_path = run_dir / "metadata.json"
            metadata_path.write_text(json.dumps(metadata, indent=2))

            results_path = run_dir / "results.json"
            subprocess.run(
                [
                    str(eval_run_bin),
                    "-manifest", str(args.manifest),
                    "-metadata", str(metadata_path),
                    "-plan", str(plan_path),
                    "-output", str(results_path),
                ],
                cwd=ROOT,
                env=base_env,
                check=True,
            )

            report_proc = subprocess.run(
                [
                    str(eval_report_bin),
                    "-manifest", str(args.manifest),
                    "-results", str(results_path),
                    "-format", "json",
                ],
                cwd=ROOT,
                env=base_env,
                text=True,
                capture_output=True,
                check=True,
            )
            report_json = json.loads(report_proc.stdout)
            runs_summary.append({
                "contender_id": cid,
                "report": report_json,
            })
            print(
                f"[{cid}] success={report_json['overall']['success']}/{report_json['overall']['total']} "
                f"p50={report_json['latency_ms']['p50']}ms p95={report_json['latency_ms']['p95']}ms "
                f"tokens={report_json['tokens']['total']} failures={report_json['tool_failures']['total']}",
                flush=True,
            )

        final_doc = {
            "schema_version": 1,
            "generated_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "source_git_revision": git_rev,
            "manifest_path": str(args.manifest.relative_to(ROOT)),
            "manifest_sha256": manifest_sha256,
            "cases_path": str(args.cases.relative_to(ROOT)),
            "cases_sha256": cases_sha256,
            "zero_paid_calls": True,
            "cli_inventory": cli_inventory,
            "evaluated_runs": runs_summary,
            "excluded_or_unavailable": non_comparable,
        }
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(final_doc, ensure_ascii=False, indent=2) + "\n")
    finally:
        shutil.rmtree(tmp_root, ignore_errors=True)


if __name__ == "__main__":
    main()
