local M = {}

local chan = nil

-- probe returns true if an existing daemon is reachable, without notifying on failure.
function M.probe(socket_path)
	local ok, ch = pcall(vim.fn.sockconnect, "pipe", socket_path, { rpc = true })
	if not ok or ch == 0 then return false end
	vim.fn.chanclose(ch)
	return true
end

function M.connect(socket_path)
	local ok, result = pcall(vim.fn.sockconnect, "pipe", socket_path, { rpc = true })
	if not ok or result == 0 then
		chan = nil
		vim.notify("ohm: failed to connect to " .. socket_path, vim.log.levels.ERROR)
		return false
	end
	chan = result
	return true
end

-- Synchronous RPC request. Returns result or nil on error.
function M.request(method, params)
	if not chan then return nil end
	local ok, result = pcall(vim.fn.rpcrequest, chan, method, params)
	if not ok then
		chan = nil
		return nil
	end
	return result
end

-- Sends a synchronous attach request. Returns proxy socket path string, or nil on error.
function M.attach(params)
	local result = M.request("attach", params)
	if type(result) ~= "string" or result == "" then return nil end
	return result
end

-- Sends a fire-and-forget detach notification.
function M.detach(params)
	if not chan then return end
	pcall(vim.rpcnotify, chan, "detach", params)
end

function M.is_connected()
	return chan ~= nil
end

function M.disconnect()
	if chan then
		vim.fn.chanclose(chan)
		chan = nil
	end
end

return M
