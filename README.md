# jobd

**Queue jobs once. Run them across your machines.**

jobd distributes commands across remote Linux instances. Submit jobs to a shared queue; available workers pick them up, one job at a time.

**Experimental — trusted workloads only.** Not designed for untrusted multi-tenant hosting.

## Why I built it

I run ML training jobs on RunPod and Vast.ai, with metrics tracked in W&B. Launching jobs across instances was still a hassle. `tsp` (Task Spooler) works well on one machine; I wanted that simplicity across many.

**CLI-first, not a UI:** agents can submit, inspect, and cancel jobs through commands, without navigating a dashboard.

## When should you use it?

Use jobd when you have multiple prepared instances and independent training runs or batch tasks to distribute—without SSHing into each machine to launch them.

You provide the machines, code, and data. jobd dispatches jobs; it does not provision GPUs, match hardware requirements, or coordinate distributed training. For one machine, `tsp` may be enough.

## How it works

```text
jobd CLI → Cloudflare Controller ← Worker → Local Process
```

- **Controller:** TypeScript/Hono API with a separate SQLite-backed Durable Object for each queue.
- **CLI:** Go `jobd` client with tsp-style submission, listing, output inspection, cancellation and queue management.
- **Worker:** Go daemon that executes one command at a time, buffers progress updates and handles process-group shutdown.
- **Local fallback:** `jobd --local COMMAND...` queues work on this machine, eligible after 30 seconds without controller jobs. See [local jobs](cli/README.md#local-fallback-jobs).

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

**Security:** All controller routes require the shared `JOBD_API_KEY` (set it in the controller, CLI and worker environments). The key grants access to every queue; queue names are not authorization boundaries. There is no execution sandbox. Use HTTPS, keep the key private and run workers as unprivileged users. See [deployment and limits](controller/README.md#deployment-and-limits).

## License

[MIT](LICENSE). Self-hosting and commercial use are permitted; hosted service access is separate.
