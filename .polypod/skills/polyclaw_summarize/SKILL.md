---
name: polyclaw_summarize
description: Resuma URLs e arquivos locais com citações usando ferramentas nativas do Polypod.
---

# Resumir conteúdo

Adaptação da skill Summarize do PolyClaw sem depender do executável externo `summarize`, que não está instalado nesta máquina. Para URLs, use `web_fetch` e registre endereço e data consultada. Para arquivos locais, use `document_extract` e cite arquivo, página ou trecho retornado. Separe fatos do texto original de inferências do modelo; marque conteúdo não extraído ou truncado.

Vídeos e podcasts exigem uma transcrição disponível antes de resumir a fala. Uma página de descrição não prova o conteúdo do áudio. Fonte: `third_party/polyclaw/skills/summarize/`; licença MIT em `third_party/polyclaw/LICENSE`.
