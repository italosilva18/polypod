# Sessões remotas no terminal

O host do Polypod oferece sessões persistentes, por padrão em HTTP no endereço local `127.0.0.1:8789`. O comando `polypod remote` cria ou entra em uma sessão e envia cada linha digitada ao agente completo. As respostas e mensagens de outros participantes aparecem no mesmo terminal.

## Preparar o host

No ambiente privado do processo que executa o Polypod, configure:

```dotenv
POLYPOD_REMOTE_SESSION_HOST=1
POLYPOD_REMOTE_SESSION_LISTEN=127.0.0.1:8789
POLYPOD_REMOTE_SESSION_TOKEN=<segredo aleatório de 32 a 512 caracteres>
```

O token deve ficar no ambiente ou em `.env` privado com permissão restrita. Use o mesmo token no cliente, sem passá-lo como argumento de linha de comando. Apenas um processo hospeda a porta; os demais clientes não precisam de `POLYPOD_REMOTE_SESSION_HOST=1`.

Sem certificado, o host aceita somente um IP de loopback. Para escutar em outro IP, configure o par TLS descrito em [TLS opcional](#tls-opcional). O túnel SSH abaixo continua válido para o HTTP de loopback.

## Conectar

Com o host ativo e `POLYPOD_REMOTE_SESSION_TOKEN` disponível no ambiente privado do cliente:

```bash
polypod remote --participant costa-notebook
```

O comando cria a primeira sessão, mostra seu ID `session_...` e grava somente esse ID em `~/.config/polypod/remote-session-costa-notebook.txt`. Na próxima execução, a mesma identidade retoma a sessão e o cursor de leitura salvo no servidor. Digite uma mensagem e pressione Enter; `/exit` fecha somente o cliente.

Se o agente usar `ask_user`, a pergunta aparecerá na mesma sessão com um ID `q_...`. Responda na linha do terminal com `resposta q_ID seu texto`, substituindo o ID pelo mostrado na pergunta. A resposta só é aceita da mesma identidade que iniciou o turno; a espera dura até cinco minutos. Pergunta e resposta ficam registradas para uma eventual retomada do turno após reinício.

Comandos úteis:

```bash
polypod remote --participant costa-notebook --list
polypod remote --participant costa-notebook --session session_0123456789abcdef0123456789abcdef
polypod remote --participant costa-notebook --new
```

Substitua o ID do exemplo pelo ID mostrado ao criar a sessão. `--session` entra em uma sessão existente e salva o ID para a próxima execução. `--new` cria outra sessão e substitui o ID salvo. Use um `--participant` diferente para cada cliente simultâneo; o cursor é associado a essa identidade. `--state-file` escolhe outro arquivo de ID, e `--url` troca o endereço do servidor.

Para acessar de outra máquina, mantenha o servidor em loopback e abra um túnel SSH:

```bash
ssh -L 8789:127.0.0.1:8789 usuario@maquina-do-polypod
polypod remote --url http://127.0.0.1:8789 --participant costa-notebook
```

O cliente aceita HTTP apenas em loopback; use HTTPS para um endereço remoto direto. O token nunca aparece na saída do comando. A interface remota é de texto, linha por linha: não envia anexos nem oferece a tela Bubbletea do CLI principal. Mensagens já entregues usam o cursor persistido do participante; se o cliente encerrar entre exibir e confirmar uma mensagem, ela pode aparecer novamente ao reconectar.

## TLS opcional

O TLS é desligado por padrão. O socket continua em HTTP claro em `127.0.0.1:8789` (ou em outro IP de loopback definido em `POLYPOD_REMOTE_SESSION_LISTEN`) e a autenticação continua sendo o bearer token.

Para ativar TLS, defina os dois caminhos no mesmo processo que tem `POLYPOD_REMOTE_SESSION_HOST=1`:

```dotenv
POLYPOD_REMOTE_SESSION_TLS_CERT=<caminho do certificado PEM>
POLYPOD_REMOTE_SESSION_TLS_KEY=<caminho da chave privada PEM>
POLYPOD_REMOTE_SESSION_LISTEN=<IP>:<porta>
```

Os dois caminhos entram juntos. Com apenas um, o host recusa iniciar. O Polypod não gera certificado, não escolhe nome e não abre rota pública. O certificado e a chave vêm do operador. O endereço de escuta é um IP e uma porta de 1 a 65535: `127.0.0.1`, `::1`, um IP da máquina, `0.0.0.0` ou `::`. Um IPv6 vai entre colchetes, como em `[::]:8789`. Nome de host no `POLYPOD_REMOTE_SESSION_LISTEN` é recusado. `0.0.0.0` e `::` escutam em todas as interfaces; o firewall fica a cargo de quem opera a máquina.

Com o par TLS definido, esse único socket fala TLS. Não há segunda porta nem redirecionamento de HTTP para HTTPS. O cliente recusa qualquer redirecionamento recebido, inclusive para o mesmo host. Um cliente no loopback também usa `https://` quando o host subiu com certificado. A versão mínima é TLS 1.2 e o protocolo de aplicação continua HTTP/1.1 com o mesmo token.

A chave é um PEM sem senha (PKCS#1 ou PKCS#8). O certificado traz a folha primeiro e, se houver, os intermediários em seguida. Os dois caminhos são absolutos. Grave a chave com permissão `0600` ou mais restrita, por exemplo `0400`, pertencente ao uid efetivo do processo que executa o host:

```bash
install -d -m 0700 /caminho/privado
install -m 0600 chave.pem /caminho/privado/remote-session.key
```

No Linux, o processo abre o certificado e a chave sem seguir links simbólicos no caminho. A chave precisa ser arquivo regular, com modo sem bits de grupo ou de outros, e com dono igual ao uid efetivo. O diretório que contém a chave não pode ser gravável por grupo nem por outros, mesmo com sticky bit. Cada diretório acima dele também não pode, exceto um ancestral com sticky bit, como um `/tmp` que só guarda um subdiretório privado. Cada PEM de certificado, chave ou CA fica entre 1 byte e 1 MiB. O certificado pode ser legível por outros. Mantenha a chave fora do Git, da linha de comando e das linhas `Environment=` da unidade systemd. Coloque os caminhos e o token em arquivo privado no modo `0600`, referenciado por `EnvironmentFile=`, ou em credencial do systemd. A unidade instalada não recebe essas linhas. Bits de permissão não refletem ACL: se o sistema de arquivos usa ACL, confira o acesso com as ferramentas do sistema. Fora do Linux, a opção TLS falha de forma explícita; o modo padrão em loopback continua disponível.

O certificado precisa ter um `subjectAltName` igual ao host que o cliente disca. URL com IP exige SAN `iPAddress`. URL com nome exige SAN `dNSName`. O nome ou o IP é o que o operador já usa; este guia não fixa um domínio. A verificação do cliente usa esse host da URL e permanece ligada.

No cliente, omita `POLYPOD_REMOTE_SESSION_TLS_CA` para usar o cliente padrão e as raízes do sistema. Para uma CA própria ou um certificado autoassinado, aponte a variável para o PEM dessa CA ou para o próprio certificado do servidor. Esse arquivo passa a ser a única âncora desta conexão; as raízes do sistema não entram junto.

```dotenv
POLYPOD_REMOTE_SESSION_URL=https://<host-do-certificado>:<porta>
POLYPOD_REMOTE_SESSION_TLS_CA=<caminho do PEM de confiança>
POLYPOD_REMOTE_SESSION_TOKEN=<o mesmo segredo do host>
```

O caminho do arquivo de confiança deve ser absoluto. No Linux, o arquivo é aberto sem seguir links simbólicos no caminho; ele não pode ser gravável por grupo nem por outros, e os diretórios ancestrais obedecem à mesma regra da chave. Também não pode conter a chave privada. Arquivo vazio, maior que 1 MiB ou sem bloco `CERTIFICATE` faz o cliente encerrar. A verificação permanece ligada. O host da URL precisa coincidir com um SAN: nome DNS, inclusive `localhost`, exige `dNSName`; URL com IP exige `iPAddress`. O common name não é consultado. O placeholder da URL é o nome ou o IP que já está no certificado.

Para girar o certificado, grave a nova chave com `0600` num diretório privado, publique o novo certificado com o SAN certo, substitua os arquivos e reinicie o processo do host. A leitura ocorre uma vez na partida; o processo antigo segue com o par anterior até sair. Se a âncora de confiança mudou, atualize `POLYPOD_REMOTE_SESSION_TLS_CA` nos clientes antes de reconectar. Girar o token é independente: altere `POLYPOD_REMOTE_SESSION_TOKEN` no host e nos clientes e reinicie o host.
