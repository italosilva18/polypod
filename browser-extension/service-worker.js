const PAIR_PATTERN = /^ws:\/\/127\.0\.0\.1:\d+\/[a-f0-9]{64}\/extension$/;
const RECONNECT_ALARM = "polypod-reconnect";

let socket = null;
let socketURL = "";
let heartbeat = null;
let retryTimer = null;
const attachedTabs = new Set();

function connected() {
  return socket !== null && socket.readyState === WebSocket.OPEN;
}

async function detachAll() {
  const tabs = [...attachedTabs];
  attachedTabs.clear();
  await Promise.allSettled(tabs.map(tabId => chrome.debugger.detach({tabId})));
}

function clearConnection() {
  if (heartbeat !== null) {
    clearInterval(heartbeat);
    heartbeat = null;
  }
  if (retryTimer !== null) {
    clearTimeout(retryTimer);
    retryTimer = null;
  }
  if (socket !== null) {
    socket.onclose = null;
    socket.close();
    socket = null;
  }
  void detachAll();
}

function scheduleReconnect() {
  if (retryTimer !== null || !socketURL) return;
  retryTimer = setTimeout(() => {
    retryTimer = null;
    void connectSaved();
  }, 5000);
}

function connect(url) {
  if (!PAIR_PATTERN.test(url)) throw new Error("Use a URL ws://127.0.0.1 enviada pelo Polypod.");
  if (socketURL === url && (connected() || socket?.readyState === WebSocket.CONNECTING)) return;
  clearConnection();
  socketURL = url;
  const next = new WebSocket(url);
  socket = next;
  next.onopen = () => {
    if (socket !== next) return;
    heartbeat = setInterval(() => {
      if (connected()) socket.send(JSON.stringify({id: 0, op: "heartbeat"}));
    }, 20000);
  };
  next.onmessage = event => {
    if (socket !== next) return;
    void handleMessage(event.data, next);
  };
  next.onclose = () => {
    if (socket !== next) return;
    socket = null;
    if (heartbeat !== null) clearInterval(heartbeat);
    heartbeat = null;
    void detachAll();
    scheduleReconnect();
  };
}

async function connectSaved() {
  const {pairURL} = await chrome.storage.local.get("pairURL");
  if (typeof pairURL === "string" && PAIR_PATTERN.test(pairURL)) connect(pairURL);
}

function tabID(value) {
  if (typeof value !== "string" || !/^\d+$/.test(value)) throw new Error("tab_id inválido");
  const id = Number(value);
  if (!Number.isSafeInteger(id) || id <= 0) throw new Error("tab_id inválido");
  return id;
}

function tabInfo(tab) {
  return {id: String(tab.id), title: tab.title || "", url: tab.url || "", type: "page"};
}

async function cdpCommand(request) {
  const id = tabID(request.tab_id);
  if (!["Runtime.evaluate", "Page.captureScreenshot", "Page.navigate"].includes(request.method)) {
    throw new Error("método CDP indisponível");
  }
  const target = {tabId: id};
  if (!attachedTabs.has(id)) {
    await chrome.debugger.attach(target, "1.3");
    attachedTabs.add(id);
  }
  return await chrome.debugger.sendCommand(target, request.method, request.params || {}) || {};
}

async function runRequest(request) {
  switch (request.op) {
    case "tabs": {
      const tabs = await chrome.tabs.query({});
      return tabs.filter(tab => Number.isSafeInteger(tab.id)).map(tabInfo);
    }
    case "new":
      return tabInfo(await chrome.tabs.create({url: "about:blank", active: false}));
    case "activate":
      await chrome.tabs.update(tabID(request.tab_id), {active: true});
      return {};
    case "close": {
      const id = tabID(request.tab_id);
      attachedTabs.delete(id);
      await chrome.tabs.remove(id);
      return {};
    }
    case "cdp":
      return await cdpCommand(request);
    default:
      throw new Error("operação desconhecida");
  }
}

async function handleMessage(raw, target) {
  let request;
  try {
    request = JSON.parse(raw);
  } catch {
    return;
  }
  if (!Number.isSafeInteger(request.id) || request.id <= 0) return;
  let reply;
  try {
    reply = {id: request.id, result: await runRequest(request)};
  } catch (error) {
    reply = {id: request.id, error: String(error?.message || error)};
  }
  if (socket === target && target.readyState === WebSocket.OPEN) target.send(JSON.stringify(reply));
}

chrome.debugger.onDetach.addListener(source => {
  if (source.tabId !== undefined) attachedTabs.delete(source.tabId);
});

chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  (async () => {
    if (message?.type === "pair") {
      const url = String(message.url || "").trim();
      if (!PAIR_PATTERN.test(url)) throw new Error("URL de pareamento inválida");
      await chrome.storage.local.set({pairURL: url});
      connect(url);
      return {ok: true, connected: connected()};
    }
    if (message?.type === "disconnect") {
      socketURL = "";
      clearConnection();
      await chrome.storage.local.remove("pairURL");
      return {ok: true, connected: false};
    }
    if (message?.type === "status") {
      return {ok: true, connected: connected(), pairing: Boolean(socketURL)};
    }
    return {ok: false, error: "comando desconhecido"};
  })().then(respond, error => respond({ok: false, error: String(error?.message || error)}));
  return true;
});

chrome.runtime.onInstalled.addListener(() => {
  void chrome.alarms.create(RECONNECT_ALARM, {periodInMinutes: 1});
  void connectSaved();
});
chrome.runtime.onStartup.addListener(() => { void connectSaved(); });
chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name === RECONNECT_ALARM && !connected()) void connectSaved();
});
void connectSaved();
