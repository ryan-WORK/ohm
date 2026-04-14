local M = {}
local client = require("ohm.client")

local defaults = {
	binary = nil,  -- auto-detected from bin/ohm in plugin dir or PATH
	socket = vim.fn.stdpath("data") .. "/ohm.sock",
	debug  = false, -- pass --debug to daemon for verbose logging
}

local job_id = nil
local config = {}
local start_daemon -- forward declaration

local function find_binary()
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

local function spawn_daemon(bin)
	local cmd = config.debug and { bin, "--debug", config.socket } or { bin, config.socket }
	job_id = vim.fn.jobstart(cmd, {
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
			-- After exit, wait then try to connect or restart.
			vim.defer_fn(function()
				start_daemon(config.binary or find_binary())
			end, 1000)
		end,
	})
end

-- start_daemon connects to an existing daemon or spawns a new one.
start_daemon = function(bin)
	if not bin then return end
	-- Probe silently first — connect only if daemon already running.
	if client.probe(config.socket) then
		client.connect(config.socket)
		return
	end
	-- Socket dead — spawn our own, then connect after it binds.
	spawn_daemon(bin)
	vim.defer_fn(function() client.connect(config.socket) end, 300)
end

-- wire_lsp overrides vim.lsp.rpc.start to transparently wrap every LSP process
-- with `ohm --client ...`. Works with lspconfig, mason, or any LSP setup.
local function wire_lsp()
	local bin = config.binary or find_binary()
	if not bin then
		return
	end

	local orig_start = vim.lsp.rpc.start

	vim.lsp.rpc.start = function(cmd, dispatchers, opts)
		-- Only wrap table cmds that aren't already ohm.
		if type(cmd) == "table" and #cmd > 0 and cmd[1] ~= bin then
			local root_dir = (opts and opts.cwd) or vim.fn.getcwd()
			-- Use binary filename as lang id (e.g. "gopls", "rust-analyzer").
			local lang = vim.fn.fnamemodify(cmd[1], ":t:r"):gsub("-", "_")
			local resolved = vim.fn.exepath(cmd[1])
			if resolved == "" then resolved = cmd[1] end

			local ohm_args = {
				bin, "--client",
				"--socket", config.socket,
				"--root", root_dir,
				"--lang", lang,
				"--",
				resolved,
			}
			for i = 2, #cmd do
				table.insert(ohm_args, cmd[i])
			end
			cmd = ohm_args
		end
		return orig_start(cmd, dispatchers, opts)
	end
end

local function wire_autocmds()
	local group = vim.api.nvim_create_augroup("ohm", { clear = true })

	vim.api.nvim_create_autocmd({ "LspDetach", "BufDelete" }, {
		group = group,
		callback = function(ev)
			if not ev.data or not ev.data.client_id then
				return
			end
			local lsp_client = vim.lsp.get_client_by_id(ev.data.client_id)
			if not lsp_client then
				return
			end
			client.detach({
				root_dir = lsp_client.config.root_dir or vim.fn.getcwd(),
				language_id = lsp_client.name,
				uri = vim.uri_from_bufnr(ev.buf),
			})
		end,
	})
end

local function create_commands()
	vim.api.nvim_create_user_command("OhmStatus", function()
		if not client.is_connected() then
			vim.notify("ohm: not connected", vim.log.levels.WARN)
			return
		end

		local servers = client.request("status", {})
		if not servers or #servers == 0 then
			vim.notify(
				string.format("ohm: connected | job=%s | socket=%s | no active servers", tostring(job_id), config.socket),
				vim.log.levels.INFO
			)
			return
		end

		local lines = { string.format("ohm: connected | job=%s", tostring(job_id)) }
		for _, s in ipairs(servers) do
			table.insert(lines, string.format(
				"  [%s] pid=%d refs=%d mem=%dMB last_response=%s",
				s.lang, s.pid, s.refs, s.memory_mb, s.last_response
			))
		end
		vim.notify(table.concat(lines, "\n"), vim.log.levels.INFO)
	end, { desc = "Show ohm daemon status" })

	vim.api.nvim_create_user_command("OhmRestart", function()
		if job_id then vim.fn.jobstop(job_id) end
		client.disconnect()
		job_id = nil
		vim.defer_fn(function()
			local bin = config.binary or find_binary()
			if not bin then
				vim.notify("ohm: binary not found", vim.log.levels.ERROR)
				return
			end
			spawn_daemon(bin)
			vim.defer_fn(function() client.connect(config.socket) end, 300)
		end, 100)
	end, { desc = "Restart ohm daemon" })
end

function M.setup(opts)
	config = vim.tbl_deep_extend("force", defaults, opts or {})

	local bin = config.binary or find_binary()
	if not bin then
		vim.notify("ohm: binary not found. Build it: go build -o bin/ohm . (in plugin dir)", vim.log.levels.ERROR)
		return
	end

	wire_lsp()
	start_daemon(bin)
	wire_autocmds()
	create_commands()
end

return M
