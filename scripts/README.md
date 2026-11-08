# Implantação do serviço local

O serviço instalado nesta máquina é `polypod-root.service`. Ele executa a CLI
interativa como `root` dentro da sessão tmux `polypod-root`. O serviço usa o
checkout em `/home/costa/Área de trabalho/CLI/polypod`, o arquivo local
`config.yaml` e o binário em `/usr/local/libexec/polypod-root`. Não há
dependência de PostgreSQL nem servidor REST nesse serviço.

Os arquivos `polypod-root.service`, `polypod-root-supervisor` e
`polypod-root-session` espelham a instalação atual. O caminho do checkout, o
usuário da sessão gráfica, o UID, o display e o ambiente COSMIC são específicos
desta máquina. Revise esses valores antes de usar os arquivos em outro host.
O socket de entrada Wayland depende do serviço separado `polypod-ydotool.service`;
sem ele, o diagnóstico marca entrada do desktop como indisponível.

Para verificar a captura e o socket de entrada com o mesmo ambiente gráfico da
unidade, execute `python3 scripts/doctor-live-service.py` no checkout. Um
`sudo polypod doctor --live` direto perde variáveis Wayland/DBus sob `sudo` e
pode acusar uma falha do desktop que o serviço não tem. A checagem captura a
tela em memória e descarta a imagem; não envia cliques nem teclas.

## Implantar nesta máquina

É necessário ter Go 1.26 ou superior, `make`, `sudo`, `readelf`, `systemd` e
`/usr/bin/tmux`. Prepare `config.yaml` no checkout sem colocar credenciais no
Git. O script preserva os artefatos instalados em um diretório privado sob
`/var/backups/polypod/`; faça também backup dos dados com o serviço parado
antes de uma migração de estado.

```bash
./scripts/deploy.sh --apply
```

O script compila em um diretório temporário com o alvo `make build`, que usa
`CGO_ENABLED=0`, e verifica se o executável não contém interpretador dinâmico.
Depois instala o binário, os dois lançadores e a unidade systemd; recarrega o
systemd, habilita o serviço na inicialização e o reinicia. Para aceitar a
implantação, exige o mesmo hash do build, um processo tmux estável por mais de
um minuto e diagnóstico WhatsApp recente com sessão autenticada quando esse
canal estiver ativado. Se a verificação falhar em 150 segundos, restaura os
artefatos anteriores e tenta reativar o serviço. O backup fica preservado para
inspeção. O script não copia nem substitui `config.yaml`, `.env`, os dados SQLite
ou outros arquivos de estado.

O deploy também instala uma cópia do RTK de `~/.local/bin/rtk` em
`/usr/local/libexec/polypod-rtk`, pertencente a `root`, e compara o hash antes de
ativar a unidade. `POLYPOD_RTK_SOURCE` permite escolher outro executável de
origem. A integração compacta apenas saídas elegíveis da ferramenta `bash` e
desativa o recall integral. Veja [o guia do RTK](../docs/integrations/rtk.md).

Para conferir a execução depois da atualização:

```bash
sudo systemctl status polypod-root.service --no-pager
sudo tmux has-session -t polypod-root
```

O processo supervisionado é interativo. Se ele sair rapidamente três vezes,
o supervisor termina com falha; o limite de reinícios do systemd impede um
ciclo infinito que pareça saudável. A verificação do deploy também acompanha
o PID do processo dentro da sessão tmux.

Para testar o sinal de falha sem tocar no serviço instalado:

```bash
sh scripts/test-polypod-root-supervisor.sh
```

## Acesso e dados

A unidade espelha o serviço instalado: roda sem isolamento de privilégios como
`root`, recebe acesso à sessão gráfica do usuário e usa `UMask=0000`. Assim, a
CLI pode alterar arquivos e processos do sistema, e novos arquivos podem ficar
graváveis por outros usuários. Restrinja o acesso ao host, mantenha
`config.yaml` e `.env` privados e revise o perfil de permissões antes de
habilitar integrações externas. O banco SQLite e as sessões ficam nos caminhos
configurados; faça backup deles com o serviço parado para obter uma cópia
consistente.
