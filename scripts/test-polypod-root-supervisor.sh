#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT

cat > "$test_dir/tmux" <<'EOF'
#!/bin/sh
case "$1" in
    has-session)
        if [ "${FAKE_TMUX_HEALTHY:-0}" = 1 ]; then exit 0; fi
        exit 1 ;;
    new-session) exit 0 ;;
    kill-session)
        if [ -n "${FAKE_TMUX_KILL_MARKER:-}" ]; then : > "$FAKE_TMUX_KILL_MARKER"; fi
        exit 0 ;;
    *) exit 2 ;;
esac
EOF
chmod 755 "$test_dir/tmux"

if POLYPOD_TMUX_BIN="$test_dir/tmux" \
    POLYPOD_SUPERVISOR_POLL_SECONDS=0.01 \
    timeout 5 sh "$repo_dir/scripts/polypod-root-supervisor" > "$test_dir/output" 2>&1; then
    printf 'supervisor aceitou uma sequencia de crashes rapidos\n' >&2
    exit 1
else
    status=$?
fi

if [ "$status" -ne 1 ]; then
    printf 'supervisor terminou com status inesperado: %s\n' "$status" >&2
    exit 1
fi
grep -q 'encerrando o supervisor' "$test_dir/output"
printf 'supervisor sinalizou crash loop ao systemd\n'

FAKE_TMUX_HEALTHY=1 FAKE_TMUX_KILL_MARKER="$test_dir/killed" \
    POLYPOD_TMUX_BIN="$test_dir/tmux" POLYPOD_SUPERVISOR_POLL_SECONDS=0.01 \
    timeout --preserve-status -s TERM 0.2 sh "$repo_dir/scripts/polypod-root-supervisor"
[ -f "$test_dir/killed" ]
printf 'supervisor encerrou e limpou a sessão ao receber TERM\n'
