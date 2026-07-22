# Stratum Studio

Desktop operator console for managing multiple deployed Stratum projects — built with Wails v3 (Go + React). It connects directly to each project's PostgreSQL, RabbitMQ, and Redis; it does not depend on the API.

## Documentation

Full documentation lives with the rest of the project docs:

- **[`docs/13-studio.md`](../../docs/13-studio.md)** — what Studio is, how it connects, features, and how to run it.

See also the other docs in [`docs/`](../../docs/) — architecture, database, billing, and messaging are the most relevant when working on Studio's queries and catalog tooling.

## Quick start

```bash
# from ui/studio/
task dev        # Wails v3 hot reload (regenerates bindings)
task build      # release build for the current OS
task package    # signed + packaged build
```

Requires Go 1.25+ and the `wails3` CLI. Studio stores its own project registry in local SQLite and needs only the target project's `/health` endpoint reachable (plus DB/MQ/Redis DSNs entered in the app).
