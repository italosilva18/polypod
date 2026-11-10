# Plugins locais

O Polypod encontra diretórios com `plugin.json` dentro das raízes de skills.
Cada manifesto declara `name`, `skills` e, opcionalmente, `version`, `author`
e `enabled`. As skills podem ser arquivos YAML ou Markdown com frontmatter.

```json
{"name":"meu-plugin","version":"1.0.0","skills":["skills/exemplo.yaml"]}
```

O agente dispõe das ferramentas `list_local_plugins`, `install_local_plugin`,
`update_local_plugin`, `set_local_plugin_enabled` e `reload_local_plugins`.
Para instalar, passe `source` como caminho de uma pasta local ou URL Git HTTPS/SSH
explícita. O manifesto deve estar na raiz da pasta ou do repositório. Para
atualizar, passe também o `id` devolvido por `list_local_plugins`:

```json
{"source":"/caminho/para/meu-plugin"}
{"id":"meu-plugin@12345678","source":"https://exemplo.org/equipe/meu-plugin.git"}
```

Na instalação, `enabled` ausente vira `true`. Na atualização, a ausência do
campo preserva o estado anterior. `false` explícito desativa o plugin. A nova
versão é validada antes da publicação; nomes de skills pertencentes a outra
origem não são substituídos. O registro troca todas as skills do plugin em uma
operação, e uma falha restaura a versão anterior. No Linux, a troca de
diretórios é atômica. Nas demais plataformas, o próximo ciclo de reconciliação
recupera um backup deixado por uma interrupção entre renomeações.

O instalador rejeita links simbólicos e arquivos especiais. URLs com usuário,
senha, query ou fragmento são rejeitadas para não expor credenciais em
argumentos de processo; use autenticação SSH ou o gerenciador de credenciais
do Git. Os plugins do projeto são publicados em `.polypod/skills`; o serviço
root e o CLI do usuário compartilham o diretório com acesso de grupo.

Instalação e atualização não instalam dependências externas declaradas pela
skill. Os testes de comportamento desse fluxo foram adiados nesta rodada;
compilação, análise estática e inspeção do diff verificam a integração básica.
