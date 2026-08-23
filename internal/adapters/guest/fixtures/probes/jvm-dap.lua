local dap = require "dap"

local adapter_type = assert(vim.env.MDS_DAP_TYPE)
local adapter = vim.env.MDS_DAP_ADAPTER
local mode = assert(vim.env.MDS_DAP_MODE)
local root = assert(vim.env.MDS_DAP_ROOT)
local source = assert(vim.env.MDS_DAP_SOURCE)
local line = assert(tonumber(vim.env.MDS_DAP_LINE))
local known_variable = assert(vim.env.MDS_DAP_VARIABLE)
local test_selector = vim.env.MDS_DAP_TEST_SELECTOR

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
local test_task
local test_result
local test_stopping = false

local trust_root = assert(vim.uv.fs_realpath(assert(vim.env.MDS_TRUST_ROOT)))
local state_dir = vim.fn.stdpath "state" .. "/mds"
local state_file = state_dir .. "/workspace-trust.json"
vim.fn.mkdir(state_dir, "p", 448)
local handle = assert(io.open(state_file, "wb"))
handle:write(vim.json.encode({ [vim.fn.sha256(trust_root)] = true }) .. "\n")
handle:close()
assert(vim.uv.fs_chmod(state_file, 384))

local function stop_test_task()
  if not test_task or test_stopping then return end
  test_stopping = true
  if test_task.pid then pcall(vim.uv.kill, -test_task.pid, 15) end
  pcall(test_task.kill, test_task, 15)
  vim.defer_fn(function()
    if test_task.pid then pcall(vim.uv.kill, -test_task.pid, 9) end
    pcall(test_task.kill, test_task, 9)
  end, 2000)
end

local function fail(message)
  result.error = message
  result.terminated = true
  stop_test_task()
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
  if mode == "test" then
    configuration = {
      type = "kotlin", request = "attach", name = "MDS Kotlin test probe",
      projectRoot = root, hostName = "127.0.0.1", port = 5005, timeout = 30000,
    }
  else
    configuration = {
      type = "kotlin", request = "launch", name = "MDS Kotlin probe",
      projectRoot = root, mainClass = assert(vim.env.MDS_DAP_MAIN),
    }
  end
elseif adapter_type == "java" then
  if mode == "test" then
    configuration = {
      type = "java", request = "attach", name = "MDS Java test probe",
      hostName = "127.0.0.1", port = 5005,
    }
  else
    configuration = {
      type = "java", request = "launch", name = "MDS Java probe",
      cwd = root, mainClass = assert(vim.env.MDS_DAP_MAIN),
    }
  end
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
  if mode == "test" then
    local output = ""
    local listening = false
    local function capture(_, data)
      if not data or data == "" then return end
      output = (output .. data):sub(-8192)
      if output:match "Listening for transport dt_socket at address: 5005" then
        listening = true
      end
    end
    test_task = vim.system({ "setsid", root .. "/gradlew", "--no-daemon", "test", "--tests", assert(test_selector), "--debug-jvm", "--rerun" }, {
      cwd = root, clear_env = true,
      env = { HOME = vim.fn.expand "~", PATH = vim.env.PATH, TMPDIR = vim.env.TMPDIR or "/tmp" },
      text = true, stdout = capture, stderr = capture,
    }, function(result) test_result = result end)
    vim.wait(90000, function() return listening or test_result ~= nil end, 50)
    if listening then
      dap.run(configuration)
    elseif test_result then
      fail("Gradle test JVM exited before opening the debug socket")
    else
      fail "Gradle test JVM timed out before opening the debug socket"
    end
  else
    dap.run(configuration)
  end
end

if not vim.wait(120000, function() return result.terminated end, 50) then
  result.error = "debug probe timed out"
  pcall(dap.terminate)
end
if test_task then
  local completed = test_result
  if not completed then
    vim.wait(60000, function() return test_result ~= nil end, 50)
    completed = test_result
  end
  if not completed then
    result.error = "Gradle test task timed out after debugging"
  elseif completed.code ~= 0 then
    result.error = "Gradle test task failed after debugging with exit code " .. tostring(completed.code)
  end
  if result.error then
    stop_test_task()
  end
end
io.stdout:write("MDS_DAP_RESULT=" .. vim.json.encode(result) .. "\n")
if not (result.breakpoint_verified and result.stopped_at_source and result.stack_observed and
    result.scopes_observed and result.known_variable_present and result.continued and
    result.stepped_in and result.stepped_over and result.terminated and not result.error) then
  vim.cmd "cquit 1"
end
vim.cmd "qa!"
