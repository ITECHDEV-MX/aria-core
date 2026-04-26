[← Back to AriaCore Cloud](./README.md)

# AriaCore Cloud Quickstart

**Fastest working setup:** run the compose smoke profile, enroll one project, and sync explicitly.

This page gives one recommended path first. Advanced/authenticated mode follows after.

---

## Recommended Path: Local Smoke (Docker Compose)

### 1) Start cloud runtime + Postgres

```bash
docker compose -f docker-compose.cloud.yml up -d
```

`docker-compose.cloud.yml` defaults on this branch:
- `ARIA_CORE_CLOUD_INSECURE_NO_AUTH=1`
- `ARIA_CORE_CLOUD_ALLOWED_PROJECTS=smoke-project`
- cloud endpoint published at `http://127.0.0.1:18080`

### 2) Configure CLI cloud endpoint

```bash
aria-core cloud config --server http://127.0.0.1:18080
```

### 3) Enroll explicit project

```bash
aria-core cloud enroll smoke-project
```

### 4) Sync explicitly in cloud mode

```bash
aria-core sync --cloud --project smoke-project
aria-core sync --cloud --status --project smoke-project
```

### 5) Verify browser dashboard

Open:
- `http://127.0.0.1:18080/dashboard`

In compose smoke mode, `/dashboard/login` redirects to `/dashboard/` (no bearer login needed).

---

## Existing Project Upgrade Path (recommended)

Use this sequence before first bootstrap for established local projects:

```bash
aria-core cloud upgrade doctor --project smoke-project
aria-core cloud upgrade repair --project smoke-project --dry-run
aria-core cloud upgrade repair --project smoke-project --apply
aria-core cloud upgrade bootstrap --project smoke-project --resume
aria-core cloud upgrade status --project smoke-project
```

`rollback` is only available before bootstrap reaches `bootstrap_verified`.

---

## Common Failure Reasons

| Reason code | Meaning |
|---|---|
| `blocked_unenrolled` | Project is not enrolled for cloud replication |
| `auth_required` | Authenticated runtime requires valid token/session |
| `cloud_config_error` | Cloud endpoint config is missing/invalid |
| `policy_forbidden` | Project blocked by cloud policy |
| `paused` | Project sync paused in cloud control plane |
| `transport_failed` | Cloud transport/network operation failed |

---

<details>
<summary><strong>Advanced: Authenticated Source-Run Mode</strong></summary>

Use this when you are running `aria-core cloud serve` directly (no insecure compose smoke mode):

```bash
ARIA_CORE_DATABASE_URL="postgres://aria-core:aria-core_dev@127.0.0.1:5433/aria-core_cloud?sslmode=disable" \
ARIA_CORE_JWT_SECRET="replace-with-32+-byte-random-secret" \
ARIA_CORE_CLOUD_TOKEN="your-token" \
ARIA_CORE_CLOUD_ALLOWED_PROJECTS="my-project" \
aria-core cloud serve
```

Then configure client endpoint + token:

```bash
aria-core cloud config --server http://127.0.0.1:8080
export ARIA_CORE_CLOUD_TOKEN="your-token"
aria-core cloud enroll my-project
aria-core sync --cloud --project my-project
```

Rules that matter:
- `ARIA_CORE_CLOUD_INSECURE_NO_AUTH=1` cannot be combined with `ARIA_CORE_CLOUD_TOKEN`
- `ARIA_CORE_CLOUD_ALLOWED_PROJECTS` is required server-side in both modes
- authenticated mode requires explicit non-default `ARIA_CORE_JWT_SECRET`

</details>

---

## Next Steps

- Deep runtime/env reference: [DOCS.md — Cloud CLI](../../DOCS.md#cloud-cli-opt-in)
- Background sync mode: [DOCS.md — Cloud Autosync](../../DOCS.md#cloud-autosync)
- Branding assets and usage: [Branding](./branding.md)
