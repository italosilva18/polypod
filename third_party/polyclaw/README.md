# Catálogo de skills do PolyClaw

Este diretório preserva o snapshot das oito skills e dos recursos em `workspace/skills` do checkout local do PolyClaw, sob a licença MIT incluída em `LICENSE`. A origem foi o worktree `/home/costa/picoclaw-src` em 6 de novembro de 2026; ele continha alterações locais. Este catálogo é referência de código e **não** é carregado automaticamente pelo Polypod.

As adaptações operacionais ficam em `.polypod/skills/`. GitHub, tmux, clima e criação de skills foram adaptados aos nomes e ferramentas do Polypod. `summarize` usa o extrator e a ferramenta web nativos, pois o executável externo não está instalado. A skill de navegador é adaptada ao backend CDP do Polypod. `hardware` foi adaptada para USB/serial/firmware e I2C/SPI; os barramentos ainda precisam de periférico para ensaio físico. `picoclaw-agent` documenta o próprio PolyClaw e permanece somente como referência. O catálogo não comprova que cada dependência ou ação funciona no serviço implantado.
