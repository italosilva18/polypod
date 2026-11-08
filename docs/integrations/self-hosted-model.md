# Modelo próprio no Polypod

O Polypod pode executar seu agente com um modelo servido em infraestrutura sob
seu controle. Isso permite escolher o modelo, o servidor de inferência e as
instruções do agente. Não há uma configuração local capaz de substituir uma
decisão tomada pelo modelo do ChatGPT enquanto o ChatGPT for o provedor ativo.
Mesmo em infraestrutura própria, o modelo escolhido pode recusar pedidos por
causa do seu treinamento, e o operador da GPU ainda tem suas próprias regras.

## Estado atual

O serviço em execução usa `chatgpt` como provedor principal e DeepSeek como
alternativa em falhas de disponibilidade. Não existe ainda um servidor de GPU
ou endpoint próprio para ativar este perfil. O computador local não é uma opção
viável para executar um modelo grande. Mantenha o serviço atual até o endpoint
estar acessível; alterar `config.yaml` antes disso interromperia as respostas do
agente nos canais conectados.

## Requisitos para a troca

1. Um servidor com GPU ou endpoint OpenAI-compatible sob seu controle, com
   `/v1/chat/completions`, streaming e chamadas de ferramentas funcionais.
2. Um modelo cujo template e servidor suportem `tool_choice=auto`. O Polypod
   envia suas ferramentas ao modelo; sem essa capacidade, ele perde as ações.
3. Uma conexão privada entre o Polypod e o servidor, um identificador de modelo
   e uma chave de API do endpoint. A chave da conta ChatGPT não serve para isso.

O [vLLM](https://docs.vllm.ai/en/stable/serving/online_serving/openai_compatible_server/)
é uma opção de servidor compatível com a API que o Polypod já usa. Para
chamadas automáticas de ferramentas, selecione um modelo e seu parser de acordo
com a [documentação do vLLM](https://docs.vllm.ai/en/stable/features/tool_calling/):

```sh
vllm serve MODEL_ID --host 127.0.0.1 --port 8000 \
  --enable-auto-tool-choice --tool-call-parser PARSER_DO_MODELO
```

Configure `VLLM_API_KEY` no ambiente privado do serviço vLLM. Se a GPU estiver
em outra máquina, conecte-se por rede privada ou túnel SSH; o endereço
`127.0.0.1` do exemplo não é acessível diretamente de outro computador. Não
publique o servidor vLLM diretamente na internet: sua autenticação de API não
cobre todos os endpoints, conforme a documentação oficial.

## Configuração do Polypod

O perfil [`examples/selfhosted-vllm.yaml`](../../examples/selfhosted-vllm.yaml)
mostra todos os campos necessários. Defina no ambiente privado do Polypod:

```text
POLYPOD_SELFHOSTED_BASE_URL=https://SEU_ENDPOINT_PRIVADO/v1
POLYPOD_SELFHOSTED_API_KEY=CHAVE_DO_SEU_ENDPOINT
POLYPOD_SELFHOSTED_MODEL=ID_EXATO_DO_MODELO_SERVIDO
```

Para preservar WhatsApp, agendador, memória e demais canais, mude somente o
bloco `ai:` do `config.yaml` privado em uso pelo serviço. Copie o bloco do
exemplo, mantendo as referências às variáveis de ambiente, e configure as três
variáveis no ambiente desse mesmo serviço. Deixe os quatro campos `fallback_*`
vazios se a intenção é não depender de outro provedor de modelos. Isso também
significa que uma indisponibilidade do servidor próprio interromperá respostas
até que ele volte.

Após iniciar o endpoint e configurar o Polypod, confirme que `/v1/models` lista
o ID configurado e que uma requisição a `/v1/chat/completions` consegue chamar
uma ferramenta. Só então reinicie o serviço Polypod e confira uma tarefa com
ferramenta por um canal conectado. O exemplo isolado pode ser usado para uma
primeira sessão de terminal com `./polypod examples/selfhosted-vllm.yaml`.
