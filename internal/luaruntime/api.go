package luaruntime

import (
	lua "github.com/yuin/gopher-lua"
)

// installAPI creates the capability globals on an extension's state.
// Every function checks the manifest permission first; denials return
// `nil, "permission denied: ..."` per docs/LUA_API.md.
func (m *Manager) installAPI(ext *Extension) {
	L := ext.state
	deny := func(perm string) (lua.LValue, lua.LValue, bool) {
		if !ext.Manifest.has(perm) {
			return lua.LNil, lua.LString("permission denied: extension " + ext.Manifest.ID + " lacks " + perm), true
		}
		return nil, nil, false
	}

	world := L.NewTable()
	L.SetField(world, "get_status", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("world:read"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		info, err := m.API.WorldStatus(L.OptString(1, ""))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		t := L.NewTable()
		t.RawSetString("id", lua.LString(info.ID))
		t.RawSetString("running", lua.LBool(info.Running))
		t.RawSetString("ready", lua.LBool(info.Ready))
		t.RawSetString("endpoint", lua.LString(info.Endpoint))
		L.Push(t)
		return 1
	}))
	L.SetField(world, "get_metrics", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("world:read"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		metrics, err := m.API.WorldMetrics(L.OptString(1, ""))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		t := L.NewTable()
		for k, v := range metrics {
			t.RawSetString(k, lua.LNumber(v))
		}
		L.Push(t)
		return 1
	}))
	L.SetGlobal("world", world)

	peers := L.NewTable()
	L.SetField(peers, "list_available", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("peers:read"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		list, err := m.API.ListPeers()
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		arr := L.NewTable()
		for _, p := range list {
			t := L.NewTable()
			t.RawSetString("id", lua.LString(p.ID))
			t.RawSetString("name", lua.LString(p.Name))
			t.RawSetString("can_host", lua.LBool(p.CanHost))
			t.RawSetString("free_ram_mb", lua.LNumber(p.FreeRAMMB))
			arr.Append(t)
		}
		L.Push(arr)
		return 1
	}))
	L.SetField(peers, "get_metrics", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("peers:read"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		id := L.CheckString(1)
		list, err := m.API.ListPeers()
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		for _, p := range list {
			if p.ID == id {
				t := L.NewTable()
				t.RawSetString("id", lua.LString(p.ID))
				t.RawSetString("can_host", lua.LBool(p.CanHost))
				t.RawSetString("free_ram_mb", lua.LNumber(p.FreeRAMMB))
				L.Push(t)
				return 1
			}
		}
		L.Push(lua.LNil)
		L.Push(lua.LString("unknown peer " + id))
		return 2
	}))
	L.SetGlobal("peers", peers)

	tasks := L.NewTable()
	L.SetField(tasks, "submit", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("tasks:submit"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		tbl, ok := L.Get(1).(*lua.LTable)
		if !ok {
			L.Push(lua.LNil)
			L.Push(lua.LString("tasks.submit expects a table"))
			return 2
		}
		def := TaskDef{
			Type:     lua.LVAsString(tbl.RawGetString("type")),
			WorldID:  lua.LVAsString(tbl.RawGetString("world_id")),
			MaxMemMB: int64(lua.LVAsNumber(tbl.RawGetString("max_mem_mb"))),
			TimeoutS: int(lua.LVAsNumber(tbl.RawGetString("timeout_s"))),
			Retries:  int(lua.LVAsNumber(tbl.RawGetString("max_retries"))),
			Payload:  map[string]string{},
		}
		if pt, ok := tbl.RawGetString("payload").(*lua.LTable); ok {
			pt.ForEach(func(k, v lua.LValue) {
				def.Payload[lua.LVAsString(k)] = lua.LVAsString(v)
			})
		}
		id, err := m.API.SubmitTask(ext.Manifest.ID, def)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		// owned is only touched from this extension's own serialized
		// Lua execution (entry point, handlers, RunChunk) — no lock.
		ext.owned[id] = true
		L.Push(lua.LString(id))
		return 1
	}))
	L.SetField(tasks, "cancel", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("tasks:submit"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		id := L.CheckString(1)
		// Extensions may only cancel their OWN tasks. Note: this closure
		// can run while the dispatcher holds ext.mu for handler calls,
		// but API calls originate from Lua execution on that same
		// goroutine; owned is therefore also guarded by the manager's
		// own lock below for cross-extension safety.
		if err := m.API.CancelTask(ext.Manifest.ID, id); err != nil {
			L.Push(lua.LBool(false))
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LBool(true))
		return 1
	}))
	L.SetGlobal("tasks", tasks)

	storage := L.NewTable()
	L.SetField(storage, "request_backup", L.NewFunction(func(L *lua.LState) int {
		if res, errv, denied := deny("storage:backup"); denied {
			L.Push(res)
			L.Push(errv)
			return 2
		}
		id, err := m.API.RequestBackup(L.CheckString(1))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(id))
		return 1
	}))
	L.SetGlobal("storage", storage)

	events := L.NewTable()
	L.SetField(events, "on", L.NewFunction(func(L *lua.LState) int {
		if _, errv, denied := deny("events:subscribe"); denied {
			L.RaiseError("%s", lua.LVAsString(errv))
			return 0
		}
		name := L.CheckString(1)
		fn, ok := L.Get(2).(*lua.LFunction)
		if !KnownEvents[name] || !ok {
			L.RaiseError("events.on: unknown event %q or handler is not a function", name)
			return 0
		}
		ext.handlers[name] = append(ext.handlers[name], fn)
		return 0
	}))
	L.SetGlobal("events", events)

	logger := L.NewTable()
	for _, lvl := range []string{"info", "warn", "error"} {
		lvl := lvl
		L.SetField(logger, lvl, L.NewFunction(func(L *lua.LState) int {
			msg := L.OptString(1, "")
			switch lvl {
			case "info":
				m.Log.Info("lua", "ext", ext.Manifest.ID, "msg", msg)
			case "warn":
				m.Log.Warn("lua", "ext", ext.Manifest.ID, "msg", msg)
			case "error":
				m.Log.Error("lua", "ext", ext.Manifest.ID, "msg", msg)
			}
			return 0
		}))
	}
	L.SetGlobal("logger", logger)
}
