# I2C e SPI nativos

O Polypod registra sete ferramentas por `tool.RegisterHardwareTools(reg)`. O registro não abre dispositivos. Em Linux, a execução usa as interfaces do kernel `/dev/i2c-*` e `/dev/spidev*`; em outros sistemas, as ferramentas retornam um erro indicando que o recurso exige Linux.

| Ferramenta | Argumentos principais | Resultado |
| --- | --- | --- |
| `i2c_detect` | nenhum | Barramentos disponíveis. |
| `i2c_scan` | `bus`: número decimal como string | Endereços de 7 bits detectados e endereços ocupados. |
| `i2c_read` | `bus`, `address` (8–119), `register` opcional (0–255), `length` (1–256; padrão 1) | Bytes em decimal e hexadecimal. Com `register`, usa uma transação combinada com repeated start. |
| `i2c_write` | `bus`, `address`, `register` opcional, `data` (1–256 bytes), `confirm: true` | Contagem de bytes escritos. |
| `spi_list` | nenhum | Nomes no formato `barramento.chip`, por exemplo `0.0`. |
| `spi_read` | `device`, `length` (1–4096), `speed` opcional (Hz), `mode` opcional (0–3), `bits` opcional (1–32) | Bytes recebidos; transmite zeros durante a leitura. |
| `spi_transfer` | `device`, `data` (1–4096 bytes), parâmetros SPI opcionais, `confirm: true` | Bytes recebidos e contagem enviada. |

`i2c_detect` e `spi_list` são apenas listagens. Leitura, escrita, sondagem e transferência são classificadas como ações físicas na política de permissões; mesmo uma leitura SPI transmite bytes. `confirm: true` expressa intenção explícita de escrita/transferência e não substitui as permissões do agente. As ferramentas validam parâmetros antes de abrir `/dev`, usam limites de transferência e aceitam somente dispositivos de caracteres locais, sem seguir links simbólicos.

Para uso real, o host precisa expor os dispositivos de kernel (`i2c-dev` e `spidev`), ter barramentos habilitados e conceder acesso ao usuário do serviço. O protocolo e a velocidade corretos dependem do periférico. A sondagem I2C pode ter efeitos no dispositivo; o código evita SMBus Quick Write nas faixas de EEPROM mais conhecidas (`0x30–0x37`, `0x50–0x5f`). A operação não promete segurança para todo hardware. O teste atual cobre validação, passagem de parâmetros, classificação de permissões e estruturas ABI com periféricos falsos; nenhuma operação física foi executada. Uma verificação ao vivo exige periférico conhecido e instrução específica para o teste.
