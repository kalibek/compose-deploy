# compose-deploy

A small self-hosted service that deploys **docker compose stacks straight from GitHub repositories**.
Point it at a repo + branch + compose file; it pulls new commits (by polling, or instantly via a GitHub
webhook) and runs `docker compose` against the host's Docker daemon. One Go binary with an embedded React UI,
SQLite storage, single admin password.

## Quick start

```sh
cp .env.example .env
# edit .env: set ADMIN_PASSWORD and APP_SECRET (e.g. `openssl rand -hex 32`)
docker compose up -d
```

Open `http://<host>:3000` and log in with `ADMIN_PASSWORD`.

The compose file pulls the prebuilt image `kalibek/compose-deploy:latest` from Docker Hub (published on every
push to `main`). To build from source instead, swap `image:` for the commented `build:` block.

The compose file reads `ADMIN_PASSWORD` / `APP_SECRET` from `.env` (`${ADMIN_PASSWORD:?...}` fails fast if
unset). You can also inline them directly in `docker-compose.yml` under `environment:` if you prefer.

## GitHub access token (private repos)

Public repositories need no token — leave the field empty.

For private repos, the recommended credential is a **fine-grained personal access token** scoped to just the
repositories you deploy, read-only:

1. GitHub → **Settings** → **Developer settings** → **Personal access tokens** → **Fine-grained tokens** →
   **Generate new token**.
2. **Resource owner**: your user, or the organization that owns the repo.
3. **Expiration**: pick one (and put a reminder in your calendar to rotate it).
4. **Repository access**: **Only select repositories** → select the repo(s).
5. **Permissions** → **Repository permissions** → **Contents: Read-only**.
   (*Metadata: Read-only* is added automatically.) For stacks that
   [deploy after a workflow](#images-built-by-github-actions), also add **Actions: Read-only**.
6. Generate, copy the `github_pat_...` value and paste it into the stack's *Access token* field.

If the resource owner is an organization, it may require an admin to approve the token before it works.

Why this over the alternatives: a *classic* PAT with `repo` scope grants read **and write** access to every
repository you can reach; a *deploy key* is per-repository SSH, but compose-deploy only speaks HTTPS. A
fine-grained token gives least privilege, an expiry, and per-repo selection. The token is stored encrypted and
passed to git only via an HTTP header in the environment (never on the command line or in `.git/config`).

## GitHub webhook (optional)

Without a webhook, compose-deploy polls each auto-deploy stack every `POLL_INTERVAL` (default 60s) with
`git ls-remote` — that works on its own. A webhook just makes deploys instant.

Repo → **Settings** → **Webhooks** → **Add webhook**:

- **Payload URL**: `https://<your-host>/api/hooks/github/<id>` (shown on the stack page)
- **Content type**: `application/json`
- **Secret**: the webhook secret shown once when the stack is created (you can rotate it on the stack page)
- **Events**: *Just the push event* — or, for stacks that deploy after a workflow, *Let me select individual
  events* → **Workflow runs**

Pushes (or successful workflow runs) on the stack's branch enqueue a deploy if auto-deploy is on. The endpoint
must be reachable from GitHub; signatures (`X-Hub-Signature-256`) are verified.

## Private registries

Add credentials on the **Registries** page; all of them are applied to every deploy (via a temporary
`DOCKER_CONFIG`).

| Registry | URL | Username | Password |
|---|---|---|---|
| GitHub Container Registry | `ghcr.io` | GitHub username | PAT (classic) with `read:packages` |
| Docker Hub | `docker.io` (or `https://index.docker.io/v1/`) | Docker Hub user | access token |
| Others (GitLab, ECR, self-hosted, ...) | registry host, e.g. `registry.gitlab.com` | user | password / token |

## Environment variables

Each stack's variables are edited either **one by one** or as **`.env` text** (toggle above the editor; the
choice is remembered per browser). The text format is the single-line subset of compose's `.env` syntax:
`KEY=value` per line, `#` comments, optional `export`, `'single'` quotes taken literally and `"double"` quotes
with `\"`, `\\` and `\$` escapes. Multi-line values aren't supported. Errors are reported per line and block
saving. Stored values are never sent back to the browser: saved variables show as `KEY=<unchanged>` — replace
the marker to change a value, delete the line to remove the variable.

## Stacks without git: compose editor

For a quick stack you don't want to keep in a repository, pick **Compose editor** as the source when creating
it and write the `docker-compose.yml` in the browser (YAML editor with syntax highlighting).

- **Validate** runs `docker compose config` on the draft, using the stack's environment variables.
- With auto-deploy on, the stack is deployed when created and whenever a **changed** file is saved; otherwise
  use *Save & deploy* / *Deploy now*. There's nothing to poll and no webhook.
- The file is stored in the database and written to `DATA_DIR/stacks/<name>/inline/docker-compose.yml` for
  each deploy; that directory is the compose project directory, so relative paths resolve there (see the
  path gotcha below). Environment variables, private registries, container logs and the shell work the same
  as for git stacks.
- The stack page shows the deployed revision (a hash of the file) and flags saved changes that aren't deployed
  yet. The source can't be changed after creation.

## Images built by GitHub Actions

If your images are built by a GitHub Actions pipeline, deploying on push is too early: the new images don't
exist yet, so compose would pull the previous `latest` or fail. Set the stack's **Deploy a new commit…**
option to **After its GitHub Actions workflow succeeds**:

- The poller sees the new commit, then asks the GitHub API about the commit's workflow runs, and waits
  (the stack shows *waiting for CI* and why) until they finished. Only a successful run is deployed, and it
  deploys **exactly that commit**, even if the branch has moved on since. Failed or cancelled runs aren't
  deployed; a later re-run that succeeds is. After a failure the poller re-checks that commit with
  exponential backoff (2× the poll interval, doubling up to 30 minutes) instead of on every tick, so a
  successful re-run may take that long to be picked up without a webhook. A new commit is checked right away.
- Optionally name the workflow that builds the images (`build` or `build.yml`); otherwise *every* workflow
  run of the commit must succeed. Pull-request runs are ignored.
- The token needs **Actions: Read-only** in addition to *Contents: Read-only*. Public repos work without a
  token, but the unauthenticated API limit (60 requests/hour) is tight — a token is recommended.
- With a webhook, subscribe to **Workflow runs** instead of pushes and deploys start as soon as the run
  finishes. Without one, the next poll picks it up.

Every deploy sets `COMMIT_SHA` and `COMMIT_SHORT_SHA` (7 chars) for compose variable substitution, so
tagging images with the commit makes deploys exact:

```yaml
# .github/workflows/build.yml
name: build
on:
  push:
    branches: [main]
jobs:
  build:
    runs-on: ubuntu-latest
    permissions: { contents: read, packages: write }
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with: { registry: ghcr.io, username: "${{ github.actor }}", password: "${{ secrets.GITHUB_TOKEN }}" }
      - uses: docker/build-push-action@v6
        with:
          push: true
          tags: ghcr.io/${{ github.repository }}:${{ github.sha }}
```

```yaml
# docker-compose.yml
services:
  app:
    image: ghcr.io/acme/app:${COMMIT_SHA}
```

Private images need registry credentials (see [Private registries](#private-registries)). These variables
take precedence over a `COMMIT_SHA` in the stack's environment variables.

## How a deploy works

For each stack (compose **project name = stack name**), under `DATA_DIR/stacks/<name>/repo`:

1. **git sync** — clone, or fetch + `checkout --force` + `clean -fd` to the branch head, or to the exact
   commit whose workflow succeeded (ignored files survive).
2. **.env** — if the stack has environment variables, write them to `.env` next to the compose file
   (overrides a committed `.env`); they are then available for `${VAR}` substitution in the compose file,
   together with `COMMIT_SHA` / `COMMIT_SHORT_SHA`.
3. `docker compose down --remove-orphans`
4. `docker compose pull`
5. `docker compose up -d --remove-orphans`

Only compose flags common to docker and podman (`-p`, `-f`) are used. Every command runs in the compose
file's directory, so relative paths, `.env` and `${VAR}` substitution resolve there.

Deploys of the same stack are serialized; the log of the last run is shown in the UI. *Stop* runs
`docker compose down`; deleting a stack can optionally run `down` first.

### Deployment history

Every deploy and stop run is recorded in the stack's **Deployment history**: when it started, how long it
took, what triggered it, the commit (or inline compose revision) it deployed, its outcome and its full log
(click a row). Triggers are:

| Trigger | Meaning |
|---|---|
| Manual | *Deploy now* / *Stop* in the UI |
| Webhook | GitHub push, or successful workflow run(s), delivered by the webhook |
| Poller | the poller saw a new commit on the branch (and, for workflow stacks, its workflow succeeded) |
| Compose saved | an inline stack was created or its compose file was saved |

The last 100 runs per stack are kept (`GET /api/stacks/{id}/deployments`, and
`/api/stacks/{id}/deployments/{deploymentId}` for one run including its log). History is deleted with the stack.

### Containers, logs and shell

The stack page lists the project's containers (`docker ps` filtered by the compose project label) with state,
health, image and ports. For each container you can:

- **Logs** — `docker logs --timestamps --tail N [--follow]`, streamed to the browser as Server-Sent Events.
- **Shell** — an interactive terminal (xterm.js) over a WebSocket, backed by `docker exec -it <id> bash`
  (falls back to `sh`; images without a shell, e.g. distroless, can't be entered). Closing the page ends the
  session.

Behind a reverse proxy, enable WebSocket upgrades for `/api/stacks/*/containers/*/exec` and disable response
buffering for `/logs` (compose-deploy sends `X-Accel-Buffering: no` for nginx). The proxy must keep the
original `Host` header: WebSocket handshakes whose `Origin` doesn't match `Host` are rejected.

### Path gotcha: relative bind mounts

`DATA_DIR` is stored in the named volume `compose_deploy_data` by default. That's fine for stacks that use
named volumes, absolute host paths, `env_file`, `build:` contexts or `configs`/`secrets` files — those are read
inside the compose-deploy container.

It is **not** fine for relative bind mounts (`./data:/data`). compose-deploy runs `docker compose` inside its
container, but containers are created by the **host's** engine: the relative path is resolved inside the
compose-deploy container (e.g. `/opt/compose-deploy/stacks/app/repo/data`) and then interpreted **on the
host**, where it doesn't exist (the engine silently creates an empty directory). If your stacks need that,
replace the named volume with a bind mount at the identical path on both sides:

```yaml
    volumes:
      - /opt/compose-deploy:/opt/compose-deploy   # and keep DATA_DIR=/opt/compose-deploy
```

## Podman (incl. macOS / arm64)

compose-deploy talks to the engine through the Docker API socket, which Podman provides. Two differences:

- **Socket path.** With `podman machine` (macOS, Windows) containers run inside a Linux VM, so the left side
  of the socket mount is a path **inside the VM**, not on your Mac. Rootless machines (the default) serve the
  API at `/run/user/<uid>/podman/podman.sock`; the VM's `/var/run/docker.sock` is the rootful socket and gives
  *permission denied* from a rootless container. Find the right path and set it in `.env`:

  ```sh
  podman info --format '{{.Host.RemoteSocket.Path}}'   # e.g. unix:///run/user/501/podman/podman.sock
  echo 'DOCKER_SOCK=/run/user/501/podman/podman.sock' >> .env
  ```

  On a rootful machine (`podman machine set --rootful`) or a Linux host with `podman.socket` enabled, use
  `/run/podman/podman.sock` (rootful) or `$XDG_RUNTIME_DIR/podman/podman.sock` (rootless).
- **SELinux.** The machine VM (Fedora CoreOS) enforces SELinux, which blocks containers from using host
  sockets. `docker-compose.yml` sets `security_opt: [label=disable]` for this; keep it.

Start it with `podman compose up -d` (or `docker compose` pointed at the Podman socket). Check the
connection with `podman exec <container> docker version` — the server should report Podman.

Other notes:

- **Bind mounts on macOS** are paths inside the VM too. The VM only shares `/Users`, so `/opt/...` doesn't exist
  there — keep the default named volume, or for relative bind mounts use a same-path bind under `/Users`
  (`/Users/<you>/compose-deploy:/Users/<you>/compose-deploy` and set `DATA_DIR` to that path).
- **arm64**: compose-deploy's image builds natively. Images in your stacks need arm64 variants, or set
  `platform: linux/amd64` on the service (emulated, slower).

## Security notes

- **The container has the Docker socket, which is root on the host.** Anyone who can log in (or push to a
  deployed repo's branch) can run arbitrary containers, including ones that mount `/`. Treat the admin
  password and the repos you deploy accordingly.
- Put it behind a **TLS reverse proxy** (Caddy, Traefik, nginx) and set `COOKIE_SECURE=true`. Don't expose
  port 3000 publicly without TLS. If the proxy sets `X-Forwarded-For`, set `TRUST_PROXY=true` so login rate
  limiting sees real client IPs (never enable it without such a proxy — the header is spoofable).
- **Keep `APP_SECRET` stable.** It encrypts stored tokens, env values, registry passwords and webhook secrets.
  Changing it makes all of them undecryptable (you'd have to re-enter them) and logs everyone out. Changing
  `ADMIN_PASSWORD` also invalidates existing sessions.
- Secrets are never returned by the API (only `has_token`, `env_keys`, `has_password`) and are scrubbed from
  deploy logs.
- Mutating API requests must be `Content-Type: application/json` (CSRF protection on top of SameSite cookies).
- The container shell gives whoever is logged in a root shell in your containers; exec sessions are logged
  (stack + container) but their contents are not.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ADMIN_PASSWORD` | — (required) | Password for the web UI. |
| `APP_SECRET` | — (required) | Encryption + session signing key. Keep stable. `openssl rand -hex 32`. |
| `DATA_DIR` | `/opt/compose-deploy` | Database and stack checkouts (see *Path gotcha*). |
| `LISTEN_ADDR` | `:3000` | HTTP listen address. |
| `POLL_INTERVAL` | `60s` | How often to check branches (Go duration, min `1s`). |
| `COOKIE_SECURE` | `false` | Set `true` when served over HTTPS. |
| `TRUST_PROXY` | `false` | Use `X-Forwarded-For`/`X-Real-IP` for the client IP (only behind a proxy). |
| `DOCKER_COMMAND` | `docker` | Container CLI binary used for `compose`, `ps`, `logs` and `exec` (e.g. `podman`). Must be installed in the image. |
| `DOCKER_SOCK` | `/var/run/docker.sock` | *Compose file only*: engine socket path on the engine host, mounted into the container (see *Podman*). |

## Local development

Requires Go 1.25+, Node 24+, and `git` + `docker` (with the compose plugin) on `PATH` for actual deploys.

```sh
# API on :3000 (serves a placeholder page at /; the API is under /api)
cd api && ADMIN_PASSWORD=x APP_SECRET=y DATA_DIR=./.data go run .

# UI dev server with hot reload; proxies /api to :3000
cd ui && npm install && npm run dev
```

Checks: `cd api && go vet ./... && go test ./...`, `cd ui && npm run build && npm run lint`.
The image build (`docker compose build`) embeds `ui/dist` into the Go binary.

## License

[MIT](LICENSE)
