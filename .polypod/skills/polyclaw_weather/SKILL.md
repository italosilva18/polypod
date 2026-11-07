---
name: polyclaw_weather
description: Consulte clima atual e previsão verificando cidade, região, país e horário dos dados.
---

# Clima

Adaptação da skill Weather do PolyClaw. Para uma cidade informada pelo usuário, confirme a localidade encontrada antes de apresentar a previsão. `wttr.in/<cidade>?format=j1` fornece uma consulta direta; Open-Meteo oferece geocodificação e dados estruturados. Use `web_fetch` ou `bash` com `curl` quando disponíveis. Codifique corretamente caracteres especiais na URL.

Mostre local, região/país, horário da observação e unidade. Se houver cidades homônimas, esclareça qual foi usada. Não apresente dados antigos como atuais. Fonte e exemplos: `third_party/polyclaw/skills/weather/`; licença MIT: `third_party/polyclaw/LICENSE`.
