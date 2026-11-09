# Despertar por recursos MCP

O Polypod pode iniciar um turno completo do agente quando um servidor MCP avisa que um recurso assinado mudou. O servidor precisa declarar a capacidade de assinatura de recursos. A assinatura é explícita: o agente usa `mcp_resource_subscribe` com o nome do servidor e a URI concreta. `mcp_resource_unsubscribe` encerra a assinatura, e `list_mcp_resource_updates` mostra assinaturas e notificações recentes.

Exemplo de pedido ao agente: “Assine o recurso `docs://projeto/status` do servidor `meu-mcp` e me avise quando ele mudar”. Depois da confirmação do servidor, a assinatura fica em `mcp-subscriptions.json` no diretório privado de dados. Ela é restaurada quando o processo reconecta ou reinicia.

Uma notificação aceita cria uma tarefa na fila durável. Essa tarefa relê o recurso com `read_mcp_resource`, avalia a mudança e envia o resultado ao proprietário pelos canais de notificação configurados. Notificações repetidas da mesma URI são agrupadas por 30 segundos; uma falha ao entregar à fila é tentada novamente. O MCP não fornece um ID único nem o conteúdo da alteração na notificação, então o agente lê o estado atual do recurso. Se o processo encerrar antes de a fila aceitar a tarefa, a notificação poderá se perder.

O CLI e o serviço root compartilham o arquivo de assinaturas. Cada processo mantém sua conexão MCP; uma assinatura feita em um processo passa a valer para a conexão do outro após ele reiniciar e restaurar o arquivo. Para receber avisos continuamente, mantenha o servidor MCP conectado ao processo do serviço.
