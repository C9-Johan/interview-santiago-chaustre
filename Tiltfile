# Tiltfile — developer dashboard for InquiryIQ.
#
# Tilt's built-in docker_compose() shells out to the "docker-compose" /
# "docker compose" CLI, which podman users typically do not have. So we
# drive the compose stack through plain `local_resource` calls that shell
# out to whichever compose binary is on PATH — `podman compose` by default,
# override with `COMPOSE=docker\ compose tilt up`.
#
# What Tilt buys you here: a single UI at http://localhost:10350 with live
# logs, quick-restart buttons, and one-click triggers for unit tests,
# integration tests, lint, eval, and the full end-to-end smoke script.
#
# Bring it up:   tilt up          (or: make tilt-up)
# Shut it down:  tilt down        (or: make tilt-down)

# Allow overriding the compose binary via env so docker users aren't locked
# out. Default is podman compose.
compose_bin = os.getenv('COMPOSE', 'podman compose')
dev_file    = './compose/dev.yml'

def compose_cmd(verb):
    return '{bin} -f {file} {verb}'.format(bin=compose_bin, file=dev_file, verb=verb)

# --- Stack lifecycle -----------------------------------------------------
# compose-up runs on `tilt up`; compose-down fires on tilt's stop via
# the cmd_button below. Keeping it as a local_resource means tilt sees
# stdout/stderr in its per-service log pane.
local_resource(
    'compose-up',
    cmd=compose_cmd('up -d --build'),
    labels=['stack'],
    auto_init=True,
    trigger_mode=TRIGGER_MODE_MANUAL,
    allow_parallel=False,
)

local_resource(
    'compose-down',
    cmd=compose_cmd('down'),
    labels=['stack'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

# --- Per-service log tails ----------------------------------------------
# Each of these runs a `compose logs -f <service>` so Tilt renders the
# container's stdout in its UI. Pressing ▶ restarts the tail if the
# container restarts under you.
def compose_logs(service, label):
    local_resource(
        service,
        serve_cmd=compose_cmd('logs -f ' + service),
        labels=[label],
        resource_deps=['compose-up'],
        readiness_probe=probe(
            period_secs=2,
            exec=exec_action(['true']),  # serve_cmd starts immediately
        ),
    )

compose_logs('mockoon',   'mocks')
compose_logs('inquiryiq', 'service')
compose_logs('tester',    'ui')

# --- Health & smoke tests -----------------------------------------------
local_resource(
    'wait-for-health',
    cmd='./scripts/wait-for-health.sh',
    labels=['tests'],
    resource_deps=['compose-up'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
    'e2e-smoke',
    cmd='./scripts/wait-for-health.sh && ./scripts/e2e-smoke.sh',
    labels=['tests'],
    resource_deps=['compose-up'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
    'unit-tests',
    cmd='go test -race -count=1 ./...',
    deps=['internal/', 'cmd/', 'go.mod', 'go.sum'],
    labels=['tests'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
    'integration-tests',
    cmd='go test -tags=integration -count=1 ./tests/integration/...',
    deps=['tests/integration/', 'internal/', 'cmd/'],
    labels=['tests'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
    'lint',
    cmd='golangci-lint run ./...',
    deps=['internal/', 'cmd/', '.golangci.yml'],
    labels=['tests'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
    'eval-classifier',
    cmd='make eval',
    labels=['tests'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)
