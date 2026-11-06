---
name: polyclaw_hardware
description: Descubra e opere dispositivos USB e seriais conectados com as ferramentas nativas do Polypod.
---

# Hardware local

Adaptação da skill `hardware` do PolyClaw. Comece com `list_usb_devices` e `list_serial_ports` para identificar o dispositivo. Para comunicação serial, escolha porta, velocidade e protocolo conforme a documentação do hardware; use `serial_exchange` quando esperar resposta e confira bytes e checksum. `flash_firmware` grava firmware e deve usar arquivo, placa e porta explicitamente identificados, com leitura de retorno após gravar. Não suponha que um dispositivo conectado seja o alvo correto apenas por nome similar.

Para I2C, use `i2c_detect`/`i2c_scan` para descobrir barramento e endereço, depois `i2c_read`/`i2c_write` com registrador e comprimento confirmados. Para SPI, use `spi_list`, `spi_read` e `spi_transfer` com modo, velocidade e chip select corretos. Confira o datasheet; uma resposta de barramento não prova que a placa escolhida era a certa.

As referências de pinos e dispositivos originais estão em `third_party/polyclaw/skills/hardware/references/`. O código I2C/SPI foi testado com backends falsos; operações físicas precisam de periférico e ensaio ao vivo. Licença MIT em `third_party/polyclaw/LICENSE`.
