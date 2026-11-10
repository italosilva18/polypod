const input = document.getElementById("pairURL");
const status = document.getElementById("status");

async function refresh() {
  const {pairURL} = await chrome.storage.local.get("pairURL");
  if (typeof pairURL === "string") input.value = pairURL;
  const result = await chrome.runtime.sendMessage({type: "status"});
  status.textContent = result.connected ? "Conectado ao Polypod" : result.pairing ? "Aguardando o Polypod" : "Desconectado";
}

document.getElementById("connect").addEventListener("click", async () => {
  const result = await chrome.runtime.sendMessage({type: "pair", url: input.value.trim()});
  status.textContent = result.ok ? "Conectando..." : result.error;
  if (result.ok) setTimeout(refresh, 400);
});

document.getElementById("disconnect").addEventListener("click", async () => {
  await chrome.runtime.sendMessage({type: "disconnect"});
  input.value = "";
  await refresh();
});

void refresh();
