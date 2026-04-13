local M = {}

local chan = nil

function M.connect(socket_path)
  chan = vim.fn.sockconnect("pipe", socket_path, { rpc = true })
  if chan == 0 then
    chan = nil
    vim.notify("ohm: failed to connect to " .. socket_path, vim.log.levels.ERROR)
    return false
  end
  return true
end

function M.notify(method, params)
  if not chan then return end
  vim.rpcnotify(chan, method, params)
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
