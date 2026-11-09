# Skills e ferramentas importadas do PolyClaw

O catálogo original de oito skills está em `third_party/polyclaw/skills/`, com licença MIT em `third_party/polyclaw/LICENSE`. É uma cópia de referência da árvore local do PolyClaw; não é executada diretamente. Sete adaptações ativas ficam em `.polypod/skills/` e passam pelo registro de ferramentas e permissões do Polypod.

O catálogo é localizado a partir da pasta do `config.yaml`, mesmo quando o CLI é iniciado em outro projeto. Para usar uma cópia em outro local, defina `POLYPOD_POLYCLAW_CATALOG` com o caminho da pasta `skills`. As skills instaladas continuam sendo gravadas em `.polypod/skills/` do diretório de trabalho atual.

| Skill original | Estado no Polypod |
| --- | --- |
| `agent-browser` | Adaptada como `polyclaw_browser`, usando `browser_manage`, `browser_observe`, `browser_act` e desktop. O perfil gerenciado é separado do Chrome pessoal. |
| `github` | Adaptada como `polyclaw_github`, usando `gh` e as ferramentas Git locais. |
| `tmux` | Adaptada como `polyclaw_tmux`, usando o `bash` e um socket próprio. |
| `weather` | Adaptada como `polyclaw_weather`, com consulta web e atribuição de local/horário. |
| `summarize` | Adaptada como `polyclaw_summarize`, usando extração de documentos e páginas. Resumo de fala depende de transcrição. |
| `skill-creator` | Adaptada como `polyclaw_skill_creator`, criando `SKILL.md` do Polypod. |
| `hardware` | Adaptada como `polyclaw_hardware` para USB, serial, firmware e I2C/SPI. Barramentos foram testados com backends falsos; hardware real ainda precisa de ensaio. |
| `picoclaw-agent` | Referência apenas; descreve a CLI do PolyClaw, não uma capacidade do Polypod. |

As ferramentas de conhecimento em documentos, fila de tarefas, auditoria e navegador são implementações nativas; não dependem do executável PolyClaw. Um `SKILL.md` descreve como o agente usa ferramentas, mas não instala binários externos nem cria permissões de serviço. A lista ativa do agente padrão está em `agents/default.yaml`.

## Estado de integração

- Skills de filesystem válidas carregam mesmo se outra skill estiver inválida; o erro é registrado.
- Skills aparecem como ferramentas `skill_<name>` no runtime, com os mesmos controles de execução das demais ferramentas.
- Ferramentas nativas usam nomes diretos, como `bash`, `read_file_dir`, `glob`, `grep`, `write_file` e `edit_file`.
- O watcher de `SKILL.md` e o callback do registro de skills atualizam ou removem a ferramenta correspondente sem reiniciar o Polypod. O agente padrão recebe automaticamente novas skills registradas em tempo de execução a partir do próximo turno; outros agentes precisam incluir o nome na própria lista.
- `find_skills` pesquisa o snapshot local e as skills ativas; `install_skill` instala uma skill original ainda sem adaptação. Para instruções que já têm adaptação, retorna o nome ativo.

O Whisper e os embeddings FastEmbed locais estão configurados sem credenciais externas. Ainda são necessários testes reais de login no navegador, voz e documentos recebidos e enviados pelo WhatsApp, perguntas `ask_user` no telefone e comparação de desempenho com PolyClaw. Não há benchmark que prove vantagem geral sobre PolyClaw.
