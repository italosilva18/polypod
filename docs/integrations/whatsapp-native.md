# WhatsApp nativo no Polypod

Implementação opt-in em `internal/whatsappgateway`, sem conexão automática durante
construção/testes. A auditoria da configuração local encontrou `whatsapp_native`
ativado no PolyClaw, sem URL de bridge; sua sessão fica em
`~/.picoclaw/workspace/whatsapp/store.db`. O adaptador legado WebSocket não foi
portado. O corte operacional da instalação local está registrado no comparativo
Polypod/PolyClaw; este documento descreve o contrato do adaptador.

O código usa diretamente whatsmeow na versão fixada no `go.mod`
(`v0.0.0-20261102152715-54038813c47f`), protobuf e SQLite.
PolyClaw é MIT (Copyright 2026 PicoClaw contributors); a implementação foi escrita
para o domínio Polypod, sem importar seu runtime/bus. whatsmeow é MPL-2.0, conforme
LICENSE da dependência; conservar seus avisos ao distribuir código da biblioteca.

## Contrato e integração

`New(store, driver, allowedChats, allowedSenders, wake)` exige ambas as listas,
sem wildcard. `Native` implementa `Driver.Send`; `Native.Run(ctx, gateway)` é
bloqueante e só deve iniciar depois da preparação explícita da sessão.
`Native.Preflight()` valida diretório/arquivo sem abrir banco. Pairing/schema são
validados em `Run`. Não há login por QR automático.

O runtime em `main.go` usa a seção `whatsapp` do `config.yaml`: `enabled`,
`session_dir`, `allowed_chats`, `allowed_senders` e o `owner_chat` opcional. Com o canal habilitado, uma
falha inicial gera tentativas de reconexão com espera progressiva de 5 a 60
segundos. `Native` atualiza `data/conversations/whatsapp-status.json` a cada
15 segundos com `connected`, `retrying` ou `stopped`; `polypod doctor` sinaliza
estado ausente ou antigo. O estado de socket não substitui um teste real de
recebimento e resposta. `config.Load` rejeita JIDs inválidos antes do início.

Se `owner_chat` estiver preenchido, ele deve ser um JID exato de usuário já
presente em `allowed_chats`. O skill `whatsapp_notify` recebe `message`, grava
uma saída durável para esse chat e acorda o processador. O registro do skill
não envia mensagem; a entrega segue o retry/backoff da outbox.

`ask_user` em um turno WhatsApp usa o chat e o remetente exatos da entrada que
originou a pergunta. O Polypod registra a pergunta em
`data/conversations/whatsapp-questions.json` (0600) e a saída com ID estável
na outbox antes do envio. A resposta pode citar a pergunta no WhatsApp ou usar
`resposta q_ID texto`; somente o mesmo chat e remetente resolvem a espera.
Essa resposta é persistida antes da confirmação ao WhatsApp e não vira um novo
prompt do agente. Após reinício, uma nova tentativa do mesmo turno recupera a
pergunta ou a resposta registrada, sem reenviar uma pergunta já entregue. A
espera dura até cinco minutos; perguntas vencidas não são enviadas mais tarde
pelo worker da outbox. Uma resposta tardia a uma pergunta ainda conhecida é
consumida sem iniciar outra tarefa; registros de perguntas são retidos por
sete dias após o vencimento.

Perguntas de rotinas sem chat de origem usam `owner_chat` se o Telegram não
estiver habilitado. Para o autochat, configure `owner_chat` com o JID exato
que aparece como **chat e remetente** nas mensagens recebidas e inclua-o nas
duas allowlists. Algumas contas recebem autochat como `@lid` mesmo que o
número de telefone use `@s.whatsapp.net`; não misture os dois identificadores
na correlação. Se ambos os canais estiverem ativos, perguntas vindas do
WhatsApp permanecem no WhatsApp; perguntas de rotina usam Telegram primeiro.
O estado da pergunta contém a resposta em arquivo privado do serviço, assim
como a inbox contém o texto recebido; não há conteúdo de pergunta/resposta nos
logs de diagnóstico.

`Gateway.Receive` persiste envelope JSON na inbox com ID externo chat+message ID.
`Decode` recupera texto, remetente, referência de resposta, MIME e caminho de mídia;
o processamento deve alimentar o loop existente, sem conceder permissões extras.
`Gateway.Deliver` atende apenas outbox WhatsApp com chat permitido. O worker
`humanloop.Processor` fornece retry/backoff e recuperação após reinício. Uma
sessão desconectada devolve a saída à fila sem gastar o limite de tentativas,
com espera de 15 segundos a 5 minutos; a autenticação adianta as saídas
pendentes. Erros de outra natureza ainda contam como tentativas.
O envio repete o mesmo ID de mensagem em toda tentativa. Isso reduz duplicações,
mas não comprova entrega exatamente uma vez nem leitura pelo destinatário.
Respostas citam a entrada enquanto ela estiver retida no histórico da inbox.

O driver habilita `SynchronousAck` e `EnableDecryptedEventBuffer`; o handler retorna
sucesso somente depois de persistir a inbox. Falhas deixam a confirmação pendente.
Mensagens `FromMe` do próprio telefone são aceitas quando chat e remetente
(sem sufixo de dispositivo) correspondem exatamente às listas permitidas.
Ecos de saídas do Polypod são ignorados pelo ID determinístico calculado a
partir da mensagem durável na outbox. Esse reconhecimento depende da retenção
da saída no histórico da inbox; uma saída muito antiga removida pelo limite de
histórico deixa de ser reconhecível. A deduplicação mantém todas as chaves de
mensagens ainda retidas e limita o total a `max(128, 10 × maxHistory)` quando
as mensagens ativas permitem. Ao ultrapassar esse teto, descarta primeiro as
chaves mais antigas de mensagens removidas. Um ID descartado pode voltar a ser
aceito; é uma garantia limitada e explícita. Candidatos rejeitados geram diagnóstico
apenas em nível debug; as entradas aceitas são registradas em info sem JIDs,
IDs ou conteúdo.
Remetentes/chats fora da lista são ignorados antes do download.
Imagens, áudio, vídeo, documentos e stickers recebidos são salvos em arquivos locais
com limite de 16 MiB, nomes derivados por hash e permissão privada. Quando o
provedor principal oferece visão, `MediaInterpreter` valida a imagem, reduz
dimensões, pede uma descrição sem ferramentas e entrega somente o texto rotulado
ao agente. Não há transcrição de áudio configurada, OCR dedicado nem upload de
anexos de saída neste incremento. O envio atual é texto/citação.
Arquivos de mídia com mais de sete dias são removidos na inicialização e a
cada 24 horas, desde que não estejam referenciados por uma mensagem de entrada
pendente ou em processamento. A limpeza só atua no diretório isolado do
Polypod e rejeita links simbólicos e caminhos fora dele.
A biblioteca pode alocar o download inteiro antes da checagem final de tamanho;
o tamanho anunciado é conferido antes de baixar. Há timeout de dois minutos.

## Migração da sessão

1. Manter o gateway PolyClaw atual ativo até escolher a janela de corte.
2. Durante o corte, encerrar o único cliente que possui a sessão; criar backup
   SQLite consistente incluindo WAL, preferindo a API SQLite backup após parada.
3. Colocar a cópia em diretório privado separado do Polypod. Nunca abrir a mesma
   sessão (nem uma cópia da mesma identidade) simultaneamente nos dois runtimes.
4. Configurar allowlists explícitas e o diretório isolado; executar preflight,
   iniciar Polypod, verificar `doctor` e confirmar recebimento e resposta antes
   de abandonar o rollback.
5. Se precisar voltar, parar Polypod antes de reativar PolyClaw. Não restaurar
   cegamente um banco antigo sobre o estado criptográfico mais recente.

O adaptador bloqueia os diretórios padrão `.picoclaw/workspace/whatsapp` e
`.polyclaw/workspace/whatsapp`, inclusive symlinks, independentemente do usuário
que executa o serviço. Um flock impede dois Polypods na mesma pasta; não detecta
outro programa que ignore esse lock ou uma cópia em diretório diferente.
A instalação local usa uma cópia isolada em `/var/lib/polypod/whatsapp`, com
backup privado da sessão anterior. O PolyClaw permanece parado para essa
identidade. A versão atual do Polypod autenticou e concluiu um turno de texto
no autochat, confirmado no telefone em 6 de novembro de 2026.

Testes locais usam driver falso e diretórios temporários: replay após crash,
correlação, ID estável no retry, allowlists, cancelamento e proteção de sessão.
O turno real acima comprova esse caso de texto; outros chats, anexos e falhas
de rede ainda exigem testes próprios.

## Reprocessar uma entrada com falha

Depois de corrigir a causa de uma falha terminal, o operador pode recolocar **um
ID interno específico** na fila. O comando é somente leitura por padrão; `-apply`
faz a alteração e recusa executar se `polypod-root.service` estiver ativo. Pare
o serviço e mantenha uma cópia privada de `humanloop.json` antes de aplicar:

```bash
go build -o /tmp/polypod-humanloop-replay ./internal/humanloop/cmd
sudo systemctl stop polypod-root.service
sudo /tmp/polypod-humanloop-replay -store 'data/conversations/humanloop.json' -id 'msg_ID_INTERNO' -channel whatsapp
sudo /tmp/polypod-humanloop-replay -store 'data/conversations/humanloop.json' -id 'msg_ID_INTERNO' -channel whatsapp -apply
sudo systemctl start polypod-root.service
```

O dry-run mostra canal, número de tentativas, hora da falha e um fingerprint do
ID; não mostra texto, destinatário nem ID externo. A aplicação preserva o ID
interno, a chave de deduplicação e o histórico de tentativas/erro no próprio
arquivo privado, reiniciando apenas o contador da nova execução. O comando
recusa entradas concluídas, saídas e entradas com resposta pronta ou já
enfileirada. Use um ID por execução para não reprocessar mensagens sem revisão.
