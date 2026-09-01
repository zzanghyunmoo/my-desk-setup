package guest

func renderSymbolConfig() string {
	return `local M = {}

local method = "textDocument/documentSymbol"
local symbol_kinds = {
  [1] = "file",
  [2] = "module",
  [3] = "namespace",
  [4] = "package",
  [5] = "class",
  [6] = "method",
  [7] = "property",
  [8] = "field",
  [9] = "constructor",
  [10] = "enum",
  [11] = "interface",
  [12] = "function",
  [13] = "variable",
  [14] = "constant",
  [15] = "string",
  [16] = "number",
  [17] = "boolean",
  [18] = "array",
  [19] = "object",
  [20] = "key",
  [21] = "null",
  [22] = "enum-member",
  [23] = "struct",
  [24] = "event",
  [25] = "operator",
  [26] = "type-parameter",
}

local state = {
  source_buf = nil,
  source_win = nil,
  sources = {},
  tree_win = nil,
  panel_buf = nil,
  panel_win = nil,
  entries = {},
  generation = 0,
  refresh_serial = 0,
  cancel = nil,
  request_timer = nil,
  suppressed = {},
  setup = false,
}

local min_tree_height = 6
local min_panel_height = 4
local max_panel_height = 12

local function valid_window(win)
  return win and vim.api.nvim_win_is_valid(win)
end

local function valid_buffer(buf)
  return buf and vim.api.nvim_buf_is_valid(buf) and vim.api.nvim_buf_is_loaded(buf)
end

local function is_source_buffer(buf)
  if not valid_buffer(buf) or vim.bo[buf].buftype ~= "" then return false end
  local filetype = vim.bo[buf].filetype
  return filetype ~= "NvimTree" and filetype ~= "mds-symbols"
end

local function remember_source(buf, win)
  if not is_source_buffer(buf) then return false end
  state.source_buf = buf
  if valid_window(win) and vim.api.nvim_win_get_buf(win) == buf then
    state.source_win = win
    state.sources[vim.api.nvim_win_get_tabpage(win)] = buf
  else
    state.source_win = nil
    state.sources[vim.api.nvim_get_current_tabpage()] = buf
  end
  return true
end

local function remember_visible_source()
  local tab = vim.api.nvim_get_current_tabpage()
  local current_win = vim.api.nvim_get_current_win()
  if remember_source(vim.api.nvim_get_current_buf(), current_win) then return true end

  local remembered = state.sources[tab]
  if is_source_buffer(remembered) then
    for _, win in ipairs(vim.api.nvim_tabpage_list_wins(tab)) do
      if valid_window(win) and vim.api.nvim_win_get_buf(win) == remembered then
        return remember_source(remembered, win)
      end
    end
  end

  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if valid_window(win) and remember_source(vim.api.nvim_win_get_buf(win), win) then return true end
  end
  state.sources[tab] = nil
  state.source_buf = nil
  state.source_win = nil
  return false
end

local function is_suppressed(tab)
  return state.suppressed[tab or vim.api.nvim_get_current_tabpage()] == true
end

local function set_suppressed(value, tab)
  state.suppressed[tab or vim.api.nvim_get_current_tabpage()] = value and true or nil
end

local function find_tree_window()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if valid_window(win) then
      local buf = vim.api.nvim_win_get_buf(win)
      if valid_buffer(buf) and vim.bo[buf].filetype == "NvimTree" then return win end
    end
  end
end

local function find_source_window()
  if valid_window(state.source_win) and
      vim.api.nvim_win_get_buf(state.source_win) == state.source_buf then
    return state.source_win
  end
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if valid_window(win) and vim.api.nvim_win_get_buf(win) == state.source_buf then
      state.source_win = win
      return win
    end
  end
end

local function panel_title()
  if not valid_buffer(state.source_buf) then return "Symbols" end
  local name = vim.api.nvim_buf_get_name(state.source_buf)
  if name == "" then return "Symbols: [No Name]" end
  return "Symbols: " .. vim.fn.fnamemodify(name, ":t")
end

local function render(lines, entries)
  if not valid_buffer(state.panel_buf) then return end
  local content = { panel_title() }
  vim.list_extend(content, lines)
  vim.bo[state.panel_buf].readonly = false
  vim.bo[state.panel_buf].modifiable = true
  vim.api.nvim_buf_set_lines(state.panel_buf, 0, -1, false, content)
  vim.bo[state.panel_buf].modifiable = false
  vim.bo[state.panel_buf].readonly = true
  state.entries = {}
  for line, entry in pairs(entries or {}) do
    state.entries[line + 1] = entry
  end
end

local function placeholder(message)
  render({ "[" .. message .. "]" }, {})
end

local function ensure_panel_buffer()
  if valid_buffer(state.panel_buf) then return state.panel_buf end
  local buf = vim.api.nvim_create_buf(false, true)
  state.panel_buf = buf
  vim.bo[buf].buftype = "nofile"
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].swapfile = false
  vim.bo[buf].modifiable = false
  vim.bo[buf].readonly = true
  vim.bo[buf].filetype = "mds-symbols"
  vim.keymap.set("n", "<CR>", function() M.jump() end, {
    buffer = buf,
    silent = true,
    desc = "Jump to symbol",
  })
  vim.keymap.set("n", "o", function() M.jump() end, {
    buffer = buf,
    silent = true,
    desc = "Jump to symbol",
  })
  vim.keymap.set("n", "<2-LeftMouse>", function() M.jump() end, {
    buffer = buf,
    silent = true,
    desc = "Jump to symbol",
  })
  vim.keymap.set("n", "q", function() M.close(true) end, {
    buffer = buf,
    silent = true,
    desc = "Close symbol panel",
  })
  return buf
end

local function symbol_location(symbol, source_uri)
  if symbol.location and symbol.location.uri and symbol.location.range then
    return symbol.location
  end
  local range = symbol.selectionRange or symbol.range
  if not range then return nil end
  return { uri = source_uri, range = range }
end

local function location_key(location)
  if not location or not location.range or not location.range.start then return "" end
  local start = location.range.start
  return table.concat({ location.uri or "", start.line or -1, start.character or -1 }, ":")
end

local function merge_symbols(target, index, symbols, source_uri, encoding)
  for _, symbol in ipairs(symbols or {}) do
    local name = tostring(symbol.name or "[anonymous]"):gsub("[\r\n]", " ")
    local kind = symbol_kinds[symbol.kind] or ("kind-" .. tostring(symbol.kind or "unknown"))
    local location = symbol_location(symbol, source_uri)
    local key = table.concat({ location_key(location), symbol.kind or "", name }, "|")
    local node = index[key]
    if not node then
      node = {
        name = name,
        kind = kind,
        location = location,
        encoding = encoding,
        children = {},
        child_index = {},
      }
      index[key] = node
      table.insert(target, node)
    end
    merge_symbols(node.children, node.child_index, symbol.children, source_uri, encoding)
  end
end

local function flatten_symbols(nodes, depth, lines, entries)
  for _, node in ipairs(nodes) do
    table.insert(lines, string.rep("  ", depth) .. "[" .. node.kind .. "] " .. node.name)
    if node.location then
      entries[#lines] = { location = node.location, encoding = node.encoding }
    end
    flatten_symbols(node.children, depth + 1, lines, entries)
  end
end

local function client_encodings(clients)
  local result = {}
  for _, client in ipairs(clients) do
    result[client.id] = client.offset_encoding or "utf-16"
  end
  return result
end

local function stop_request_timer()
  local timer = state.request_timer
  state.request_timer = nil
  if not timer or timer:is_closing() then return end
  pcall(timer.stop, timer)
  pcall(timer.close, timer)
end

local function cancel_request()
  stop_request_timer()
  local cancel = state.cancel
  state.cancel = nil
  if cancel then pcall(cancel) end
end

local function request_timeout_ms()
  local configured = tonumber(vim.g.mds_symbol_request_timeout_ms)
  if configured and configured > 0 then return configured end
  return 5000
end

function M.refresh()
  if not valid_window(state.panel_win) or not valid_buffer(state.panel_buf) then return end
  if not valid_buffer(state.source_buf) then
    placeholder "No source buffer"
    return
  end

  state.generation = state.generation + 1
  local generation = state.generation
  cancel_request()

  local clients = vim.lsp.get_clients { bufnr = state.source_buf, method = method }
  if #clients == 0 then
    placeholder "No LSP document symbols"
    return
  end

  placeholder "Loading symbols..."
  local source_buf = state.source_buf
  local source_uri = vim.uri_from_bufnr(source_buf)
  local encodings = client_encodings(clients)
  local params = { textDocument = vim.lsp.util.make_text_document_params(source_buf) }
  local completed = false
  local cancel
  cancel = vim.lsp.buf_request_all(source_buf, method, params, function(results)
    completed = true
    if state.cancel == cancel then
      state.cancel = nil
      stop_request_timer()
    end
    if generation ~= state.generation or source_buf ~= state.source_buf or
        not valid_buffer(source_buf) or not valid_window(state.panel_win) then
      return
    end
    local roots = {}
    local root_index = {}
    local successful_response = false
    local client_ids = vim.tbl_keys(results or {})
    table.sort(client_ids, function(left, right) return tostring(left) < tostring(right) end)
    for _, client_id in ipairs(client_ids) do
      local response = results[client_id]
      local response_error = response and (response.err or response.error)
      if response and not response_error then
        successful_response = true
        merge_symbols(
          roots,
          root_index,
          response.result,
          source_uri,
          encodings[tonumber(client_id) or client_id] or "utf-16"
        )
      end
    end
    local lines = {}
    local entries = {}
    flatten_symbols(roots, 0, lines, entries)
    if #lines == 0 then
      placeholder(successful_response and "No symbols" or "Symbol request failed")
      return
    end
    render(lines, entries)
  end)
  if completed then return end
  state.cancel = cancel
  state.request_timer = vim.defer_fn(function()
    if generation ~= state.generation or source_buf ~= state.source_buf or state.cancel ~= cancel then
      return
    end
    stop_request_timer()
    state.cancel = nil
    if cancel then pcall(cancel) end
    state.generation = state.generation + 1
    if valid_buffer(source_buf) and valid_window(state.panel_win) then
      placeholder "Symbol request timed out"
    end
  end, request_timeout_ms())
end

local function schedule_refresh()
  state.refresh_serial = state.refresh_serial + 1
  local serial = state.refresh_serial
  vim.defer_fn(function()
    if serial == state.refresh_serial then M.refresh() end
  end, 120)
end

local function initial_panel_height(tree_height)
  local available = tree_height - min_tree_height - 1
  if available < min_panel_height then return nil end
  return math.min(max_panel_height, available, math.max(min_panel_height, math.floor(tree_height / 3)))
end

local function rebalance_panel()
  if not valid_window(state.panel_win) or not valid_window(state.tree_win) then
    local tree_win = find_tree_window()
    if not is_suppressed() and tree_win
        and initial_panel_height(vim.api.nvim_win_get_height(tree_win)) then
      M.open()
    end
    return
  end
  local tree_height = vim.api.nvim_win_get_height(state.tree_win)
  local panel_height = vim.api.nvim_win_get_height(state.panel_win)
  local available = tree_height + panel_height
  local maximum = available - min_tree_height
  if maximum < min_panel_height then
    M.close(false)
    return
  end
  local desired = math.min(
    max_panel_height,
    maximum,
    math.max(min_panel_height, math.floor((available + 1) / 3))
  )
  if desired ~= panel_height then vim.api.nvim_win_set_height(state.panel_win, desired) end
end

function M.open(force)
  local tab = vim.api.nvim_get_current_tabpage()
  if is_suppressed(tab) and not force then return false end
  if #vim.api.nvim_list_uis() == 0 and not force then return false end

  remember_visible_source()

  local tree_win = find_tree_window()
  if not tree_win then return false end
  if valid_window(state.panel_win) then
    local panel_tab = vim.api.nvim_win_get_tabpage(state.panel_win)
    if panel_tab == tab and state.tree_win == tree_win then
      M.refresh()
      return true
    end
    M.close(false)
  end

  local tree_height = vim.api.nvim_win_get_height(tree_win)
  local panel_height = initial_panel_height(tree_height)
  if not panel_height then return false end
  local panel_buf = ensure_panel_buffer()
  local panel_win = vim.api.nvim_open_win(panel_buf, false, {
    split = "below",
    win = tree_win,
    height = panel_height,
  })
  state.tree_win = tree_win
  state.panel_win = panel_win
  set_suppressed(false, tab)
  vim.wo[panel_win].number = false
  vim.wo[panel_win].relativenumber = false
  vim.wo[panel_win].signcolumn = "no"
  vim.wo[panel_win].foldcolumn = "0"
  vim.wo[panel_win].wrap = false
  vim.wo[panel_win].cursorline = true
  vim.wo[panel_win].winfixheight = true
  vim.wo[panel_win].winfixwidth = true
  M.refresh()
  return true
end

function M.close(suppress)
  state.generation = state.generation + 1
  state.refresh_serial = state.refresh_serial + 1
  cancel_request()
  local panel_win = state.panel_win
  local panel_tab = valid_window(panel_win) and vim.api.nvim_win_get_tabpage(panel_win)
    or vim.api.nvim_get_current_tabpage()
  state.panel_win = nil
  state.tree_win = nil
  state.entries = {}
  if suppress then set_suppressed(true, panel_tab) end
  if valid_window(panel_win) then pcall(vim.api.nvim_win_close, panel_win, true) end
end

function M.toggle()
  local tab = vim.api.nvim_get_current_tabpage()
  if valid_window(state.panel_win) and vim.api.nvim_win_get_tabpage(state.panel_win) == tab then
    M.close(true)
    return
  end
  if valid_window(state.panel_win) then M.close(false) end
  set_suppressed(false, tab)
  M.open()
end

function M.jump()
  if not valid_window(state.panel_win) then return end
  local line = vim.api.nvim_win_get_cursor(state.panel_win)[1]
  local entry = state.entries[line]
  if not entry then return end
  local source_win = find_source_window()
  if not source_win then return end
  vim.api.nvim_set_current_win(source_win)
  vim.lsp.util.show_document(entry.location, entry.encoding, {
    focus = true,
    reuse_win = true,
  })
end

function M.setup()
  if state.setup then return end
  state.setup = true
  remember_source(vim.api.nvim_get_current_buf(), vim.api.nvim_get_current_win())

  vim.api.nvim_create_user_command("MdsSymbolsOpen", function()
    set_suppressed(false)
    M.open()
  end, { desc = "Open the managed LSP symbol panel" })
  vim.api.nvim_create_user_command("MdsSymbolsToggle", function() M.toggle() end, {
    desc = "Toggle the managed LSP symbol panel",
  })
  vim.api.nvim_create_user_command("MdsSymbolsRefresh", function() M.refresh() end, {
    desc = "Refresh the managed LSP symbol panel",
  })

  local group = vim.api.nvim_create_augroup("mds-symbol-panel", { clear = true })
  vim.api.nvim_create_autocmd("BufEnter", {
    group = group,
    callback = function(event)
      if remember_source(event.buf, vim.api.nvim_get_current_win()) then
        if not is_suppressed() and find_tree_window() then vim.schedule(function() M.open() end) end
      end
    end,
  })
  vim.api.nvim_create_autocmd("BufWinEnter", {
    group = group,
    callback = function(event)
      if valid_buffer(event.buf) and vim.bo[event.buf].filetype == "NvimTree" then
        vim.schedule(function() M.open() end)
      end
    end,
  })
  vim.api.nvim_create_autocmd("FileType", {
    group = group,
    pattern = "NvimTree",
    callback = function()
      vim.schedule(function() M.open() end)
    end,
  })
  vim.api.nvim_create_autocmd("TabEnter", {
    group = group,
    callback = function()
      remember_visible_source()
      if not is_suppressed() and find_tree_window() then
        vim.schedule(function() M.open() end)
      end
    end,
  })
  vim.api.nvim_create_autocmd({ "LspAttach", "LspDetach", "TextChanged", "TextChangedI", "BufWritePost" }, {
    group = group,
    callback = function(event)
      if event.buf == state.source_buf then schedule_refresh() end
    end,
  })
  vim.api.nvim_create_autocmd("WinClosed", {
    group = group,
    callback = function(event)
      local closed = tonumber(event.match)
      if closed == state.tree_win then
        vim.schedule(function()
          if state.tree_win == closed then M.close(false) end
        end)
      elseif closed == state.panel_win then
        M.close(false)
      end
    end,
  })
  vim.api.nvim_create_autocmd({ "VimResized", "WinResized" }, {
    group = group,
    callback = function() vim.schedule(rebalance_panel) end,
  })

  if #vim.api.nvim_list_uis() > 0 and find_tree_window() then
    vim.schedule(function() M.open() end)
  end
end

return M
`
}
