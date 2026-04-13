local M = {}
local client = require("ohm.client")

local defaults = {
  binary = nil, -- auto-detected from plugin dir or PATH
  socket = vim.fn.stdpath("data") .. "/ohm.sock",
}

local job_id = nil
local config = {}

local function find_binary()
  -- look for compiled binary next to the plugin
  local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h:h")
  local bin = plugin_dir .. "/bin/ohm"
  if vim.fn.executable(bin) == 1 then
    return bin
  end
  if vim.fn.executable("ohm") == 1 then
    return "ohm"
  end
  return nil
end

local function start_daemon(bin)
  job_id = vim.fn.jobstart({ bin, config.socket }, {
    on_stderr = function(_, data)
      for _, line in ipairs(data) do
        if line ~= "" then
          vim.notify("ohm: " .. line, vim.log.levels.WARN)
        end
      end
    end,
    on_exit = function(_, code)
      if code ~= 0 then
        vim.notify("ohm: daemon exited with code " .. code, vim.log.levels.ERROR)
      end
      client.disconnect()
      job_id = nil
    end,
  })
end

local function wire_autocmds()
  local group = vim.api.nvim_create_augroup("ohm", { clear = true })

  vim.api.nvim_create_autocmd("LspAttach", {
    group = group,
    callback = function(ev)
      local lsp_client = vim.lsp.get_client_by_id(ev.data.client_id)
      if not lsp_client then return end
      local cmd = lsp_client.config.cmd or {}
      client.notify("attach", {
        root_dir = lsp_client.config.root_dir or vim.fn.getcwd(),
        language_id = (lsp_client.config.filetypes or {})[1] or "unknown",
        command = cmd[1] or "",
        args = vim.list_slice(cmd, 2),
      })
    end,
  })

  vim.api.nvim_create_autocmd({ "LspDetach", "BufDelete" }, {
    group = group,
    callback = function(ev)
      if not ev.data or not ev.data.client_id then return end
      local lsp_client = vim.lsp.get_client_by_id(ev.data.client_id)
      if not lsp_client then return end
      client.notify("detach", {
        root_dir = lsp_client.config.root_dir or vim.fn.getcwd(),
        language_id = (lsp_client.config.filetypes or {})[1] or "unknown",
        uri = vim.uri_from_bufnr(ev.buf),
      })
    end,
  })
end

local function create_commands()
  vim.api.nvim_create_user_command("OhmStatus", function()
    if client.is_connected() then
      vim.notify(string.format("ohm: connected | job=%s | socket=%s", tostring(job_id), config.socket),
        vim.log.levels.INFO)
    else
      vim.notify("ohm: not connected", vim.log.levels.WARN)
    end
  end, { desc = "Show ohm daemon status" })

  vim.api.nvim_create_user_command("OhmRestart", function()
    if job_id then vim.fn.jobstop(job_id) end
    client.disconnect()
    vim.defer_fn(function()
      local bin = config.binary or find_binary()
      if not bin then
        vim.notify("ohm: binary not found", vim.log.levels.ERROR)
        return
      end
      start_daemon(bin)
      vim.defer_fn(function() client.connect(config.socket) end, 150)
    end, 100)
  end, { desc = "Restart ohm daemon" })
end

function M.setup(opts)
  config = vim.tbl_deep_extend("force", defaults, opts or {})

  local bin = config.binary or find_binary()
  if not bin then
    vim.notify(
      "ohm: binary not found. Build it: go build -o bin/ohm . (in plugin dir)",
      vim.log.levels.ERROR
    )
    return
  end

  start_daemon(bin)
  -- give daemon time to bind socket before connecting
  vim.defer_fn(function() client.connect(config.socket) end, 150)

  wire_autocmds()
  create_commands()
end

return M
