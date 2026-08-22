local dap = require "dap"

local adapter_type = assert(vim.env.MDS_DAP_TYPE)
local adapter = vim.env.MDS_DAP_ADAPTER
local root = assert(vim.env.MDS_DAP_ROOT)
local source = assert(vim.env.MDS_DAP_SOURCE)
local line = assert(tonumber(vim.env.MDS_DAP_LINE))
local known_variable = assert(vim.env.MDS_DAP_VARIABLE)

local result = {
  breakpoint_verified = false,
  stopped_at_source = false,
  stack_observed = false,
  scopes_observed = false,
  known_variable_present = false,
  continued = false,
  stepped_in = false,
  stepped_over = false,
  terminated = false,
}
local stops = 0
local inspecting = false

local trust_root = assert(vim.uv.fs_realpath(assert(vim.env.MDS_TRUST_ROOT)))
local state_dir = vim.fn.stdpath "state" .. "/mds"
local state_file = state_dir .. "/workspace-trust.json"
vim.fn.mkdir(state_dir, "p", 448)
local handle = assert(io.open(state_file, "wb"))
handle:write(vim.json.encode({ [vim.fn.sha256(trust_root)] = true }) .. "\n")
handle:close()
assert(vim.uv.fs_chmod(state_file, 384))

local function fail(message)
  result.error = message
  result.terminated = true
end

local function inspect_stop(session, body, done)
  local buffer_breakpoints = require("dap.breakpoints").get(0)[vim.api.nvim_get_current_buf()] or {}
  for _, breakpoint in ipairs(buffer_breakpoints) do
    if breakpoint.line == line and breakpoint.state and breakpoint.state.verified == true then
      result.breakpoint_verified = true
    end
  end
  session:request("stackTrace", { threadId = body.threadId, startFrame = 0, levels = 20 }, function(stack_error, stack)
    if stack_error or not stack or not stack.stackFrames or #stack.stackFrames == 0 then
      fail "stackTrace failed"
      return
    end
    result.stack_observed = true
    local frame = stack.stackFrames[1]
    if frame.source and frame.source.path then
      local stopped_path = vim.uv.fs_realpath(frame.source.path) or frame.source.path
      result.stopped_at_source = stopped_path == source and frame.line == line
    end
    session:request("scopes", { frameId = frame.id }, function(scope_error, scope_result)
      if scope_error or not scope_result or not scope_result.scopes or #scope_result.scopes == 0 then
        fail "scopes failed"
        return
      end
      result.scopes_observed = true
      local pending = 0
      for _, scope in ipairs(scope_result.scopes) do
        if scope.variablesReference and scope.variablesReference > 0 then
          pending = pending + 1
          session:request("variables", { variablesReference = scope.variablesReference }, function(_, variables_result)
            if variables_result and variables_result.variables then
              for _, variable in ipairs(variables_result.variables) do
                if variable.name == known_variable then result.known_variable_present = true end
              end
            end
            pending = pending - 1
            if pending == 0 then done() end
          end)
        end
      end
      if pending == 0 then done() end
    end)
  end)
end

dap.listeners.after.event_breakpoint.mds_probe = function(_, body)
  if body and body.breakpoint and body.breakpoint.verified then result.breakpoint_verified = true end
end
dap.listeners.after.event_continued.mds_probe = function() result.continued = true end
dap.listeners.after.event_stopped.mds_probe = function(session, body)
  if inspecting then return end
  stops = stops + 1
  if stops == 1 then
    inspecting = true
    inspect_stop(session, body, function()
      inspecting = false
      vim.schedule(dap.step_over)
    end)
  elseif stops == 2 then
    result.stepped_over = true
    vim.schedule(dap.step_into)
  elseif stops == 3 then
    result.stepped_in = true
    result.continued = true
    vim.schedule(dap.continue)
  else
    result.continued = true
    vim.schedule(dap.continue)
  end
end
dap.listeners.after.event_terminated.mds_probe = function() result.terminated = true end
dap.listeners.after.event_exited.mds_probe = function() result.terminated = true end

local configuration
if adapter_type == "kotlin" then
  dap.adapters.kotlin = { type = "executable", command = assert(adapter) }
  configuration = {
    type = "kotlin", request = "launch", name = "MDS Kotlin probe",
    projectRoot = root, mainClass = assert(vim.env.MDS_DAP_MAIN),
  }
elseif adapter_type == "java" then
  configuration = {
    type = "java", request = "launch", name = "MDS Java probe",
    cwd = root, mainClass = assert(vim.env.MDS_DAP_MAIN),
  }
  if vim.env.MDS_DAP_PROJECT and vim.env.MDS_DAP_PROJECT ~= "" then
    configuration.projectName = vim.env.MDS_DAP_PROJECT
  end
else
  error "unsupported MDS_DAP_TYPE"
end

vim.cmd("edit " .. vim.fn.fnameescape(source))
if adapter_type == "java" then
  if not vim.wait(120000, function()
    for _, client in ipairs(vim.lsp.get_clients { bufnr = 0 }) do
      if client.name == "jdtls" and client.initialized then return dap.adapters.java ~= nil end
    end
    return false
  end, 100) then
    fail "jdtls DAP adapter did not initialize"
  end
end
vim.api.nvim_win_set_cursor(0, { line, 0 })
if not result.terminated then
  dap.set_breakpoint()
  dap.run(configuration)
end

if not vim.wait(120000, function() return result.terminated end, 50) then
  result.error = "debug probe timed out"
  pcall(dap.terminate)
end
io.stdout:write("MDS_DAP_RESULT=" .. vim.json.encode(result) .. "\n")
if not (result.breakpoint_verified and result.stopped_at_source and result.stack_observed and
    result.scopes_observed and result.known_variable_present and result.continued and
    result.stepped_in and result.stepped_over and result.terminated) then
  vim.cmd "cquit 1"
end
vim.cmd "qa!"
