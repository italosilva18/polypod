# Plugins locais

O Polypod encontra diretórios com `plugin.json` dentro das raízes de skills.
Cada manifesto declara `name` e pelo menos um recurso em `skills`, `hooks` ou
`mcp_servers`. `schema_version: 1` fixa o contrato; a ausência do campo mantém
compatibilidade com manifestos antigos. Pacotes com hooks ou MCP exigem
`version`. As skills podem ser arquivos YAML ou Markdown com frontmatter.

```json
{"name":"meu-plugin","version":"1.0.0","skills":["skills/exemplo.yaml"]}
```

Um pacote pode incluir os três tipos de recurso:

```json
{
  "schema_version": 1,
  "name": "meu-plugin",
  "version": "1.0.0",
  "skills": ["skills/exemplo.yaml"],
  "hooks": ["hooks.yaml"],
  "mcp_servers": ["mcp.json"]
}
```

`hooks.yaml` contém uma lista de hooks no formato `name`, `event`, `type`
(`shell` ou `http`) e `command` ou `url`. `mcp.json` contém um objeto
`mcpServers` com as definições de servidores; YAML também é aceito. O
instalador valida esses arquivos antes de publicar a versão. Hooks e servidores
ativos recebem um prefixo exclusivo do plugin e são atualizados ou removidos
quando o plugin muda de estado. Uma falha de conexão MCP é registrada e tentada
novamente no próximo ciclo; ela não desfaz skills já instaladas. O teste local
cobre carregamento, descarregamento e chamada de um servidor MCP empacotado.

O agente padrão dispõe das ferramentas `list_local_plugins`, `install_local_plugin`,
`update_local_plugin`, `remove_local_plugin`, `set_local_plugin_enabled` e
`reload_local_plugins`.
Na interface do terminal, os mesmos recursos estão em `/plugin list`,
`/plugin info <id>`, `/plugin install <pasta-ou-URL-Git>`,
`/plugin update <id> <pasta-ou-URL-Git>`, `/plugin enable <id>`,
`/plugin disable <id>`, `/plugin remove <id>` e `/plugin reload [id]`.
`/reload-plugins` recarrega todos os plugins.
Para instalar, passe `source` como caminho de uma pasta local ou URL Git HTTPS/SSH
explícita. O manifesto deve estar na raiz da pasta ou do repositório. Para
atualizar, passe também o `id` devolvido por `list_local_plugins`:

```json
{"source":"/caminho/para/meu-plugin"}
{"id":"meu-plugin@12345678","source":"https://exemplo.org/equipe/meu-plugin.git"}
```

`remove_local_plugin` recebe o mesmo `id`, retira o diretório do catálogo e
descarrega as skills do processo. Uma falha ao descarregar restaura o diretório;
uma queda após a retirada pode deixar uma cópia oculta para limpeza manual.

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
skill. Um teste de ponta a ponta cobre a instalação, consulta, ativação,
desativação, recarga e remoção pelo comando `/plugin`.

Não há índice remoto embutido. Para publicar ou consultar uma lista HTTPS
opcional de metadados e URLs `source`, sem instalar nada nessa consulta, veja
[catálogo remoto de plugins](plugin-catalog.md). A instalação de um item
encontrado continua sendo uma chamada explícita a `install_local_plugin`;
nome, autor, descrição e URL do índice são alegações não verificadas.
