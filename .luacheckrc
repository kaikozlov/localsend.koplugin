-- Luacheck configuration for LocalSend KOReader plugin

-- Allow long lines (KOReader plugin style)
max_line_length = 150

-- KOReader global variables
globals = {
    "G_reader_settings",
}

-- Allow unused self in methods (common Lua OOP pattern)
unused_args = false

-- Don't warn about shadowing (common in nested functions)
redefined = false

-- Files to ignore (test mocks define globals)
exclude_files = {
    "lua/spec/*",
    "spec/*",
}
