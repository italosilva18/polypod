# Backup e restauração do Polypod

O comando `backup` funciona sem iniciar o agente. Execute-o no diretório do projeto que contém `config.yaml`.

```bash
polypod backup create --output /caminho/seguro/polypod-2026-11-10.zip
polypod backup inspect /caminho/seguro/polypod-2026-11-10.zip --list
polypod backup restore /caminho/seguro/polypod-2026-11-10.zip
polypod backup restore /caminho/seguro/polypod-2026-11-10.zip --confirm
```

`restore` mostra a prévia por padrão; só modifica arquivos com `--confirm`. Pare o serviço Polypod antes da restauração e reinicie depois. A restauração não apaga arquivos ausentes do backup.

Para restaurar apenas uma parte, repita `--only` com a raiz ou o prefixo do caminho mostrado no manifesto. Por exemplo:

```bash
polypod backup restore arquivo.zip --only project/data/conversations/sessions
polypod backup restore arquivo.zip --only project/data/conversations/sessions --confirm
```

Todos os itens do ZIP continuam sendo verificados antes da restauração seletiva.

## Conteúdo

O ZIP inclui a configuração, `config.d/`, `.polypod/`, diretório de agentes, `data/`, diretório de dados configurado, banco SQLite e arquivo do agendador. Bancos `.db`, `.sqlite` e `.sqlite3` são copiados com `VACUUM INTO`, que produz um retrato consistente de cada banco mesmo quando o serviço está gravando. Arquivos `-wal` e `-shm` não entram no ZIP. Logs, locks, arquivos temporários, caches descartáveis e backups Polypod anteriores são omitidos, mesmo que um backup anterior tenha um nome personalizado. Outros arquivos ZIP permanecem no backup.

Os caminhos configurados são resolvidos com a mesma mesclagem de `config.yaml` e `config.d/*.yaml` usada pelo runtime. Isso inclui caminhos de dados, agentes, banco e sessão do WhatsApp definidos nos fragmentos.

Por padrão, `.env`, `.env.local`, `credentials.enc`, `credentials.json`, `secrets.enc`, `chatgpt-auth.enc`, arquivos de token conhecidos, chaves privadas e a sessão vinculada do WhatsApp são excluídos. A sessão do WhatsApp fica fora das varreduras gerais mesmo quando seu diretório está dentro de `data/` ou `.polypod/`; com `--include-secrets`, ela entra apenas na raiz `whatsapp` do ZIP. Valores de chaves, tokens e senhas reconhecidos em `config.yaml`, `config.d/` e `.polypod/settings.json`, inclusive campos em camelCase, são substituídos no ZIP por um marcador. Na restauração, o Polypod conserva os valores existentes nesses campos; quando não existem, deixa-os vazios.

Para levar também esses arquivos de credenciais e a sessão do WhatsApp:

```bash
polypod backup create --output /caminho/seguro/polypod-com-credenciais.zip --include-secrets
```

Mesmo sem `--include-secrets`, **o ZIP é sensível**: conversas, journal, memória e resultados de ferramentas podem conter senhas ou outros dados que você tenha enviado ao agente. O arquivo é criado com permissão `0600`; mantenha-o em armazenamento protegido. Segredos que estejam no chaveiro do sistema operacional não são exportados por este comando.

## Limites de consistência e restauração

Cada banco SQLite tem seu próprio retrato consistente. Os demais arquivos são copiados individualmente e a criação falha quando um arquivo muda durante a leitura. Isso não forma uma transação única entre todos os arquivos se o serviço estiver ativo; para um backup integralmente coerente, pare o serviço antes de `create`.

A restauração verifica tamanho e SHA-256 de todos os itens antes de modificar o disco e confere novamente cada item enquanto prepara o arquivo de destino. Rejeita caminhos fora das raízes previstas e diretórios pais que sejam links simbólicos. Ela prepara os novos arquivos na mesma partição e usa renomeações atômicas por arquivo. Se ocorrer um erro durante a instalação, tenta restaurar os arquivos anteriores. Uma queda abrupta do sistema entre renomeações pode deixar um conjunto parcialmente restaurado; conserve o ZIP para executar novamente a restauração.

Para evitar que um WAL antigo se sobreponha ao banco restaurado, a prévia e a restauração recusam substituir qualquer banco SQLite cujo `-wal` ou `-shm` ainda exista no destino. Pare o serviço e faça o checkpoint/fechamento limpo do banco antes de tentar novamente. Se o rollback falhar, o comando informa a falha parcial e mantém os arquivos originais temporários para recuperação manual.

Quando o `config.yaml` ainda não existe na máquina de destino, informe os caminhos de destino conforme necessário: `--config`, `--data-dir`, `--database`, `--agents-dir`, `--scheduler` e `--whatsapp-session-dir`. O comando `--project` seleciona outro diretório de projeto.

Planos de verificação em `goals.json` e `evolution.db` podem conter caminhos absolutos para arquivos ou bancos. Ao migrar para outra máquina ou diretório, confira esses alvos antes de retomar metas e rotinas. Se o caminho mudou, crie uma nova meta ou rotina com um plano válido, ou disponibilize o caminho antigo no destino; restaurar o ZIP não reescreve planos imutáveis.
