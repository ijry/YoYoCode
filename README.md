# YoYoCode

A native Go desktop workspace for multi-agent AI coding — aggregate sessions
from Claude Code, Codex, OpenCode, Pi, Grok Build and more in one window.

## Shape

| Path | What |
|---|---|
| `src-go/` | Server: HTTP/WebSocket layer, ACP engine, SQLite, command handlers |
| `src/` | mygo native UI (mirrors the upstream page tree) |
| `cmd/yoyocode/` | Desktop app — in-process backend + native window |
| `cmd/yoyocode-server/` | Standalone server binary |
| `cmd/yoyocode-mcp/` | MCP companion binary |
| `scripts/` | `gen_inventory.js` (route checklist), `smoke.js` (protocol smoke test) |
| `_upstream/` | Local reference clones — **not** part of the repo |

The UI never reaches into the database: it speaks the same loopback
`/api/*` + `/ws/events` contract the mobile client uses, so the two clients
exercise one wire.

## Build

```bash
go build ./...                                                # compile check
go vet ./... && go test ./...                                 # verify
go run github.com/egoist/mygo/cmd/mygo dev ./cmd/yoyocode     # live-reload dev
go run github.com/egoist/mygo/cmd/mygo build ./cmd/yoyocode   # packaged app
```

Requires Go 1.27+. The UI toolkit (mygo) is a normal module dependency fetched
over git; if the module proxy is unreachable, `GOPROXY=direct go mod download`
works.

## Docs

- `docs/REPLICATION_PLAN.md` — the contract: goals, directory mapping, wire
  compatibility rules.
- `docs/ROUTE_INVENTORY.md` — every route, ticked off; regenerate with
  `node scripts/gen_inventory.js`.
- `docs/PROGRESS.md` — milestones and the rolling work log.
