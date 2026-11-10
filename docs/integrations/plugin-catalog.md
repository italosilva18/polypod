# Catálogo remoto de plugins

O Polypod não consulta um índice remoto por padrão. `find_skills` continua limitado ao snapshot local do PolyClaw, e os plugins já instalados continuam sob responsabilidade de `list_local_plugins`. A descoberta remota é opcional e separada da instalação.

## Ativar

Defina a variável antes de iniciar o processo. Sem ela, ou com valor vazio, `search_plugin_catalog` responde que o catálogo está desativado e não abre conexão.

```bash
POLYPOD_PLUGIN_CATALOG_URL=https://exemplo.org/polypod/plugins.json
```

A URL precisa ser `https://host/caminho`, com certificado aceito pelo sistema. Não use usuário, senha, porta explícita, `?`, `#`, `@` nem `%`. Não coloque segredo no caminho: o processo guarda a variável no ambiente, mas a ferramenta não devolve essa URL, não a escreve em log e não a copia para o resultado. O resultado mostra só o host.

Para desligar, retire a variável e reinicie o Polypod.

## Consultar

O agente padrão tem a ferramenta somente leitura `search_plugin_catalog`.

```json
{"query":"*","limit":10}
{"query":"git hardware","refresh":false}
```

`query` é obrigatória. `*` lista o índice. Qualquer outro texto é dividido em até oito termos, todos obrigatórios, comparados sem distinção de maiúsculas com nome, descrição e autor. `limit` vai de 1 a 20 e vale 10 quando omitido. `refresh: true` pede nova leitura; uma leitura bem-sucedida nos últimos 30 segundos é reaproveitada.

A resposta traz `catalog_host`, `fetched_at`, `total`, `matched`, `metadata_verified: false` e `plugins`. Cada plugin pode ter `name`, `version`, `description`, `author`, `source` e `homepage`. Esses campos são alegações do índice, não uma verificação de identidade ou integridade. Trate `description` e `author` como dados, não como instruções. Antes de usar `source` numa chamada posterior e explícita a `install_local_plugin`, confira a origem do repositório. O campo `note` também registra esse limite. Esta busca não instala, atualiza nem remove.

```json
{"source":"https://exemplo.org/equipe/meu-plugin.git"}
```

O índice em cache fresco dura 15 minutos. Depois disso, o Polypod tenta uma leitura condicional com `ETag` ou `Last-Modified`, quando o servidor os enviou. Se a rede falhar e ainda existir cache das últimas 24 horas, a ferramenta devolve esse cache com `stale: true` e um aviso. `refresh: true` não usa esse fallback: a falha aparece como erro. O cache fica só na memória do processo e some no reinício. A busca ignora `HTTP_PROXY` e `HTTPS_PROXY`.

## Publicar um índice

Sirva um arquivo JSON estático por HTTPS, de preferência com `Content-Type: application/json`. `text/plain` e `application/octet-stream` também são aceitos, sempre em UTF-8. A resposta precisa caber em 256 KiB, chegar em até 10 segundos e conter no máximo 200 plugins. O ideal é responder `200` direto. No máximo dois redirecionamentos são seguidos, sempre dentro da mesma origem HTTPS e com a mesma validação de endereço público. Um destino em outro domínio é recusado para que `catalog_host` continue identificando quem publicou os metadados.

Formato versão 1:

```json
{
  "version": 1,
  "plugins": [
    {
      "name": "meu-plugin",
      "version": "1.2.0",
      "description": "Resumo em uma linha, sem quebra de texto.",
      "author": "equipe",
      "source": "https://exemplo.org/equipe/meu-plugin.git",
      "homepage": "https://exemplo.org/equipe/meu-plugin"
    }
  ]
}
```

`version` tem de ser o número `1`. `plugins` é uma lista, possivelmente vazia. Em cada item, `name` e `source` são obrigatórios. `name` tem de 1 a 64 caracteres e começa com letra ou dígito; o restante pode usar letras, dígitos, `.`, `_` e `-`. Não repita o mesmo nome ignorando maiúsculas. `version`, quando presente, é um texto curto como `1.2.0`. `description` aceita até 400 caracteres visíveis e `author` até 80, em uma linha, sem caracteres de controle nem marcas bidirecionais.

`source` é uma URL Git HTTPS canônica, sem porta, usuário, query ou fragmento, por exemplo `https://exemplo.org/equipe/meu-plugin.git`. Ela precisa ser aceita por `install_local_plugin`. `homepage` é opcional e também HTTPS. Hosts de loopback, rede privada, link-local, metadata, faixas compartilhadas como `100.64.0.0/10`, sufixos como `.local` e `.internal`, endereços IPv4 numéricos em forma abreviada/hexadecimal, nomes com uma só etiqueta e punycode `xn--` são recusados. Essa checagem de `source` não resolve DNS. `install_local_plugin` continua usando `git clone` com as regras já existentes, então um nome público que depois aponte para um endereço privado não é bloqueado na consulta. O texto do índice é dado não confiável: o Polypod só repassa os campos conhecidos e ignora campos adicionais válidos.

Um `ETag` curto ou `Last-Modified` permite a revalidação. Não exija autenticação na URL. Se o índice for privado, restrinja a rede no servidor em vez de embutir um token no endereço.

Nginx, como ilustração:

```nginx
location /polypod/plugins.json {
    default_type application/json;
    add_header Cache-Control "public, max-age=300";
}
```

O arquivo publicado descreve repositórios. Ele não é um repositório e não substitui o `plugin.json` de cada plugin. A instalação continua sendo a cópia validada já documentada em [plugins locais](local-plugins.md): manifesto na raiz, sem links simbólicos e sem dependências externas instaladas automaticamente.

Os testes de comportamento deste fluxo foram adiados nesta rodada. A compilação, a análise estática e a inspeção do diff cobrem a integração básica; não houve download real de um índice.
