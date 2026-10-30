# FE builds (care_fe)

care-pi serves care_fe as static files. There is no FE container.
Caddy serves the directory `/var/lib/care-pi/fe/current` with a fallback to `index.html`.
`current` is a symlink to one build directory, `/var/lib/care-pi/fe/<build id>/`.
A **provider** makes the build. `fe.provider` in `config.yaml` selects the provider.

- How apply works: `docs/apply.md`.
- The config keys: `docs/config.md`, section `fe`.
- The measurements: `plans/08-spike-log.md`, spike SP4 (build) and SP5 (same origin).

## Providers

| Provider | What it does | Network | Time |
|---|---|---|---|
| `none` | No build. Caddy shows the setup page. apply does not change `fe/current`. | none | none |
| `artifact` | Downloads a prebuilt bundle (`tar.gz`) and extracts it. | the bundle URL, or none with `file://` | seconds |
| `local` (default, S8) | Builds care_fe on the host in `care-fe-build.service`, a `node:22` container. | `codeload.github.com` and the npm registry | warm about 1 minute, cold about 3 minutes on a Pi 5 |

`local` is the default. Thus a new install serves Care after the first build.
`care-pi bootstrap` runs this build. It takes about 3 minutes and needs internet.
`care-pi bootstrap --fe-provider NAME` and `install.sh --fe-provider NAME` set the provider of a new `config.yaml`.

Select the provider:

| Host | Use |
|---|---|
| Pi 5, internet, default env | `local` or `artifact`. Both give the same build id. |
| Pi 5, internet, your own `env.frontend` or a plug with frontend env | `local`. The default bundle has only the default env. |
| Pi 4 (2 GB or 4 GB) | `artifact` (O11). Nobody measured the local build on a Pi 4. Expect 2 to 3 times the Pi 5 time. |
| No internet | `artifact` with a `file://` URL, for example a bundle on a USB drive. |

## Build id

The build id identifies a build. It is a hash of the care_fe sha and the frontend env:

1. Start with the line `care_fe=<full care_fe sha>`.
2. Add one line `NAME=value` for each variable of the frontend env, sorted by name.
3. Calculate the SHA-256 of the lines. Each line ends with a newline.
4. The build id is the first 16 hex digits.

The care_fe sha comes from `lock.yaml` (`git.care_fe.sha`).
The frontend env is the merge of: `REACT_CARE_API_URL=""`, `env.frontend` of the solution,
`env.frontend` of each enabled plug, and `env.frontend` of `config.yaml`. The last value wins.
For `REACT_MFE_REGISTERED_COMPONENTS`, the merge joins the values with commas.
The local provider writes the merged env to `/etc/care-pi/rendered/fe/frontend.env`.

To calculate a build id, use the agent:

```sh
care-pi fe build-id --care-fe-sha <full sha> --env-file frontend.env
```

The fe-bundle workflow uses the same command, so CI and the Pi calculate the same id.
A change of the care_fe sha or of the frontend env gives a new build id.
Then apply makes the new build and points `fe/current` at it. No container restarts.

care-pi does not set `REACT_PUBLIC_URL`. In care_fe, this variable sets only the
`og:url` meta tag of `index.html`, and the `.env` file of care_fe has a default
(`https://care.ohc.network`). Thus the build id does not depend on the host name,
and the default bundle of the solution fits each Pi with the default env.
You can set `REACT_PUBLIC_URL` in `env.frontend`. This gives a new build id.

## How apply uses the provider

1. apply calculates the build id.
2. When the provider is not `none` and `fe/current` is not the build id, the plan has the step `ensure-fe`.
   `apply --dry-run` prints `FE build <id> needed (provider <name>)` and builds nothing.
3. `ensure-fe` runs the provider. When the build directory has an `index.html`, the provider does nothing.
4. apply points `fe/current` at the build. The symlink swap is atomic. Caddy needs no reload.
5. apply records the build in `lock.yaml`, section `fe`.

The buckets and the stanza come before `ensure-fe`. When the provider fails, apply continues with the steps after it (the lock).
Then apply exits with code 1. The stack runs, and Caddy serves the previous build or the setup page.
The next apply tries the build again.

With `fe.provider: none`, apply does not change `fe/current` and does not remove a build.

`lock.yaml` records:

| Key | Provider | Content |
|---|---|---|
| `fe.provider`, `fe.build_id` | all | The provider and the build id. |
| `fe.care_fe_sha` | `artifact`, `local` | The care_fe sha of the build. |
| `fe.url`, `fe.sha256` | `artifact` | The bundle URL and the SHA-256 of the downloaded file. |
| `fe.built_at`, `fe.duration` | `local` | The start time (UTC) and the duration of the last build. |

## The local provider

apply renders three files for `fe.provider: local`:

| File | Content |
|---|---|
| `/etc/containers/systemd/care-fe-build.container` | The builder unit. |
| `/etc/care-pi/rendered/fe/fe-build.sh` | The build script. It runs in the container. |
| `/etc/care-pi/rendered/fe/frontend.env` | The frontend env, one `NAME=value` line for each variable. |

### The builder unit

| Setting | Value | Why |
|---|---|---|
| `Image=` | `images.node` (default `docker.io/library/node:22-bookworm-slim`), pinned to the digest in the lock | SP4 used `node:22`. |
| `Pod=`, `WantedBy=` | none | The build does not use the pod. Nothing starts the unit at boot. |
| `Type=oneshot`, `Restart=no` | | `systemctl start` waits until the build ends. |
| `MemoryHigh=768M` | | The kernel reclaims the memory of the builder first. |
| `MemoryMax=1700M`, `MemorySwapMax=1500M` | | The build needs about 1.8 to 2 GiB. A 1 GiB cap kills it every time (SP4). |
| `OOMScoreAdjust=900` | | When the host has no memory, the kernel stops the builder, not Postgres (`-500`). |
| `TimeoutStartSec=1800` | | 30 minutes. The agent also stops the unit after 40 minutes. |
| `Volume=` | `/var/lib/care-pi/build/care_fe:/build`, `/etc/care-pi/rendered/fe:/build/env:ro`, `/var/lib/care-pi/fe:/out` | Directories only, never single files. |
| `Environment=` | `CARE_FE_REPO`, `CARE_FE_SHA`, `BUILD_ID` | The inputs of the script. |
| `LogDriver=passthrough` | | The build log goes to the journal of the unit one time. |

The container uses the default network. It needs the internet.

### The build script

`fe-build.sh` is the SP4 recipe. It does these steps:

1. When `/build/src/.care-pi-sha` is not `CARE_FE_SHA`, it removes the old source (not `node_modules`),
   and extracts `https://codeload.github.com/<CARE_FE_REPO>/tar.gz/<CARE_FE_SHA>` into `/build/src`.
   The tarball is about 21 MB. The container needs no git.
2. It keeps a copy of the `package-lock.json` of the source in `/build/src/.care-pi-package-lock.json`.
   Before npm runs, it copies this file back to `package-lock.json`.
   `npm install` can change `package-lock.json`. Without the copy, the next build of the same source
   gets a different SHA-256 and runs `npm ci` again (wave 3 on lumine).
   When the SHA-256 of `package-lock.json` is the value in `/build/src/.care-pi-lock-hash`,
   it runs `npm install --no-audit --no-fund --prefer-offline` (seconds).
   Else it runs `npm ci --no-audit --no-fund --prefer-offline` and keeps the new hash.
   `node_modules` stays in `/build/src`. The npm cache is `/build/npm-cache`.
3. It exports each `NAME=value` line of `/build/env/frontend.env`. The value is literal.
4. It runs `npm run build:meta`, `npm run supported-browsers`,
   `npm run build:react -- --sourcemap false`, and `npm run postbuild`.
   It runs only the scripts that `package.json` has.
   Without `build:react`, it runs `npx vite build --sourcemap false`.
5. It copies `build/` to `/out/<BUILD_ID>.tmp`, writes `care-pi-build.json`, and renames the directory to `/out/<BUILD_ID>`.
6. It prints the time of each step.

`NODE_OPTIONS` is `--max-old-space-size=1024`. Sourcemaps are off: the build is 30 % faster
and the output is 13 MB instead of 45 MB (SP4).

`care-pi-build.json` records the build id, the care_fe repository and sha, the build time,
and the names of the env variables (not the values).

### Scale-down during a build (S8)

On a 2 GB Pi, the build fits only when the stack uses less memory.
First, the provider pulls the builder image when the local storage does not have it.
Thus the API keeps all workers during the pull. Then the provider does these steps, in this order:

1. Write `/etc/containers/systemd/care-api.container.d/50-care-pi-build.conf`:

   ```ini
   [Container]
   Environment=GUNICORN_WORKERS=1
   ```

2. `systemctl daemon-reload`
3. `systemctl restart care-api.service`
4. `systemctl stop care-worker.service`
5. Wait until `care-api` is active and healthy (at most 10 minutes).
6. `systemctl start care-fe-build.service`. This command waits until the build ends (at most 40 minutes).
   The agent shows the build log on its stdout (`journalctl -u care-fe-build.service -f`).
7. Restore. The provider always does this step, also after a failure, a timeout, Ctrl-C, SIGTERM,
   or SIGHUP (for example an SSH disconnect):
   remove the drop-in, `systemctl daemon-reload`, `systemctl restart care-api.service`,
   `systemctl start care-worker.service`.
   After a failure, the provider first runs `systemctl stop care-fe-build.service`.
   For a unit that failed, this command does nothing. After a timeout or a signal, it stops the build.
   The provider ignores SIGPIPE. Ctrl-C under `install.sh` also stops `tee`, the reader of the output.
   A write to the output then fails, but the restore continues. The provider prints the restore message
   after the restore.

Preset `solo` has 1 gunicorn worker, so the provider skips steps 1 to 3, 5, and the care-api part of step 7.
It stops and starts only care-worker.
When care-api or care-worker is not active (for example, the stack is stopped), the provider does not change it.

Why (SP4, Pi 5 2 GB):

- The vite 8 (rolldown) build has a working set of 1.8 to 2.0 GiB. Most of it is native memory,
  so the Node heap limit does not bound it.
- It fits only because of zram swap (1× RAM, zstd, about 4:1 on the cold pages of the build).
- 1 gunicorn worker and no celery worker free about 350 MiB.
- With these settings, host `MemAvailable` stayed at 370 MiB or more,
  and the API answered in 15 to 30 ms (mean) during the build.
  Background jobs wait until the build ends.
- The scale-down and the restore each restart care-api. Each restart gives HTTP 502 for about
  15 to 25 seconds (wave 3 on lumine). Preset `solo` has no care-api restart.
- A warm build took about 1 minute, a cold build (`npm ci`, 1456 packages) about 3 minutes.
  Wave 3 on lumine: in the container, warm 40 s and cold 115 to 146 s.
  With the scale-down and the restore, warm 1 min 27 s and cold 2 min 41 s to 3 min 13 s.

A build can stop before the restore, for example after a power loss. Then the drop-in stays.
The next `care-pi apply` removes it. apply holds the apply lock, and `fe build` holds the same lock,
so no build runs at that time (`docs/apply.md`, "The apply lock").
apply then runs `daemon-reload`. When `care.target` is active, apply restarts care-api and starts care-worker.
`care-pi fe status` shows a warning when the drop-in exists.

## The artifact provider

The provider downloads one `tar.gz` file with one GET request:

- The URL is `fe.artifact_url` (`https://` or `file://`).
  Without `fe.artifact_url`, the provider uses the default bundle of the solution for the care_fe sha
  and the build id:

  ```
  https://github.com/rithviknishad/care-pi/releases/download/fe-<solution>-<sha12>-<build id>/care_fe-<solution>-<sha12>-<build id>.tar.gz
  ```

  `<sha12>` is the first 12 digits of the care_fe sha in the lock. `<build id>` is the build id of the Pi.
  A change to the env of the solution gives a new build id, and thus a new release.
  The new bundle never replaces a bundle that a Pi with the old build id downloads.
  A Pi with its own `env.frontend` calculates a build id that has no default bundle. It gets HTTP 404.
- The time limit is 15 minutes. The size limit is 200 MB.
  The `User-Agent` header is `care-pi/<version>`.
- The provider downloads to `/var/lib/care-pi/fe/.tmp-<build id>-<random>/`.
- When `fe.artifact_sha256` is set, the SHA-256 of the file must be that value.
  For the default URL, the provider also reads the sidecar file `<URL>.sha256` when it exists.
  A different SHA-256 stops the provider. It installs nothing.
- The archive can contain only files and directories. A path with `..` or `/` at the start, a link,
  or a device stops the provider.
- When all entries are below one top-level directory, the provider removes that directory from the paths.
- The root of the bundle must have `index.html`.
- When the bundle has `care-pi-build.json` with a different build id:
  for the default URL, the provider stops (the env of the Pi is not the default env).
  For `fe.artifact_url`, the provider shows a warning and installs the bundle.
- Then the provider renames the directory to `/var/lib/care-pi/fe/<build id>`.

The repository `rithviknishad/care-pi` is private now. Thus the default URL gives HTTP 404
until the repository is public. A `file://` URL and each other `https://` URL work now.

The default bundle exists only for the care_fe shas that the fe-bundle workflow built.
The Clinic solution tracks `develop`, so the sha in your lock can be newer.
Then run the workflow with `care_fe_ref` set to the sha in `lock.yaml` (`care-pi fe status` shows it),
or set `fe.artifact_url`, or use the local provider.

## Disk layout

| Path | Content | Size |
|---|---|---|
| `/var/lib/care-pi/fe/<build id>/` | One build. | about 13 MB |
| `/var/lib/care-pi/fe/current` | Relative symlink to the current build. | |
| `/var/lib/care-pi/fe/.tmp-*`, `/var/lib/care-pi/fe/*.tmp` | Incomplete builds. `fe prune` removes them. | |
| `/var/lib/care-pi/build/care_fe/src/` | The care_fe source and `node_modules` (local provider). | about 1.2 GB |
| `/var/lib/care-pi/build/care_fe/npm-cache/` | The npm cache (local provider). | about 0.4 GB |
| `/var/lib/care-pi/build/care_fe/env/` | An empty mount point. podman creates it. | |
| `/var/lib/care-pi/state/fe-build.lock` | One build, download, or prune at a time. | |
| `/run/care-pi/apply.lock` | The apply lock: one apply, bootstrap, `fe build`, or `fe prune` at a time. | |

Plan for about 2 GB of disk for the local provider.
To free the build cache, remove `/var/lib/care-pi/build/care_fe`. The next build is a cold build.

## Commands

| Command | What it does | Exit codes |
|---|---|---|
| `care-pi fe build [--force] [--lock-timeout DUR]` | Holds the apply lock. Makes the build of `lock.yaml` with the provider of `config.yaml`, then points `fe/current` at it. `--force` removes the build directory first. When that build is current, the site shows the setup page until the new build is ready. After a change to `config.yaml`, run `care-pi apply` instead. | 0 ok, 1 failed, 2 invalid config or `fe.provider: none` |
| `care-pi fe status` | Shows the provider, the build id, the care_fe sha, `fe/current`, the last build, and each build with its size and date (`*` = current). | 0 `fe/current` is the build of the lock (or provider `none`), 1 it is not |
| `care-pi fe prune [--keep N] [--lock-timeout DUR]` | Holds the apply lock. Removes all builds except the `N` newest builds (default 2) and the current build. Removes incomplete builds. | 0 ok, 1 a build runs now or a removal failed, 2 `N` < 1 |
| `care-pi logs fe-build [-f]` | The journal of `care-fe-build.service`. | passthrough |

Hidden commands for CI:

| Command | Output |
|---|---|
| `care-pi fe script` | `fe-build.sh`, as apply renders it. |
| `care-pi fe env --solution NAME [--solutions DIR]` | `frontend.env` for the default config of the solution. |
| `care-pi fe build-id --care-fe-sha SHA --env-file FILE` | The build id. |

## The fe-bundle workflow

`.github/workflows/fe-bundle.yml` builds the default bundle of a solution.

Triggers: `workflow_dispatch` (inputs `solution`, default `care-clinic`, and `care_fe_ref`,
default `care_fe.ref` of the solution), and a push to `main` that changes `solutions/**` or the workflow.

Steps:

1. Read `solutions/<solution>/solution.yaml` with `yq`.
2. Resolve `care_fe_ref` to a sha: `gh api repos/<owner>/<name>/commits/<ref> --jq .sha`.
3. Build `care-pi`. Write `fe-build.sh` (`care-pi fe script`) and `frontend.env` (`care-pi fe env`).
   Calculate the build id (`care-pi fe build-id`).
4. Check out care_fe at the sha into `care_fe/`. Set up Node 22 with the npm cache.
5. Run `fe-build.sh` with `SRC=care_fe`. The script sees `.care-pi-sha` and does not fetch the tarball.
6. Check the bundle: `index.html`, no `careapi.ohc.network` (same origin), and the build id in `care-pi-build.json`.
7. Write `care_fe-<solution>-<sha12>-<build id>.tar.gz` and `care_fe-<solution>-<sha12>-<build id>.tar.gz.sha256`.
   Upload them as a workflow artifact.
8. Create or update the release `fe-<solution>-<sha12>-<build id>`. The notes and the job summary give the solution,
   the care_fe sha, the build id, and the env keys (not the values).
   The build id is in the tag and in the file name. Thus `--clobber` replaces only a bundle with the same build id.

One recipe has two entry points: `care-fe-build.service` on the Pi, and the workflow on a runner.

## Troubleshooting

| Problem | Cause and action |
|---|---|
| The build stops with exit code 137, or the journal shows `oom-kill` | The host has not enough memory. Make sure that zram swap is 1× RAM or more: `care-pi host check`, `cat /proc/swaps`. Make sure that the memory cgroup is on (`cgroup_enable=memory cgroup_memory=1`). Stop other programs. On a Pi 4, use the artifact provider. |
| The first build takes about 3 minutes | This is a cold build: `npm ci` installs 1456 packages. The next build with the same `package-lock.json` takes about 1 minute. |
| `fetch ... HTTP 404`, `npm ERR! network`, or `EAI_AGAIN` | The container has no internet. Connect the host, or use the artifact provider with a `file://` URL. |
| `care-fe-build.container is not for FE build <id>` | `config.yaml` changed after the last apply. Run `care-pi apply`. |
| The default bundle gives HTTP 404 | The repository is private, or the workflow did not build this care_fe sha and build id. Your own `env.frontend` gives a build id without a default bundle. See "The artifact provider". |
| `the bundle is for FE build X, but this config needs FE build Y` | The env of the Pi is not the default env of the solution. Use the local provider, or set `fe.artifact_url`. |
| care-api has 1 worker after a build | A build stopped before the restore (for example, a power loss). Run `care-pi apply`. apply removes `/etc/containers/systemd/care-api.container.d/50-care-pi-build.conf` and restores care-api and care-worker. |
| `a different care-pi process builds or downloads an FE build now` | Wait until the other build ends, then do the command again. |
| `another care-pi command holds the lock; waiting` | An apply, bootstrap, `fe build`, or `fe prune` runs now. The command waits, at most `--lock-timeout` (default 30 minutes). |
