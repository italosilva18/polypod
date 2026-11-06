# Catálogo e instalação local de skills

`find_skills` pesquisa o snapshot em `third_party/polyclaw/skills` e as skills ativas em `.polypod/skills`. Use `query: "*"` para listar tudo ou termos como `github` e `hardware`; `limit` aceita 1 a 20. Cada resultado informa origem e estado. As adaptações já ativas aparecem como `active_adapter`, com seu nome Polypod em `active_as`.

`install_skill` recebe um `slug` do catálogo PolyClaw. Para um slug já adaptado, retorna `already_active` e o caminho da adaptação; não instala uma cópia crua duplicada. Para uma skill ainda não presente, valida o nome, o `SKILL.md` e todos os arquivos, copia para um diretório temporário fora da árvore observada e renomeia o diretório completo para `.polypod/skills/<slug>`. A operação recusa links simbólicos, caminhos fora da skill, arquivos irregulares, manifesto inválido e pacotes acima de 128 arquivos ou 8 MiB. Uma skill existente não é sobrescrita. `.skill-origin.json` registra catálogo, slug, instante e SHA-256 do conteúdo copiado.

O registro de skills e a lista do agente padrão são atualizados logo após a instalação; o watcher também encontra o diretório completo na próxima varredura. A nova skill fica disponível no próximo turno do agente. Outros agentes precisam incluir seu nome na própria lista. Um `SKILL.md` oferece instruções e não instala executáveis ou credenciais exigidos por elas.

As ferramentas usam apenas o snapshot local já incluído no repositório. Não fazem busca, download nem instalação de fontes remotas. O catálogo original está sob licença MIT em `third_party/polyclaw/LICENSE`.
