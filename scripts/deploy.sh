#!/usr/bin/env bash
set -euo pipefail

# Deploy the local root CLI service. This script intentionally does not copy config.yaml.
# Usage: ./scripts/deploy.sh --apply

usage() {
    printf 'Uso: ./scripts/deploy.sh --apply\n' >&2
}

fail() {
    printf 'Erro: %s\n' "$1" >&2
    exit 1
}

if [[ $# -ne 1 || "$1" != --apply ]]; then
    usage
    exit 2
fi

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
expected_root='/home/costa/Área de trabalho/CLI/polypod'
[[ "$repo_root" == "$expected_root" ]] || fail "a unidade systemd espera o checkout em $expected_root"
[[ -f "$repo_root/config.yaml" ]] || fail "config.yaml não encontrado em $repo_root"
rtk_source=${POLYPOD_RTK_SOURCE:-/home/costa/.local/bin/rtk}
[[ "$rtk_source" == /* ]] || fail 'POLYPOD_RTK_SOURCE deve ser um caminho absoluto'
[[ -x "$rtk_source" ]] || fail "RTK não encontrado em $rtk_source"
cd -- "$repo_root"
[[ -x /usr/bin/tmux ]] || fail '/usr/bin/tmux não está disponível'
[[ "$(uname -s)" == Linux ]] || fail 'este serviço requer Linux'
[[ $EUID -ne 0 ]] || fail 'execute como usuário comum com acesso a sudo'

for tool in go make sudo install systemctl readelf grep sha256sum; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool não está disponível"
done

go_version=$(go env GOVERSION)
if [[ ! "$go_version" =~ ^go([0-9]+)\.([0-9]+) ]]; then
    fail "não foi possível identificar a versão do Go: $go_version"
fi
major=${BASH_REMATCH[1]}
minor=${BASH_REMATCH[2]}
if (( major < 1 || (major == 1 && minor < 26) )); then
    fail "Go 1.26 ou superior é necessário; encontrado $go_version"
fi

# The host grants specific non-interactive sudo commands. Do not block a
# headless deployment waiting for a password prompt.
sudo -n true || fail 'sudo sem interacao nao esta disponivel'
sudo() { command sudo -n "$@"; }

build_dir=$(mktemp -d)
trap 'rm -rf -- "$build_dir"' EXIT

printf 'Compilando binário Linux com CGO desativado...\n'
make -C "$repo_root" build "BINARY=$build_dir/polypod-root"

# The Makefile's build target uses CGO_ENABLED=0; verify the resulting build metadata.
go version -m "$build_dir/polypod-root" | grep -Eq 'build[[:space:]]+CGO_ENABLED=0' \
    || fail 'o binário foi compilado com CGO habilitado'
program_headers=$(readelf -l "$build_dir/polypod-root") \
    || fail 'não foi possível verificar os cabeçalhos ELF do binário'
if [[ "$program_headers" == *INTERP* ]]; then
    fail 'o binário contém um interpretador dinâmico'
fi

sudo install -d -m 0755 /usr/local/libexec /usr/local/sbin
sudo install -d -m 0700 /var/backups/polypod
backup_dir=$(sudo mktemp -d "/var/backups/polypod/deploy-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
rtk_hash=$(sha256sum "$rtk_source")
rtk_hash=${rtk_hash%% *}

targets=(
    /usr/local/libexec/polypod-root
    /usr/local/libexec/polypod-rtk
    /usr/local/sbin/polypod-root-supervisor
    /usr/local/sbin/polypod-root-session
    /etc/systemd/system/polypod-root.service
)
names=(binary rtk supervisor session unit)
modes=(0755 0755 0755 0755 0644)
was_active=0
was_enabled=0
if sudo systemctl is-active --quiet polypod-root.service; then was_active=1; fi
if sudo systemctl is-enabled --quiet polypod-root.service; then was_enabled=1; fi

for i in "${!targets[@]}"; do
    if sudo test -e "${targets[$i]}"; then
        sudo cp -p "${targets[$i]}" "$backup_dir/${names[$i]}"
    else
        : > "$build_dir/missing-${names[$i]}"
    fi
done

rollback() {
    local restore_path
    printf 'A verificação falhou; restaurando artefatos de %s...\n' "$backup_dir" >&2
    sudo systemctl stop polypod-root.service || true
    sudo rm -f -- /usr/local/libexec/polypod-root.new /usr/local/libexec/polypod-rtk.new || true
    for i in "${!targets[@]}"; do
        if sudo test -e "$backup_dir/${names[$i]}"; then
            if [[ "${names[$i]}" == binary || "${names[$i]}" == rtk ]]; then
                restore_path="${targets[$i]}.restore"
                if sudo install -m 0755 "$backup_dir/${names[$i]}" "$restore_path"; then
                    sudo mv -f "$restore_path" "${targets[$i]}" || true
                else
                    printf 'Falha ao preparar a restauração de %s; backup em %s\n' "${names[$i]}" "$backup_dir" >&2
                fi
            else
                sudo install -m "${modes[$i]}" "$backup_dir/${names[$i]}" "${targets[$i]}" || true
            fi
        elif [[ -f "$build_dir/missing-${names[$i]}" ]]; then
            sudo rm -f -- "${targets[$i]}" || true
        fi
    done
    sudo systemctl daemon-reload || true
    if (( was_enabled )); then
        sudo systemctl enable polypod-root.service || true
    else
        sudo systemctl disable polypod-root.service || true
    fi
    sudo systemctl reset-failed polypod-root.service || true
    if (( was_active )); then
        sudo systemctl start polypod-root.service || true
    fi
}

deploy_step() {
    if "$@"; then return 0; fi
    rollback
    fail "etapa de implantação falhou: $1; backup preservado em $backup_dir"
}

whatsapp_ready() {
    local report channel_line
    report=$(sudo /usr/local/libexec/polypod-root doctor 2>/dev/null) || return 1
    channel_line=$(printf '%s\n' "$report" | grep 'WhatsApp nativo' || true)
    [[ "$channel_line" == *'[OK]'* ]] &&
        { [[ "$channel_line" == *'conexao ativa'* ]] || [[ "$channel_line" == *'desativado'* ]]; }
}

wait_ready() {
    # Wait beyond the doctor's one-minute heartbeat freshness window. This
    # prevents a status file from the previous process from passing the gate.
    local deadline=$((SECONDS + 150)) pane_pid stable_pid='' stable_since=$SECONDS good_checks=0
    while (( SECONDS < deadline )); do
        if sudo systemctl is-active --quiet polypod-root.service &&
            pane_pid=$(sudo /usr/bin/tmux list-panes -t polypod-root -F '#{pane_pid}' 2>/dev/null) &&
            [[ "$pane_pid" =~ ^[0-9]+$ ]]; then
            if [[ "$pane_pid" != "$stable_pid" ]]; then
                stable_pid=$pane_pid
                stable_since=$SECONDS
                good_checks=0
            fi
            if (( SECONDS - stable_since >= 65 )); then
                if whatsapp_ready; then
                    good_checks=$((good_checks + 1))
                    if (( good_checks >= 2 )); then return 0; fi
                else
                    good_checks=0
                fi
            fi
        else
            stable_pid=''
            stable_since=$SECONDS
            good_checks=0
        fi
        sleep 5
    done
    return 1
}

printf 'Instalando unidade, lançadores e binário; backup em %s...\n' "$backup_dir"
deploy_step sudo install -m 0755 "$repo_root/scripts/polypod-root-supervisor" /usr/local/sbin/polypod-root-supervisor
deploy_step sudo install -m 0755 "$repo_root/scripts/polypod-root-session" /usr/local/sbin/polypod-root-session
deploy_step sudo install -m 0644 "$repo_root/scripts/polypod-root.service" /etc/systemd/system/polypod-root.service
deploy_step sudo install -o root -g root -m 0755 "$rtk_source" /usr/local/libexec/polypod-rtk.new
deploy_step sudo mv -f /usr/local/libexec/polypod-rtk.new /usr/local/libexec/polypod-rtk
deploy_step sudo install -m 0755 "$build_dir/polypod-root" /usr/local/libexec/polypod-root.new
deploy_step sudo mv -f /usr/local/libexec/polypod-root.new /usr/local/libexec/polypod-root

installed_rtk_hash=$(sudo sha256sum /usr/local/libexec/polypod-rtk)
installed_rtk_hash=${installed_rtk_hash%% *}
if [[ "$installed_rtk_hash" != "$rtk_hash" ]]; then
    rollback
    fail "hash do RTK instalado diverge da origem; backup preservado em $backup_dir"
fi

expected_hash=$(sha256sum "$build_dir/polypod-root")
expected_hash=${expected_hash%% *}
installed_hash=$(sudo sha256sum /usr/local/libexec/polypod-root)
installed_hash=${installed_hash%% *}
if [[ "$installed_hash" != "$expected_hash" ]]; then
    rollback
    fail "hash do binário instalado diverge do build; backup preservado em $backup_dir"
fi

printf 'Recarregando systemd e verificando processo e WhatsApp...\n'
deploy_step sudo systemctl daemon-reload
deploy_step sudo systemctl enable polypod-root.service
deploy_step sudo systemctl reset-failed polypod-root.service
deploy_step sudo systemctl restart polypod-root.service
if ! wait_ready; then
    rollback
    fail "serviço ou WhatsApp não ficou pronto em 150s; backup preservado em $backup_dir"
fi
printf 'Implantação saudável. Binário SHA-256: %s\n' "$installed_hash"
sudo systemctl --no-pager --full status polypod-root.service || true
