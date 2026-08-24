package guest

import (
	"fmt"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/projectaction"
)

const workspaceTrustLua = `local M = {}

local state_dir = vim.fn.stdpath "state" .. "/mds"
local state_file = state_dir .. "/workspace-trust.json"
local warned = {}
local exact_markers = {
  [".git"] = true,
  ["gradlew"] = true,
  ["pom.xml"] = true,
  ["settings.gradle"] = true,
  ["settings.gradle.kts"] = true,
  ["global.json"] = true,
}

local function canonical(root)
  return root and vim.uv.fs_realpath(root) or nil
end

local function has_project_marker(directory)
  local scan = vim.uv.fs_scandir(directory)
  if not scan then return false end
  while true do
    local name = vim.uv.fs_scandir_next(scan)
    if not name then return false end
    if exact_markers[name] or vim.endswith(name, ".sln") or vim.endswith(name, ".csproj") then
      return true
    end
  end
end

function M.roots(source)
  local start = type(source) == "number" and vim.api.nvim_buf_get_name(source) or source
  if not start or start == "" then start = vim.uv.cwd() end
  local stat = vim.uv.fs_stat(start)
  if not stat then
    start = vim.fs.dirname(start)
  elseif stat.type ~= "directory" then
    start = vim.fs.dirname(start)
  end
  local result = {}
  local cursor = canonical(start)
  while cursor do
    if has_project_marker(cursor) then table.insert(result, cursor) end
    local parent = vim.fs.dirname(cursor)
    if parent == cursor then break end
    cursor = parent
  end
  table.sort(result)
  return result
end

function M.nearest(source)
  local roots = M.roots(source)
  table.sort(roots, function(left, right)
    if #left ~= #right then return #left > #right end
    return left < right
  end)
  return roots[1]
end

function M.managed_executable(name)
  if not name:match "^[%w._+-]+$" then error("invalid managed executable name") end
  local path = vim.fn.expand("~/.local/share/mise/shims/") .. name
  local stat = vim.uv.fs_stat(path)
  if not stat or stat.type ~= "file" then error("missing managed executable: " .. name) end
  return path
end

function M.managed_launcher(name)
  if not name:match "^[%w._+-]+$" then error("invalid managed launcher name") end
  local path = vim.fn.expand("~/.local/bin/") .. name
  local stat = vim.uv.fs_stat(path)
  if not stat or stat.type ~= "file" then error("missing managed launcher: " .. name) end
  return path
end

local function load()
  local stat = vim.uv.fs_stat(state_file)
  if not stat or stat.type ~= "file" or stat.size > 65536 then return {} end
  local handle = io.open(state_file, "rb")
  if not handle then return {} end
  local content = handle:read "*a"
  handle:close()
  local ok, decoded = pcall(vim.json.decode, content)
  return ok and type(decoded) == "table" and decoded or {}
end

local function save(records)
  vim.fn.mkdir(state_dir, "p", 448)
  local temporary = state_file .. ".tmp-" .. tostring(vim.uv.os_getpid())
  local handle = assert(io.open(temporary, "wb"))
  handle:write(vim.json.encode(records) .. "\n")
  handle:close()
  assert(vim.uv.fs_chmod(temporary, 384))
  assert(vim.uv.fs_rename(temporary, state_file))
end

function M.is_trusted(root)
  root = canonical(root)
  if not root then return false end
  local trusted = load()[vim.fn.sha256(root)] == true
  if not trusted and not warned[root] then
    warned[root] = true
    vim.schedule(function()
      vim.notify("MDS workspace is untrusted; project import and execution are disabled. Run :MdsTrustWorkspace to opt in.", vim.log.levels.WARN)
    end)
  end
  return trusted
end

function M.untrust(root)
  root = canonical(root)
  if not root then return false end
  local records = load()
  local digest = vim.fn.sha256(root)
  if records[digest] ~= true then return false end
  records[digest] = nil
  save(records)
  warned[root] = nil
  for _, client in ipairs(vim.lsp.get_clients()) do
    if canonical(client.root_dir) == root then pcall(client.stop, client, true) end
  end
  vim.api.nvim_exec_autocmds("User", {
    pattern = "MdsWorkspaceUntrusted", data = { root = root },
  })
  return true
end

function M.runtime(component, identity, executable)
  local root = vim.fn.expand("~/.local/share/mds/runtime-trees/") .. component .. "/" .. identity
  local handle = assert(io.open(root .. "/.mds-runtime-tree-current", "rb"))
  local generation = handle:read "*l"
  handle:close()
  if not generation or not generation:match "^g%-%w[%w%-]*$" then error("invalid MDS runtime generation") end
  local path = root .. "/generations/" .. generation .. "/payload/" .. executable
  local stat = vim.uv.fs_stat(path)
  if not stat or stat.type ~= "file" then error("missing MDS runtime payload: " .. component) end
  return path
end

vim.api.nvim_create_user_command("MdsTrustWorkspace", function()
  local roots = M.roots(0)
  if #roots == 0 then vim.notify("No project root found", vim.log.levels.ERROR); return end
  local function trust(root)
    if not root then return end
    if vim.fn.confirm("Trust project-controlled code in " .. root .. "?", "&Trust\n&Cancel", 2) ~= 1 then return end
    local records = load()
    records[vim.fn.sha256(root)] = true
    save(records)
    warned[root] = nil
    vim.notify("Trusted workspace: " .. root)
  end
  if #roots == 1 then trust(roots[1]); return end
  vim.ui.select(roots, { prompt = "Trust which MDS project root?" }, trust)
end, {})

vim.api.nvim_create_user_command("MdsUntrustWorkspace", function()
  local roots = M.roots(0)
  if #roots == 0 then vim.notify("No project root found", vim.log.levels.ERROR); return end
  local function untrust(root)
    if not root then return end
    if vim.fn.confirm("Revoke trust and stop project processes for " .. root .. "?", "&Revoke\n&Cancel", 2) ~= 1 then return end
    if M.untrust(root) then
      vim.notify("Revoked workspace trust: " .. root)
    else
      vim.notify("Workspace was not trusted: " .. root, vim.log.levels.WARN)
    end
  end
  if #roots == 1 then untrust(roots[1]); return end
  vim.ui.select(roots, { prompt = "Revoke trust for which MDS project root?" }, untrust)
end, {})

return M
`

func renderJVMConfig(references map[string]runtimeTreeReference) string {
	return fmt.Sprintf(`local M = {}

local trust = require "configs.trust"
local jdtls = %s
local debug_bundle = %s
local test_bundle = %s
local spring_server = %s
local kotlin_debug = %s

local function bundles()
  local result = { debug_bundle }
  local parent = vim.fs.dirname(test_bundle)
  vim.list_extend(result, vim.fn.glob(parent .. "/*.jar", false, true))
  local spring_extension = vim.fs.dirname(vim.fs.dirname(spring_server))
  vim.list_extend(result, vim.fn.glob(spring_extension .. "/jars/*.jar", false, true))
  table.sort(result)
  return result
end

local function attach(bufnr)
  local root = vim.fs.root(bufnr, { "gradlew", "mvnw", "settings.gradle", "settings.gradle.kts", "pom.xml", ".git" })
  if not root or not trust.is_trusted(root) then return end
  local client = require "jdtls"
  client.start_or_attach({
    cmd = { jdtls, "-data", vim.fn.stdpath("cache") .. "/mds/jdtls/" .. vim.fn.sha256(root) },
    root_dir = root,
    init_options = { bundles = bundles() },
    settings = { java = { import = { gradle = { enabled = true } } } },
    on_attach = function(_, attached)
      vim.keymap.set("n", "<leader>jt", client.test_nearest_method, { buffer = attached, desc = "Java test method" })
      vim.keymap.set("n", "<leader>jT", client.test_class, { buffer = attached, desc = "Java test class" })
    end,
  }, { dap = { hotcodereplace = "auto" } })
end

function M.setup()
  local group = vim.api.nvim_create_augroup("mds-jdtls", { clear = true })
  vim.api.nvim_create_autocmd("FileType", {
    group = group, pattern = "java",
    callback = function(args) attach(args.buf) end,
  })
  if vim.bo.filetype == "java" then attach(0) end

  local dap = require "dap"
  dap.adapters.kotlin = { type = "executable", command = kotlin_debug }
end

return M
`,
		runtimeLuaExpression(references["jdt-language-server"], "jdtls"),
		runtimeLuaExpression(references["java-debug-server"], "java-debug-server.jar"),
		runtimeLuaExpression(references["java-test-server"], "java-test-server.jar"),
		runtimeLuaExpression(references["spring-tools-language-server"], "mds-spring-boot-ls"),
		runtimeLuaExpression(references["kotlin-debug-adapter"], "kotlin-debug-adapter"),
	)
}

func renderDotNetConfig(references map[string]runtimeTreeReference) string {
	return fmt.Sprintf(`local M = {}

function M.setup()
	local trust = require "configs.trust"
	local roslyn = %s
	local dotnet = trust.managed_executable "dotnet"
  vim.lsp.config("roslyn", {
    cmd = { dotnet, roslyn, "--stdio" },
    filetypes = { "cs", "razor" },
    root_dir = function(bufnr, callback)
      local target = require "roslyn.target"
      local decision = target.resolve(bufnr)
      target.notify_if_needed(decision)
      if not decision.root_dir or not trust.is_trusted(decision.root_dir) then return end
      target.remember(decision)
      callback(decision.root_dir)
    end,
  })
  require("roslyn").setup { broad_search = true, lock_target = false }
  vim.lsp.enable "roslyn"

  local dap = require "dap"
  dap.adapters.coreclr = { type = "executable", command = %s, args = { "--interpreter=vscode" } }

	local function project_file(root)
		if not root or root == "" then return nil end
		local stat = vim.uv.fs_stat(root)
		if vim.endswith(root, ".csproj") and stat and stat.type == "file" then return root end
		return nil
	end

	local function target_path()
		local project = project_file(vim.g.mds_dotnet_project)
		if not project then
			return vim.fn.input("Assembly: ", vim.fn.getcwd() .. "/bin/Debug/", "file")
		end
		local options = {
			cwd = vim.fs.dirname(project), clear_env = true,
			env = { HOME = vim.fn.expand "~", PATH = vim.env.PATH, TMPDIR = vim.env.TMPDIR or "/tmp" },
			text = true,
		}
		local build = vim.system({ dotnet, "build", project }, options):wait()
		if build.code ~= 0 then error("failed to restore and build selected .NET project") end
		local result = vim.system({ dotnet, "msbuild", project, "-getProperty:TargetPath" }, options):wait()
		if result.code ~= 0 then error("failed to resolve .NET target path") end
		local target = vim.trim(result.stdout or "")
		local stat = target ~= "" and vim.uv.fs_stat(target) or nil
		if not stat or stat.type ~= "file" then error("built .NET target path is missing") end
		return target
	end

	local function binding_environment_key(key)
		if type(key) ~= "string" then return false end
		local canonical = key:upper()
		while true do
			local stripped = canonical:gsub("^ASPNETCORE_", ""):gsub("^DOTNET_", "")
			if stripped == canonical then break end
			canonical = stripped
		end
		return canonical == "URLS" or canonical == "HTTP_PORTS" or canonical == "HTTPS_PORTS" or
			canonical:match "^KESTREL__ENDPOINTS__" ~= nil
	end

	local function launch_environment()
		local result = {}
		for key, value in pairs(vim.g.mds_dotnet_environment or {}) do
			if type(key) == "string" and type(value) == "string" and not binding_environment_key(key) then
				result[key] = value
			end
		end
		if vim.g.mds_dotnet_launch_profile then
			result.ASPNETCORE_URLS = "http://127.0.0.1:0"
			result.DOTNET_WATCH_SUPPRESS_LAUNCH_BROWSER = "1"
		end
		return result
	end

	local function launch_arguments()
		if not vim.g.mds_dotnet_launch_profile then return {} end
		return { "--urls", "http://127.0.0.1:0" }
	end

	local test_task
	local test_root
	local test_stopping = {}
	local function launch_valid(launch)
		return launch and launch.valid == true and launch.root == test_root and
			require("configs.trust").is_trusted(launch.root)
	end
	function M.stop(root)
		if test_task and test_root == root then
			local task = test_task
			test_task = nil
			test_root = nil
			test_stopping[task] = true
			if task.pid then pcall(vim.uv.kill, -task.pid, 15) end
			pcall(task.kill, task, 15)
			vim.defer_fn(function()
				if not test_stopping[task] then return end
				test_stopping[task] = nil
				if task.pid then pcall(vim.uv.kill, -task.pid, 9) end
				pcall(task.kill, task, 9)
			end, 2000)
		end
	end
	local function test_process_id()
		local project = project_file(vim.g.mds_dotnet_project)
		if not project then error("no .NET test project selected") end
		test_root = vim.g.mds_dotnet_root
		local launch = vim.g.mds_dotnet_launch
		if not launch_valid(launch) then error(".NET test debug launch was cancelled or untrusted") end
		local process_id, output, finished = nil, "", false
		local function capture(_, data)
			if not data or data == "" then return end
			output = (output .. data):sub(-8192)
			local matched = output:match "[Pp]rocess [Ii][Dd]:?%%s*(%%d+)" or output:match "[Pp]rocess [Ii]d%%s+(%%d+)"
			if matched then process_id = tonumber(matched) end
		end
		local task
		task = vim.system({ "setsid", dotnet, "test", project }, {
			cwd = vim.fs.dirname(project), clear_env = true,
			env = {
				HOME = vim.fn.expand "~", PATH = vim.env.PATH,
				TMPDIR = vim.env.TMPDIR or "/tmp", VSTEST_HOST_DEBUG = "1",
			},
			text = true,
			stdout = capture, stderr = capture,
		}, function()
			finished = true
		end)
		test_task = task
		if not vim.wait(30000, function()
			return process_id ~= nil or finished or not launch_valid(launch)
		end, 50) or process_id == nil then
			M.stop(test_root)
			error(".NET testhost exited or timed out before debugger attach")
		end
		if not launch_valid(launch) then
			M.stop(test_root)
			error(".NET test debug launch was cancelled or untrusted")
		end
		return process_id
	end

	dap.configurations.cs = {{
    type = "coreclr", name = "Launch .NET assembly", request = "launch",
		program = target_path,
		args = launch_arguments,
		cwd = function()
			local project = project_file(vim.g.mds_dotnet_project)
			return project and vim.fs.dirname(project) or vim.fn.getcwd()
		end,
		env = launch_environment,
	}, {
		type = "coreclr", name = "Debug .NET tests", request = "attach",
		processId = test_process_id,
	}}
end

return M
`,
		runtimeLuaPathExpression(
			references["roslyn-language-server"],
			"tools/net10.0/linux-arm64/roslyn-language-server.dll",
			"mds-roslyn-ls",
		),
		runtimeLuaExpression(references["netcoredbg"], "netcoredbg"),
	)
}

func renderProjectActions(set pluginSet) string {
	javaEnabled := set&jvmPluginSet != 0
	dotnetEnabled := set&dotnetPluginSet != 0
	actionOrder := projectaction.Order()
	renderedOrder := make([]string, 0, len(actionOrder))
	for _, kind := range actionOrder {
		renderedOrder = append(renderedOrder, fmt.Sprintf("%q", kind))
	}
	return fmt.Sprintf(`local M = {}

local trust = require "configs.trust"
local active = {}
local stopping = {}
local generations = {}
local debug_root
local max_output = 262144
local families = { java = %t, dotnet = %t }

local function roots()
  return trust.roots(0)
end

local function select_one(items, prompt, callback)
  if #items == 0 then vim.notify("No project candidate found", vim.log.levels.ERROR); return end
  if #items == 1 then callback(items[1]); return end
  vim.ui.select(items, { prompt = prompt }, callback)
end

local function loopback_urls(value)
  if not value or value == "" then return true end
  if type(value) ~= "string" then return false end
  for url in value:gmatch "[^;]+" do
    local host = url:match "^https?://(%%[[^%%]]+%%])" or url:match "^https?://([^/:]+)"
    if host ~= "127.0.0.1" and host ~= "localhost" and host ~= "[::1]" then return false end
  end
  return true
end

local function binding_environment_name(key)
	if type(key) ~= "string" then return nil end
	local canonical = key:upper()
	while true do
		local stripped = canonical:gsub("^ASPNETCORE_", ""):gsub("^DOTNET_", "")
		if stripped == canonical then break end
		canonical = stripped
	end
	if canonical == "URLS" or canonical == "HTTP_PORTS" or canonical == "HTTPS_PORTS" or
		canonical:match "^KESTREL__ENDPOINTS__" then return canonical end
	return nil
end

local function binding_environment_key(key)
	return binding_environment_name(key) ~= nil
end

local function profile_loopback_urls(environment)
	for key, value in pairs(environment or {}) do
		local name = binding_environment_name(key)
		if name == "URLS" and not loopback_urls(value) then
			return false
		end
	end
	return true
end

local function dotnet_projects(root)
  local result = vim.fs.find(function(name) return vim.endswith(name, ".csproj") end, {
    path = root, type = "file", limit = 100,
  })
  table.sort(result)
  return result
end

local function dotnet_web_project(project)
	if not project or not vim.endswith(project, ".csproj") then return false end
	local stat = vim.uv.fs_stat(project)
	if not stat or stat.type ~= "file" or stat.size > 262144 then return false end
	local handle = io.open(project, "rb")
	local content = handle and handle:read "*a" or ""
	if handle then handle:close() end
	return content:match('Sdk%%s*=%%s*["\']Microsoft%%.NET%%.Sdk%%.Web["\']') ~= nil or
		content:match('<ProjectCapability%%s+Include%%s*=%%s*["\']AspNetCore["\']') ~= nil
end

local function launch_profiles(root)
  local result = {}
  local files = vim.fs.find("launchSettings.json", { path = root, limit = 100 })
  table.sort(files)
  for _, path in ipairs(files) do
    local stat = vim.uv.fs_stat(path)
    if stat and stat.type == "file" and stat.size <= 262144 then
      local handle = io.open(path, "rb")
      local content = handle and handle:read "*a" or nil
      if handle then handle:close() end
      local ok, document = pcall(vim.json.decode, content or "")
		  if ok and type(document.profiles) == "table" then
			for name, profile in pairs(document.profiles) do
			  if type(profile) == "table" and profile.commandName == "Project" then
				local project_root = vim.fs.dirname(vim.fs.dirname(path))
				local projects = dotnet_projects(project_root)
				local direct_projects = {}
				for _, project in ipairs(projects) do
				  if vim.fs.dirname(project) == project_root then table.insert(direct_projects, project) end
				end
				local candidates = #direct_projects > 0 and direct_projects or projects
				table.insert(result, {
				  label = vim.fs.relpath(root, path) .. ":" .. name,
				  name = name,
				project = #direct_projects == 1 and direct_projects[1] or nil,
				project_candidates = candidates,
				urls = profile.applicationUrl,
				environment = type(profile.environmentVariables) == "table" and profile.environmentVariables or {},
            })
          end
        end
      end
    end
  end
  table.sort(result, function(left, right) return left.label < right.label end)
  return result
end

local function select_launch_profile(root, spec, callback)
  local profiles = launch_profiles(root)
  if #profiles == 0 then callback(spec); return end
  local labels, indexed = {}, {}
  for _, profile in ipairs(profiles) do
    table.insert(labels, profile.label)
    indexed[profile.label] = profile
  end
  select_one(labels, "ASP.NET launch profile", function(label)
    if not label then return end
    local profile = indexed[label]
    if not loopback_urls(profile.urls) or not profile_loopback_urls(profile.environment) then
      vim.notify("MDS rejected a non-loopback ASP.NET launch profile", vim.log.levels.ERROR)
      return
    end
	local resolved = vim.deepcopy(spec)
	resolved.project = profile.project
	resolved.project_candidates = profile.project_candidates
		resolved.profile = profile.name
    resolved.urls = profile.urls
		resolved.environment = vim.deepcopy(profile.environment)
		resolved.environment.ASPNETCORE_URLS = nil
	if not resolved.dap then
		local index = resolved.argv[2] == "watch" and 4 or 3
		table.insert(resolved.argv, index, "--launch-profile")
		table.insert(resolved.argv, index + 1, profile.name)
	end
    callback(resolved)
  end)
end

local function select_dotnet_project(root, spec, callback)
	local function select_project(project)
	if not project then return end
	local resolved = vim.deepcopy(spec)
	resolved.project = project
		resolved.project_candidates = nil
	if not resolved.dap then
	  if resolved.argv[2] == "build" or resolved.argv[2] == "test" then
        table.insert(resolved.argv, 3, project)
      else
		local index = resolved.argv[2] == "watch" and 4 or 3
		table.insert(resolved.argv, index, "--project")
		table.insert(resolved.argv, index + 1, project)
	  end
	end
	callback(resolved)
	end
	if spec.project and vim.endswith(spec.project, ".csproj") then select_project(spec.project); return end
	local projects = spec.project_candidates or dotnet_projects(root)
	select_one(projects, ".NET project", select_project)
end

local function family(root)
  local java = families.java and (vim.uv.fs_stat(root .. "/gradlew") or vim.uv.fs_stat(root .. "/pom.xml"))
  local dotnet = families.dotnet and vim.uv.fs_stat(root .. "/global.json") ~= nil
  if families.dotnet and not dotnet then
    local scan = vim.uv.fs_scandir(root)
    if scan then
      while true do
        local name = vim.uv.fs_scandir_next(scan)
        if not name then break end
        if vim.endswith(name, ".sln") or vim.endswith(name, ".csproj") then dotnet = true; break end
      end
    end
  end
  if java and dotnet then return nil, { "java", "dotnet" } end
  if java then return "java" end
  if dotnet then return "dotnet" end
  return nil, {}
end

local function specs(kind, root)
  if kind == "java" then
    local gradle = root .. "/gradlew"
    if not vim.uv.fs_stat(gradle) then return {} end
    return {
      build = { argv = { gradle, "--no-daemon", "build" } },
      test = { argv = { gradle, "--no-daemon", "test" } },
      run = { argv = { gradle, "--no-daemon", "bootRun" }, long = true },
      watch = { argv = { gradle, "--no-daemon", "bootRun", "--continuous" }, long = true },
      ["debug-app"] = { argv = { gradle, "--no-daemon", "bootRun", "--debug-jvm" }, long = true, java_debug = true },
      ["debug-test"] = { argv = { gradle, "--no-daemon", "test", "--debug-jvm", "--rerun" }, long = true, java_debug = true },
    }
  end
  local dotnet = trust.managed_executable "dotnet"
  return {
    build = { argv = { dotnet, "build" } },
    test = { argv = { dotnet, "test" } },
    run = { argv = { dotnet, "run", "--urls", "http://127.0.0.1:0" }, long = true, launch_profile = true },
    watch = { argv = { dotnet, "watch", "run", "--urls", "http://127.0.0.1:0" }, long = true, launch_profile = true },
		["debug-app"] = { dap = true, debug_kind = "app", launch_profile = true },
		["debug-test"] = { dap = true, debug_kind = "test" },
  }
end

local function stop_debug(root)
	if not root then return end
	if debug_root == root then debug_root = nil end
	pcall(function() require("configs.dotnet").stop(root) end)
	pcall(function()
		local dap = require "dap"
		local previous = dap.session()
		local targets = {}
		for _, session in pairs(dap.sessions()) do
			if session.config and session.config.mds_root == root then table.insert(targets, session) end
		end
		for _, session in ipairs(targets) do
			dap.set_session(session)
			dap.terminate({ hierarchy = true })
		end
		if previous and not previous.closed and previous.config and previous.config.mds_root ~= root then
			dap.set_session(previous)
		end
	end)
end

local function invalidate(root)
	generations[root] = (generations[root] or 0) + 1
	local launch = vim.g.mds_dotnet_launch
	if launch and launch.root == root then launch.valid = false end
end

local function launch_valid(root, generation)
	return generations[root] == generation and trust.is_trusted(root)
end

local function stop(root)
	invalidate(root)
  local task = active[root]
		if task then
		active[root] = nil
		stopping[task] = root
		if task.pid then pcall(vim.uv.kill, -task.pid, 15) end
		pcall(task.kill, task, 15)
		vim.defer_fn(function()
			if stopping[task] ~= root then return end
			stopping[task] = nil
			if task.pid then pcall(vim.uv.kill, -task.pid, 9) end
			pcall(task.kill, task, 9)
		end, 2000)
	end
	stop_debug(root)
end

local function action_environment(spec)
	local result = {}
	for key, value in pairs(spec.environment or {}) do
		if type(key) == "string" and type(value) == "string" and not binding_environment_key(key) then
			result[key] = value
		end
	end
	result.HOME = vim.fn.expand "~"
	result.PATH = vim.env.PATH
	result.TMPDIR = vim.env.TMPDIR or "/tmp"
	result.ASPNETCORE_URLS = "http://127.0.0.1:0"
	result.DOTNET_WATCH_SUPPRESS_LAUNCH_BROWSER = "1"
	return result
end

local function jvm_debug_adapter(root)
	local filetype = vim.bo.filetype
	if filetype == "java" or filetype == "kotlin" then return filetype end
	local kotlin_sources = vim.fs.find(function(name, path)
		if not vim.endswith(name, ".kt") then return false end
		local normalized = (path .. "/" .. name):gsub("\\\\", "/")
		return normalized:find("/src/", 1, true) ~= nil
	end, { path = root, type = "file", limit = 1 })
	return #kotlin_sources > 0 and "kotlin" or "java"
end

local function run(root, name, spec)
  if not trust.is_trusted(root) then return end
	if spec.launch_profile and dotnet_web_project(spec.project) and not spec.profile then
		vim.notify("MDS requires a valid Project launch profile for ASP.NET actions", vim.log.levels.ERROR)
		return
	end
	if (spec.dap or spec.java_debug) and debug_root and debug_root ~= root then stop(debug_root) end
  if spec.dap then
		stop(root)
		local generation = generations[root]
		local launch = { root = root, generation = generation, valid = true }
		vim.g.mds_dotnet_launch = launch
		vim.g.mds_dotnet_project = spec.project or root
		vim.g.mds_dotnet_root = root
		vim.g.mds_dotnet_launch_profile = spec.profile
		vim.g.mds_dotnet_urls = spec.urls
		vim.g.mds_dotnet_environment = spec.environment
		local dap = require "dap"
		local index = spec.debug_kind == "test" and 2 or 1
		local configuration = vim.deepcopy(dap.configurations.cs[index])
		configuration.mds_root = root
		configuration.mds_generation = generation
		debug_root = root
		if not launch_valid(root, generation) or not launch.valid then return end
		dap.run(configuration, { new = true })
    return
  end
	if spec.java_debug then
		local socket = vim.uv.new_tcp()
		local available, bind_error = socket:bind("127.0.0.1", 5005)
		socket:close()
		if not available then
			vim.notify("MDS debug port 5005 is unavailable: " .. tostring(bind_error), vim.log.levels.ERROR)
			return
		end
	end
  stop(root)
	local generation = generations[root]
	local debug_adapter = spec.java_debug and jvm_debug_adapter(root) or nil
  local buffer = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_set_current_buf(buffer)
  vim.bo[buffer].filetype = "mds-project-output"
  vim.bo[buffer].bufhidden = "wipe"
  local size = 0
	local debug_output, debug_started = "", false
  local function append(_, data)
    if not data or data == "" then return end
		if spec.java_debug and not debug_started then
			debug_output = (debug_output .. data):sub(-8192)
			if debug_output:match "Listening for transport dt_socket at address: 5005" then
				debug_started = true
				vim.schedule(function()
					if not launch_valid(root, generation) then stop(root); return end
					require("dap").run {
						type = debug_adapter,
						request = "attach", name = "Attach Gradle JVM",
						hostName = "127.0.0.1", port = 5005, mds_root = root,
						mds_generation = generation,
					}
					debug_root = root
				end)
			end
		end
    size = size + #data
    if size > max_output then stop(root); return end
    vim.schedule(function()
      if vim.api.nvim_buf_is_valid(buffer) then
        vim.api.nvim_buf_set_lines(buffer, -1, -1, false, vim.split(data, "\n", { plain = true, trimempty = true }))
      end
    end)
  end
	local argv = vim.list_extend({ "setsid" }, vim.deepcopy(spec.argv))
  local task
  task = vim.system(argv, {
    cwd = root, clear_env = true,
		env = action_environment(spec),
    text = true, stdout = append, stderr = append,
  }, function(result)
    vim.schedule(function()
		if stopping[task] == root then return end
      if active[root] ~= task then return end
      active[root] = nil
		if debug_root == root then debug_root = nil end
      local status
      if result.code == 0 then
        status = "succeeded"
      else
        status = "failed"
      end
      vim.notify("MDS " .. name .. ": " .. status)
    end)
  end)
  active[root] = task
end

local order = { %s }
local function choose_action(root, kind)
  local available = specs(kind, root)
  local choices = {}
  for _, name in ipairs(order) do if available[name] then table.insert(choices, name) end end
  select_one(choices, "MDS project action", function(name)
    if not name then return end
    local spec = available[name]
    if spec.launch_profile then
		select_launch_profile(root, spec, function(resolved)
			select_dotnet_project(root, resolved, function(selected) run(root, name, selected) end)
		end)
	elseif kind == "dotnet" then
		select_dotnet_project(root, spec, function(selected) run(root, name, selected) end)
    else
      run(root, name, spec)
    end
  end)
end

function M.open()
  select_one(roots(), "MDS project root", function(root)
    if not root then return end
    local selected, choices = family(root)
    if selected then choose_action(root, selected); return end
    select_one(choices, "MDS project kind", function(kind) if kind then choose_action(root, kind) end end)
  end)
end

function M.setup()
  if vim.fn.exists(":MdsProjectAction") == 0 then
    vim.api.nvim_create_user_command("MdsProjectAction", M.open, {})
    vim.api.nvim_create_user_command("MdsProjectCancel", function()
      select_one(vim.tbl_keys(active), "Cancel MDS task", function(root) if root then stop(root) end end)
    end, {})
    vim.keymap.set("n", "<leader>pa", M.open, { desc = "Project actions" })
		vim.api.nvim_create_autocmd("User", {
			pattern = "MdsWorkspaceUntrusted",
			callback = function(args)
				local root = args.data and args.data.root or nil
				if root then stop(root) end
			end,
		})
		local ok, dap = pcall(require, "dap")
		if ok and dap.listeners and dap.listeners.before then
			dap.listeners.before.event_initialized.mds_trust_guard = function(session)
				local config = session and session.config or nil
				if not config or not config.mds_root or not config.mds_generation or
					launch_valid(config.mds_root, config.mds_generation) then return end
				vim.schedule(function()
					local previous = dap.session()
					dap.set_session(session)
					dap.terminate({ hierarchy = true })
					if previous and previous ~= session and not previous.closed then dap.set_session(previous) end
				end)
			end
		end
  end
end

return M
`, javaEnabled, dotnetEnabled, strings.Join(renderedOrder, ", "))
}
