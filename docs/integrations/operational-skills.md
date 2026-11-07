# Skills operacionais do Polypod

As skills em `.polypod/skills` são instruções reutilizáveis carregadas pelo `FSSkillLoader`. O runtime registra cada `SKILL.md` como uma ferramenta de leitura e a adiciona ao agente padrão na inicialização ou no hot reload. Elas organizam o uso das ferramentas existentes; não instalam bibliotecas, modelos ou serviços.

| Skill | Quando usar | Ferramentas principais |
| --- | --- | --- |
| `polypod_evidence_research` | Questões atuais, técnicas e comparações | `web_search`, `web_fetch`, `document_extract` |
| `polypod_document_analysis` | PDFs, Office, imagens e consultas entre arquivos | `document_extract`, `document_knowledge`, `image_analyze` |
| `polypod_routine_recovery` | Falhas ou resultados incertos em rotinas e mensagens | `heartbeat_status`, `inbox_list`, `scheduler_history`, `task_queue`, `audit` |

Esses três fluxos são nativos. O snapshot PolyClaw contém oito manifests em `third_party/polyclaw/skills`; sete já têm adaptações em `.polypod/skills`. O restante, `picoclaw-agent`, documenta comandos e configuração do runtime PicoClaw e não representa uma ferramenta transferível ao Polypod.
