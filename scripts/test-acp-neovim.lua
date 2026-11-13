-- Run inside Neovim with Agentic.nvim on runtimepath. The caller supplies
-- POLYPOD_ACP_BINARY, POLYPOD_ACP_CONFIG, and POLYPOD_ACP_RESULT.
-- Optional env vars:
--   POLYPOD_ACP_PROMPT              custom prompt text
--   POLYPOD_ACP_REQUIRE_TOOL=1      require >=1 tool call
--   POLYPOD_ACP_REQUIRE_DIFF=1      require >=1 structured ACP diff rendered with Agentic.nvim extmarks
--   POLYPOD_ACP_REQUIRE_MCP_TOOL    tool name substring that must be invoked (e.g. "mcp__editor__")
--   POLYPOD_ACP_MCP_SERVERS_JSON    JSON array of ACP mcpServers passed in session/new and session/load
local binary = assert(vim.env.POLYPOD_ACP_BINARY, "POLYPOD_ACP_BINARY missing")
local config = assert(vim.env.POLYPOD_ACP_CONFIG, "POLYPOD_ACP_CONFIG missing")
local output = assert(vim.env.POLYPOD_ACP_RESULT, "POLYPOD_ACP_RESULT missing")
local prompt_text = vim.env.POLYPOD_ACP_PROMPT
  or "Responda em português, em uma frase curta: qual é a finalidade de um teste de integração?"
local require_tool = vim.env.POLYPOD_ACP_REQUIRE_TOOL == "1"
local require_diff = vim.env.POLYPOD_ACP_REQUIRE_DIFF == "1"
local require_mcp_tool = vim.env.POLYPOD_ACP_REQUIRE_MCP_TOOL or ""
local mcp_servers = {}
if vim.env.POLYPOD_ACP_MCP_SERVERS_JSON and vim.env.POLYPOD_ACP_MCP_SERVERS_JSON ~= "" then
  mcp_servers = vim.json.decode(vim.env.POLYPOD_ACP_MCP_SERVERS_JSON)
end

local cwd = vim.fn.getcwd()
local Theme = require("agentic.theme")
Theme.setup()
local ACPClient = require("agentic.acp.acp_client")
local MessageWriter = require("agentic.ui.message_writer")
local ns_diff = vim.api.nvim_create_namespace("agentic_diff_highlights")

local chat_buf = vim.api.nvim_create_buf(false, true)
vim.api.nvim_win_set_buf(0, chat_buf)
local writer = MessageWriter:new(chat_buf)
writer:set_provider_name("Poly")

local replay_buf = vim.api.nvim_create_buf(false, true)
local replay_writer = MessageWriter:new(replay_buf)
replay_writer:set_provider_name("Poly")

local report = {
  ready = false,
  created = false,
  prompt = false,
  loaded = false,
  mcp_capabilities = nil,
  mcp_servers_count = #mcp_servers,
  chunks = 0,
  replay_chunks = 0,
  tools = 0,
  tool_names = {},
  mcp_tool_matched = false,
  diff_blocks = 0,
  diff_paths = {},
  diff_extmarks = 0,
  rendered_lines = 0,
  replay_diff_blocks = 0,
  replay_diff_extmarks = 0,
  errors = {},
}

local done = false
local function fail(message)
  table.insert(report.errors, tostring(message))
  done = true
end

local function record_tool_msg(msg, is_replay)
  if type(msg) ~= "table" then
    return
  end
  if not is_replay and type(msg.argument) == "string" and msg.argument ~= "" then
    table.insert(report.tool_names, msg.argument)
    if require_mcp_tool ~= "" and string.find(msg.argument, require_mcp_tool, 1, true) then
      report.mcp_tool_matched = true
    end
  end
  if msg.diff ~= nil then
    if is_replay then
      report.replay_diff_blocks = report.replay_diff_blocks + 1
    else
      report.diff_blocks = report.diff_blocks + 1
      if msg.file_path then
        table.insert(report.diff_paths, msg.file_path)
      end
    end
  end
end

local function start_new_session(client, handlers, cb)
  if #mcp_servers == 0 then
    client:create_session(handlers, cb)
    return
  end
  client:_send_request("session/new", {
    cwd = cwd,
    mcpServers = mcp_servers,
  }, function(result, err)
    if err or not result then
      cb(nil, err or { message = "missing session/new result" })
      return
    end
    if result.sessionId then
      client:_subscribe(result.sessionId, handlers)
    end
    cb(result, nil)
  end)
end

local first
first = ACPClient:new({ name = "Poly", command = binary, args = { "acp", config } }, function(client)
  vim.schedule(function()
    report.ready = true
    if client.agent_capabilities then
      report.mcp_capabilities = client.agent_capabilities.mcpCapabilities
    end
    local handlers = {
      on_session_update = function(update)
        if update.sessionUpdate == "agent_message_chunk" then
          report.chunks = report.chunks + 1
        end
        pcall(function()
          writer:write_message_chunk(update)
        end)
      end,
      on_tool_call = function(msg)
        report.tools = report.tools + 1
        record_tool_msg(msg, false)
        pcall(function()
          writer:write_tool_call_block(msg)
        end)
      end,
      on_tool_call_update = function(msg)
        record_tool_msg(msg, false)
        pcall(function()
          writer:update_tool_call_block(msg)
        end)
      end,
      on_error = function(err)
        fail(vim.inspect(err))
      end,
    }

    start_new_session(client, handlers, function(created, err)
      if err then
        fail(vim.inspect(err))
        return
      end
      report.created = created.sessionId ~= nil
      report.session_id = created.sessionId
      client:send_prompt(
        created.sessionId,
        { { type = "text", text = prompt_text } },
        function(answer, prompt_err)
          vim.schedule(function()
            if prompt_err then
              fail(vim.inspect(prompt_err))
              return
            end
            report.prompt = answer.stopReason == "end_turn"
            report.rendered_lines = vim.api.nvim_buf_line_count(chat_buf)
            report.diff_extmarks = #vim.api.nvim_buf_get_extmarks(chat_buf, ns_diff, 0, -1, {})
            local buf_lines = vim.api.nvim_buf_get_lines(chat_buf, 0, -1, false)
            local sample = {}
            for i = 1, math.min(#buf_lines, 40) do
              table.insert(sample, buf_lines[i])
            end
            report.rendered_preview = sample

            client:stop()
            local second
            second = ACPClient:new({ name = "Poly", command = binary, args = { "acp", config } }, function(reconnected)
              reconnected:load_session(created.sessionId, cwd, mcp_servers, {
                on_session_update = function(update)
                  if update.sessionUpdate == "agent_message_chunk" then
                    report.replay_chunks = report.replay_chunks + 1
                  end
                  pcall(function()
                    replay_writer:write_message_chunk(update)
                  end)
                end,
                on_tool_call = function(msg)
                  record_tool_msg(msg, true)
                  pcall(function()
                    replay_writer:write_tool_call_block(msg)
                  end)
                end,
                on_tool_call_update = function(msg)
                  record_tool_msg(msg, true)
                  pcall(function()
                    replay_writer:update_tool_call_block(msg)
                  end)
                end,
              }, function(_, load_err)
                if load_err then
                  fail(vim.inspect(load_err))
                  return
                end
                report.loaded = true
                vim.defer_fn(function()
                  report.replay_diff_extmarks = #vim.api.nvim_buf_get_extmarks(replay_buf, ns_diff, 0, -1, {})
                  reconnected:stop()
                  done = true
                end, 300)
              end)
            end)
          end)
        end
      )
    end)
  end)
end)

local timeout_ms = tonumber(vim.env.POLYPOD_ACP_TIMEOUT_MS or "") or 240000
if not vim.wait(timeout_ms, function()
  return done
end, 25) then
  fail("timeout")
end

if vim.api.nvim_buf_is_valid(chat_buf) then
  report.rendered_lines = vim.api.nvim_buf_line_count(chat_buf)
  report.diff_extmarks = #vim.api.nvim_buf_get_extmarks(chat_buf, ns_diff, 0, -1, {})
  local buf_lines = vim.api.nvim_buf_get_lines(chat_buf, 0, -1, false)
  local sample = {}
  for i = 1, math.min(#buf_lines, 50) do
    table.insert(sample, buf_lines[i])
  end
  report.rendered_preview = sample
end
if vim.api.nvim_buf_is_valid(replay_buf) then
  report.replay_diff_extmarks = #vim.api.nvim_buf_get_extmarks(replay_buf, ns_diff, 0, -1, {})
end

vim.fn.writefile({ vim.json.encode(report) }, output)

local ok = report.ready
  and report.created
  and report.prompt
  and report.loaded
  and report.chunks > 0
  and report.replay_chunks > 0
  and (not require_tool or report.tools > 0)
  and (not require_diff or (report.diff_blocks > 0 and report.diff_extmarks > 0))
  and (require_mcp_tool == "" or report.mcp_tool_matched)

if not ok then
  vim.cmd("cquit 1")
else
  vim.cmd("qa!")
end
