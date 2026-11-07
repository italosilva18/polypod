#!/usr/bin/env python3
"""Run the installed doctor's live probes with the service's desktop env.

Invoking ``sudo polypod doctor --live`` directly loses the Wayland and DBus
variables from systemd, so that invocation cannot diagnose the desktop used by
the running service. Only a fixed allowlist of display variables is copied;
credentials and unrelated service environment never enter this subprocess.
"""

import os
import pathlib
import shlex
import subprocess
import sys


SERVICE = "polypod-root.service"
BINARY = "/usr/local/libexec/polypod-root"
DESKTOP_KEYS = {
    "HOME",
    "DISPLAY",
    "WAYLAND_DISPLAY",
    "XDG_RUNTIME_DIR",
    "DBUS_SESSION_BUS_ADDRESS",
    "XDG_CURRENT_DESKTOP",
    "XDG_SESSION_TYPE",
    "POLYPOD_DESKTOP_USER",
    "POLYPOD_DESKTOP_HOME",
    "YDOTOOL_SOCKET",
    "PATH",
}


def main() -> int:
    repo = pathlib.Path(__file__).resolve().parent.parent
    result = subprocess.run(
        ["systemctl", "show", SERVICE, "--property=Environment", "--value", "--no-pager"],
        capture_output=True,
        text=True,
        check=False,
        timeout=8,
    )
    if result.returncode != 0:
        print("Não foi possível ler o ambiente da unidade Polypod.", file=sys.stderr)
        return 1

    values: dict[str, str] = {}
    for entry in shlex.split(result.stdout):
        key, sep, value = entry.partition("=")
        if sep and key in DESKTOP_KEYS:
            values[key] = value
    if not values.get("POLYPOD_DESKTOP_USER") or not (
        values.get("WAYLAND_DISPLAY") or values.get("DISPLAY")
    ):
        print("A unidade Polypod não declara uma sessão gráfica delegada.", file=sys.stderr)
        return 1

    command = ["/usr/bin/env", *(f"{k}={v}" for k, v in values.items()), BINARY, "doctor", "--live"]
    if os.geteuid() != 0:
        command = ["sudo", "-n", *command]
    os.chdir(repo)
    os.execvp(command[0], command)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
