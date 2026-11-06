# USB, serial e gravação de firmware

`tool.RegisterSerialHardwareTools(reg)` registra as cinco ferramentas do
agente padrão: `list_usb_devices`, `list_serial_ports`, `serial_send`,
`serial_exchange` e `flash_firmware`.

No Linux, a listagem USB lê `/sys/bus/usb/devices`; a listagem serial procura
portas em `/dev` e aliases em `/dev/serial/by-id` e `by-path`. Listar não abre
dispositivos. `serial_send` e `serial_exchange` abrem apenas caminhos seriais
reconhecidos que apontem para dispositivos de caracteres. A conexão usa 8N1,
baud selecionado entre 9600 e 921600, timeout de 1 a 30000 ms e no máximo
4096 bytes por envio ou leitura. `serial_exchange` envia e recebe na mesma
abertura; a resposta inclui hexadecimal, texto UTF-8 quando válido e indicação
de timeout. Em outros sistemas, essas operações informam indisponibilidade.

`flash_firmware` tem `action=status` para informar se os backends estão
instalados. `action=flash` exige `confirm=true`, `backend=esptool|avrdude`,
porta explícita e firmware regular de até 64 MiB. `esptool` aceita `.bin`, chip
validado e offset múltiplo de 4096. `avrdude` aceita `.hex`/`.bin`, `part` e
`programmer` validados. O processo tem timeout máximo de 10 minutos e saída
limitada. Os comandos usam `exec.CommandContext` com argumentos separados;
nenhum texto é passado a um shell. A sintaxe segue a documentação oficial de
[esptool](https://docs.espressif.com/projects/esptool/en/latest/esp32/esptool/basic-commands.html)
e [AVRDUDE](https://www.nongnu.org/avrdude/user-manual/avrdude_3.html).

Os testes usam fixtures de sysfs, transporte e executor falsos, além de um
pseudoterminal Linux. Nenhum teste lê ou grava hardware físico. As permissões
normais do orquestrador continuam aplicáveis; envio serial e gravação são
classificados como operações de escrita.
