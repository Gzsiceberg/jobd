# jobd

A small pull-based job scheduler: submit commands to named queues, then execute them on remote Linux workers.

**Experimental — trusted workloads only.** Not designed for untrusted multi-tenant hosting. See [Security](SECURITY.md).

```text
jobd CLI → Cloudflare Controller ← Worker → Local Process
```

- **Controller:** TypeScript/Hono API with a separate SQLite-backed Durable Object for each queue.
- **CLI:** Go `jobd` client with tsp-style submission, listing, output inspection, cancellation and queue management.
- **Worker:** Go daemon that executes one command at a time, buffers progress updates and handles process-group shutdown.

Job lifecycle: `queued → running → succeeded | failed`.

## Documentation

| Guide | Contents |
| --- | --- |
| [Installation](docs/installation.md) | Public GitHub releases, upgrades and uninstalling |
| [CLI](cli/README.md) | Commands, configuration and listing output |
| [Worker](worker/README.md) | Execution, progress sockets, cancellation and source layout |
| [Controller](controller/README.md) | HTTP API, storage, deployment and limitations |
| [Development](docs/development.md) | Local setup, checks and end-to-end tests |
| [Releases](docs/releases.md) | Packaging and publishing GitHub releases |

**Security:** All controller routes require the shared `JOBD_API_KEY` (set it in the controller, CLI and worker environments). The key grants access to every queue; queue names are not authorization boundaries. There is no execution sandbox. Use HTTPS, keep the key private and run workers as unprivileged users. See [deployment and limits](controller/README.md#deployment-and-limits) and the [security policy](SECURITY.md).

## License

[MIT](LICENSE). Self-hosting and commercial use are permitted; hosted service access is separate.
