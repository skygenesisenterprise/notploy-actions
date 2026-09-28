# Notploy Deployment — GitHub Action

A GitHub Action that triggers a deployment on a [Notploy](https://github.com/skygenesisenterprise/notploy)
instance and, optionally, waits for it to finish so the workflow fails when the
deployment fails.

It is a Go program packaged as a container action: no Node.js, no pnpm and no
monorepo tooling is needed to run it.

> **Location.** The action currently lives in `packages/actions/` of the Notploy
> monorepo, and is written so it can be moved to
> `skygenesisenterprise/notploy-actions` as-is. See
> [Future migration](#future-migration-to-a-dedicated-repository).

---

## 1. What it does

```
GitHub Actions job
        │
        ├─ reads the runner context (repository, ref, sha, actor, workflow, run …)
        │
        ├─ POST /api/application.deploy        → triggers the deployment
        │
        ├─ GET  /api/deployment.all            → finds the deployment it created
        │                                       and follows its status
        │
        └─ writes step outputs, a job summary and readable log lines
```

With `wait: true` (the default) it polls until the deployment reaches a final
state. It exits non-zero only when the deployment fails or the wait cannot be
completed.

## 2. Requirements

- A Notploy instance reachable from the runner.
- A Notploy API key with, at least, the `deployment:create` and
  `deployment:read` permissions on the target application. The optional
  `deployment-url` output additionally needs read access to the application.
- A GitHub-hosted runner (or any runner with Docker), because the action is a
  container action.

## 3. Configuration

| Input | Required | Default | Description |
| --- | --- | --- | --- |
| `endpoint` | yes | — | Base URL of the Notploy instance, e.g. `https://notploy.example.com`. Do **not** append `/api`; it is added automatically. |
| `api-key` | yes | — | Notploy API key, sent as the `x-api-key` header. Always pass it via a secret. |
| `application-id` | yes | — | Identifier of the application to deploy. |
| `wait` | no | `true` | Wait for the deployment to reach a final state. |
| `timeout` | no | `30m` | Maximum time to wait, as a Go duration (`30m`, `90s`, `1h`). Only used when `wait` is `true`. |
| `poll-interval` | no | `5s` | Delay between two status lookups. Minimum `1s`. |

Durations use Go syntax: `s`, `m`, `h` (`1h30m`, `90s`). Days (`1d`) are not
supported.

## 4. Outputs

| Output | Description |
| --- | --- |
| `deployment-id` | Identifier of the triggered deployment. Empty when the deployment is not visible yet, or when the API key cannot read deployments. |
| `deployment-status` | Last observed status: `running`, `done`, `error`, `cancelled`, or `triggered` when `wait` is `false`. |
| `deployment-url` | Public URL of the application, when it has an enabled domain. |

### About `deployment-status`

These are the four statuses Notploy actually stores for a deployment
(`running`, `done`, `error`, `cancelled`). A deployment row is created with the
status `running`; Notploy has no separate `queued` state, and `done`/`error` are
what a successful/failed deployment ends as.

### About `deployment-url`

`POST /api/application.deploy` returns an empty body, so Notploy exposes no
per-deployment URL. `deployment-url` is therefore the application's first
enabled domain (`https` preferred), resolved from `GET /api/application.one`.
It is empty when the application has no domain yet, and resolving it is best
effort: a failure there never fails the step.

## 5. Minimal example

```yaml
name: Deploy

on:
  push:
    branches:
      - main

jobs:
  deploy:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - name: Deploy to Notploy
        id: deploy
        uses: skygenesisenterprise/notploy-actions@v1
        with:
          endpoint: ${{ secrets.NOTPLOY_URL }}
          api-key: ${{ secrets.NOTPLOY_API_KEY }}
          application-id: ${{ secrets.NOTPLOY_APPLICATION_ID }}
```

`wait` defaults to `true`, so this step fails if the deployment fails.

## 6. Example: waiting for the deployment

```yaml
      - name: Deploy to Notploy
        id: notploy
        uses: skygenesisenterprise/notploy-actions@v1
        with:
          endpoint: ${{ secrets.NOTPLOY_URL }}
          api-key: ${{ secrets.NOTPLOY_API_KEY }}
          application-id: ${{ secrets.NOTPLOY_APPLICATION_ID }}
          wait: true
          timeout: 30m
          poll-interval: 5s

      - name: Show deployment
        run: |
          echo "Deployment: ${{ steps.notploy.outputs.deployment-id }}"
          echo "Status: ${{ steps.notploy.outputs.deployment-status }}"
          echo "URL: ${{ steps.notploy.outputs.deployment-url }}"
```

Use these outputs to annotate the run, comment on a pull request, post a
notification, or send the URL to a smoke-test step.

## 7. Example: Docker build + Notploy deployment

The recommended production flow: build and push the image in CI, then ask
Notploy to deploy that exact tag. The Notploy server then only pulls and runs
the image instead of building it.

```yaml
name: Build and deploy

on:
  push:
    branches:
      - main

jobs:
  deploy:
    runs-on: ubuntu-latest

    steps:
      - name: Check out repository
        uses: actions/checkout@v4

      - name: Log in to the registry
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push the image
        uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: ghcr.io/${{ github.repository }}:${{ github.sha }}

      - name: Deploy to Notploy
        id: notploy
        uses: skygenesisenterprise/notploy-actions@v1
        with:
          endpoint: ${{ secrets.NOTPLOY_URL }}
          api-key: ${{ secrets.NOTPLOY_API_KEY }}
          application-id: ${{ secrets.NOTPLOY_APPLICATION_ID }}
          wait: true
          timeout: 15m

      - name: Smoke test
        run: curl --fail --retry 5 --retry-delay 5 "${{ steps.notploy.outputs.deployment-url }}"
```

The application in Notploy must be configured with the `Docker` source type
pointing at the tag pushed above. If the tag changes per commit, update it
through `application.update` before deploying, or use a moving tag such as
`latest`.

## 8. Handling secrets

- Store the endpoint, the API key and the application id as repository or
  environment secrets and reference them with `${{ secrets.NAME }}`. Never put
  them in the workflow file or in `with:` as literals.
- The action registers the API key with `::add-mask::` as its very first
  action, so the runner redacts it from every later log line — including lines
  produced by the action itself and by other steps.
- The API key is never written to `$GITHUB_OUTPUT`, to `$GITHUB_STEP_SUMMARY`,
  to an error message or to any annotation. Error messages only carry the
  operation, the HTTP status and the public message Notploy returned.
- The action never prints request headers, and if a Notploy error body ever
  echoed the credential back, it would be redacted before being displayed.
- `GITHUB_TOKEN` is not used: the action only talks to Notploy. Give the job
  `permissions: contents: read` unless it needs more.

## 9. Behaviour on failure

| Situation | Step result |
| --- | --- |
| Deployment reaches `done` | success |
| Deployment reaches `error` | **failure** — the step exits non-zero, with the deployment id, status and Notploy's `errorMessage` |
| Deployment reaches `cancelled` | success, with a warning annotation (a cancelled deployment is not a CI failure) |
| `timeout` reached while still `running` | **failure**, after reporting the last observed status |
| Job cancelled (`SIGTERM`) | **failure**, the wait stops immediately |
| Trigger rejected (401/403/400/404) | **failure**, with an actionable hint about which input to check |
| Temporary network or 5xx/429 error while polling | retried a few times, then the step fails if the instance stays unreachable |

Outputs are written even when the deployment fails, so later steps can still
link to the failed deployment. The job summary is written in every case.

## 10. Local development

```bash
cd packages/actions

# Run the action outside of GitHub Actions by providing its inputs directly.
INPUT_ENDPOINT=https://notploy.example.com \
INPUT_API-KEY=npk_xxx \
INPUT_APPLICATION-ID=application-id \
go run ./cmd/notploy-action
```

Environment variables map one-to-one onto the inputs, upper-cased and prefixed
with `INPUT_` (`endpoint` → `INPUT_ENDPOINT`). Input names containing a hyphen
are accepted both hyphenated (`INPUT_API-KEY`, what the runner sets for
container actions) and underscored (`INPUT_API_KEY`).

`GITHUB_OUTPUT` and `GITHUB_STEP_SUMMARY` are optional: when they are unset, the
action runs normally and simply does not publish files. The `GITHUB_*` context
variables are optional too — the action falls back to a generic deployment
title when it is not running inside a runner.

## 11. Tests

```bash
cd packages/actions

go test ./...
```

The suite is deterministic, needs no network and no credentials:

- `internal/config` — required inputs, defaults, invalid durations and
  booleans, and that no input value can leak into an error message.
- `internal/github` — context parsing, input reading, step outputs, job
  summary and workflow-command escaping.
- `internal/notploy` — the client against an `httptest` server: every HTTP
  status class, invalid JSON, timeouts, unreachable instances, retry policy,
  endpoint validation and credential redaction.
- `internal/deployment` — the lifecycle with a scripted fake client:
  `running → done`, `running → error`, `running → cancelled`, timeout, context
  cancellation, transient lookup failures and URL resolution.
- `cmd/notploy-action` — an end-to-end test that builds the real binary and
  runs it against a fake Notploy instance, asserting exit codes, outputs and
  the summary.

## 12. Build

The action ships as a container image built from [`Dockerfile`](./Dockerfile):

```bash
cd packages/actions

# Binaries (the runner's platform is used inside Docker, so no GOOS/GOARCH is forced).
go build -o notploy-action ./cmd/notploy-action

# The image the runner builds from `action.yml`.
docker build -t notploy-action:local .
```

The module has **no third-party dependencies** — only the Go standard library —
so there is deliberately no `go.sum`, and builds need no network access. The
module path is `github.com/skygenesisenterprise/notploy-actions`, i.e. the future
dedicated repository, so extracts require no import changes.

Reproducibility measures: pinned `golang:1.24-alpine` and `alpine:3.21` base
images, `-trimpath`, a static binary (`CGO_ENABLED=0`), and a `VERSION` build
argument injected with `-ldflags`.

## 13. Versioning

The action is designed for the usual GitHub Action versioning scheme:

```yaml
uses: skygenesisenterprise/notploy-actions@v1     # moving major tag
uses: skygenesisenterprise/notploy-actions@v1.2.0 # exact release
```

This repository is already the dedicated repository, so the action is always
referenced by that endpoint — never by a monorepo path.

No versioning mechanism is implemented inside the action itself: a release is a
Git tag on this repository, plus a moving `v1` tag pointing at the latest
`v1.x.x`.

## 14. Future migration to a dedicated repository

`packages/actions/` is written to become the root of
`github.com/skygenesisenterprise/notploy-actions` with minimal changes.

What to do when extracting:

1. Move the contents of `packages/actions/` to the new repository root.
   `go.mod` already declares the future module path, `action.yml` already
   references `Dockerfile` relatively, and every import is intra-module — no
   code change is required.
2. Move `.github/workflows/notploy-action.yml` of this monorepo to
   `.github/workflows/ci.yml` of the new repository and drop the `paths`
   filters. The workflow lives at the monorepo root today because GitHub only
   runs workflows from the repository root; a nested
   `packages/actions/.github/workflows/` would never run.
3. Point the `uses:` references in consumer workflows, and in the Notploy
   documentation, at `skygenesisenterprise/notploy-actions@v1`.
4. Publish the moving `v1` tag as described in [Versioning](#13-versioning).
5. Optionally pass the release version to the build
   (`docker build --build-arg VERSION=1.2.0`) so it shows up in the Notploy
   `User-Agent` header.

Dependencies on the monorepo: **none**. The action does not read
`package.json`, does not use pnpm, does not import anything from another
package, and does not require any path relative to the monorepo root. It only
depends on the public Notploy HTTP API described in `openapi.json`.

## Architecture

```
packages/actions/
├── action.yml                     # Action metadata: inputs, outputs, Docker entrypoint
├── Dockerfile                     # Multi-stage build of the Go binary
├── cmd/notploy-action/main.go     # Thin entry point: wiring only
└── internal/
    ├── config/                    # Action inputs → validated configuration
    ├── github/                    # Everything GitHub-specific: context, inputs,
    │                              # outputs, summary, workflow commands
    ├── notploy/                   # Notploy HTTP client: trigger, list, get
    ├── deployment/                # Deployment lifecycle: trigger, poll, result
    └── version/                   # Reported version
```

The layering is deliberate:

- `internal/notploy` and `internal/deployment` know nothing about GitHub
  Actions and read no environment variable. `internal/deployment` receives its
  parameters and its logger, which is what makes the lifecycle reusable and
  unit-testable without a runner.
- `internal/github` is the only package that touches `GITHUB_*` and `INPUT_*`.
- `cmd/notploy-action/main.go` only wires them together: load the config, build
  the client, run the lifecycle, publish the results, choose the exit code.

## Notploy API notes

- The API is the tRPC-to-OpenAPI surface of a Notploy instance, served under
  `<endpoint>/api`, authenticated with an `x-api-key` header.
- Endpoints used: `POST /api/application.deploy`,
  `GET /api/deployment.all?applicationId=…`,
  `GET /api/application.one?applicationId=…`.
- `POST /api/application.deploy` returns `200` with an **empty body**. The
  action therefore snapshots the existing deployments before triggering, then
  identifies the new one as the newest deployment that was not in the snapshot.
  There is no endpoint that returns the id directly; nothing is invented to
  work around that.
- `GET /api/deployment.all` returns deployments **newest first**, which is what
  makes that identification reliable.
- Errors are read from Notploy's public `{ "message": …, "code": … }` body when
  it is available, and fall back to a truncated, single-line snippet otherwise.

## License

See the repository [`LICENSE`](../../LICENSE).
