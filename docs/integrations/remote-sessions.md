# Sessões remotas no terminal

O host do Polypod oferece sessões persistentes em HTTP no endereço local `127.0.0.1:8789`. O comando `polypod remote` cria ou entra em uma sessão e envia cada linha digitada ao agente completo. As respostas e mensagens de outros participantes aparecem no mesmo terminal.

## Preparar o host

No ambiente privado do processo que executa o Polypod, configure:

```dotenv
POLYPOD_REMOTE_SESSION_HOST=1
POLYPOD_REMOTE_SESSION_LISTEN=127.0.0.1:8789
POLYPOD_REMOTE_SESSION_TOKEN=<segredo aleatório de 32 a 512 caracteres>
```

O token deve ficar no ambiente ou em `.env` privado com permissão restrita. Use o mesmo token no cliente, sem passá-lo como argumento de linha de comando. Apenas um processo hospeda a porta; os demais clientes não precisam de `POLYPOD_REMOTE_SESSION_HOST=1`.

## Conectar

Com o host ativo e `POLYPOD_REMOTE_SESSION_TOKEN` disponível no ambiente privado do cliente:

```bash
polypod remote --participant costa-notebook
```

O comando cria a primeira sessão, mostra seu ID `session_...` e grava somente esse ID em `~/.config/polypod/remote-session-costa-notebook.txt`. Na próxima execução, a mesma identidade retoma a sessão e o cursor de leitura salvo no servidor. Digite uma mensagem e pressione Enter; `/exit` fecha somente o cliente.

Se o agente usar `ask_user`, a pergunta aparecerá na mesma sessão com um ID `q_...`. Responda na linha do terminal com `resposta q_ID seu texto`, substituindo o ID pelo mostrado na pergunta. A resposta só é aceita da mesma identidade que iniciou o turno; a espera dura até cinco minutos. Pergunta e resposta ficam registradas para uma eventual retomada do turno após reinício.

Comandos úteis:

```bash
polypod remote --participant costa-notebook --list
polypod remote --participant costa-notebook --session session_0123456789abcdef0123456789abcdef
polypod remote --participant costa-notebook --new
```

Substitua o ID do exemplo pelo ID mostrado ao criar a sessão. `--session` entra em uma sessão existente e salva o ID para a próxima execução. `--new` cria outra sessão e substitui o ID salvo. Use um `--participant` diferente para cada cliente simultâneo; o cursor é associado a essa identidade. `--state-file` escolhe outro arquivo de ID, e `--url` troca o endereço do servidor.

Para acessar de outra máquina, mantenha o servidor em loopback e abra um túnel SSH:

```bash
ssh -L 8789:127.0.0.1:8789 usuario@maquina-do-polypod
polypod remote --url http://127.0.0.1:8789 --participant costa-notebook
```

O cliente aceita HTTP apenas em loopback; use HTTPS para um endereço remoto direto. O token nunca aparece na saída do comando. A interface remota é de texto, linha por linha: não envia anexos nem oferece a tela Bubbletea do CLI principal. Mensagens já entregues usam o cursor persistido do participante; se o cliente encerrar entre exibir e confirmar uma mensagem, ela pode aparecer novamente ao reconectar.
