# NVIDIA NIM no Polypod

O perfil `examples/nvidia-nim.yaml` usa a API hospedada pela NVIDIA. Ele não
instala um modelo nesta máquina e não substitui o provedor atual do serviço
até você escolher ativá-lo. A API usa uma chave gerada na sua conta; o login
no navegador sozinho não autentica o Polypod.

1. Entre no [catálogo NVIDIA Build](https://build.nvidia.com/explore/discover).
   Escolha um modelo com endpoint hospedado, por exemplo
   [Nemotron 3.5 Super VL](https://build.nvidia.com/nvidia/nemotron-3.5-super-vl-120b-a12b).
   Gere uma chave pessoal em [NGC > API Keys](https://org.ngc.nvidia.com/account/api-keys)
   com o serviço **NVIDIA Public API Endpoints** habilitado. Essa permissão
   depende das funções disponíveis na sua conta ou organização NVIDIA.
2. No `.env` privado do diretório do Polypod, configure `NVIDIA_API_KEY` com a
   chave gerada. Mantenha o arquivo fora do Git e com permissão `0600`. Não
   envie a chave na conversa nem a coloque em `config.yaml` ou no exemplo.
3. Execute `./polypod examples/nvidia-nim.yaml` para usar o perfil no CLI.
   Se escolher outro modelo no catálogo, configure `NVIDIA_NIM_MODEL` no mesmo
   `.env` com o ID exato exibido pela NVIDIA.

O perfil usa `https://integrate.api.nvidia.com/v1` e Chat Completions. O modelo
de exemplo é `nvidia/nemotron-3.5-super-vl-120b-a12b`, cujo endpoint hospedado
aceita chamadas de ferramentas e saída estruturada. Disponibilidade, cotas e
custos dependem da conta e do modelo escolhido. Os limites do endpoint hospedado
continuam definidos pela NVIDIA. Uma instalação local de NIM exige
infraestrutura própria e é um fluxo separado.

Para selecionar NIM como provedor principal do serviço, aplique os mesmos
campos de `ai` ao `config.yaml` privado, mantendo `api_key` como referência
`${NVIDIA_API_KEY:}`, e reinicie o serviço. Faça isso depois que a chave estiver
configurada. O exemplo não altera o serviço já em execução.

Fontes oficiais consultadas em 2026-11-11:

- [Guia de início da API NVIDIA](https://docs.api.nvidia.com/nim/docs/api-quickstart)
- [Endpoint de LLMs](https://docs.api.nvidia.com/nim/reference/llm-apis)
- [Modelo Nemotron 3.5 Super VL no NVIDIA Build](https://build.nvidia.com/nvidia/nemotron-3.5-super-vl-120b-a12b)
- [Referência da API do Nemotron 3.5 Super VL](https://docs.api.nvidia.com/nim/reference/nvidia-nemotron-3_5-super-vl-120b-a12b-infer)
- [Guia de chaves pessoais NGC e permissões de serviços](https://docs.nvidia.com/ngc/latest/ngc-user-guide.html#generating-a-personal-api-key)
