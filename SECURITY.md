# Security policy

## Status and trust model

jobd is experimental software for **trusted users and trusted commands**. It is
not a sandbox and is not ready for untrusted, multi-tenant hosting. Security fixes
target the latest source/release; older versions have no maintenance guarantee.

- A controller has one shared `JOBD_API_KEY`. Anyone holding it has access to
  every queue and can submit commands, manage jobs, and perform worker operations.
  Queue names isolate storage, not authorization. Do not share a controller/key
  between unrelated customers. Use separate deployments, keys and worker accounts
  or machines for separate trust domains.
- Jobs execute as the worker's operating-system user with its filesystem and
  network access. Run workers unprivileged and away from sensitive workloads.
  Cancellation/process groups do not provide containment against malicious jobs.
- The worker removes `JOBD_API_KEY` from child job environments as defense in
  depth. This does not prevent same-user code from obtaining credentials through
  other mechanisms. Other inherited environment variables can contain secrets;
  keep the worker environment minimal.
- Local fallback submissions use an owner-only Unix socket served by the worker,
  not the controller API. The CLI never directly reads/writes queue files. Treat
  anyone who can access the socket or write the worker's state directory as
  authorized to execute commands. Pending commands and
  their results are persisted there; do not store secrets in
  command arguments. Local jobs have the same unsandboxed privileges as remote
  jobs.
- The installer and `jobd --restart` import configuration into the systemd user
  manager's environment. Other user services may inherit it. Use a dedicated
  account. No persistent key file is created by these commands.
- Use HTTPS outside localhost and a unique strong random API key. Do not put keys
  in submitted command arguments, public issues, logs, or source control. Command
  arguments are stored by the controller and may appear in logs. Output remains
  on workers and must be managed according to your data-retention requirements.
- The public default controller hostname is not a credential. Self-hosters should
  set `JOBD_CONTROLLER` explicitly. Do not test against the hosted service without
  authorization. Public deployment also needs appropriate abuse/rate controls;
  authentication alone does not prevent resource-exhaustion attacks.

## Reporting a vulnerability

Do not post credentials or exploit details in a public issue. Email
**gzs_iceberg@outlook.com** with the affected version, impact, and a minimal
reproduction using dummy credentials. Coordinate disclosure privately; no
response-time SLA is promised.

## Handling secrets

`.env`, `.env.*`, `.dev.vars`, `.dev.vars.*`, and `tooling/env.sh` are ignored;
example files must contain placeholders only. Gitignore does not remove files
already committed. Before publishing, review Git history, release artifacts,
Actions logs, and local changes. If a real credential was ever exposed, rotate it
first; deleting a file or rewriting history is not sufficient remediation.

Generate keys with `uv run tooling/generate-api-key.py`. Keep the same key in the
controller secret and its trusted clients/workers. Rotate it by updating the
controller secret and restarting clients/workers with the new environment.
