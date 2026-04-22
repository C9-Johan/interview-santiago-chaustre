# Tiltfile — developer dashboard for InquiryIQ.
#
# One command dev loop:  `make run`  (or `tilt up` directly)
# Dashboard:              http://localhost:10350
# Tester UI (dev stack):  http://localhost:4000
#
# What Tilt gives you on top of `make up`:
#   • Live per-service logs rendered side-by-side in the browser
#   • Preflight env-check surfaces missing LLM_API_KEY before compose runs
#   • Auto-waits for /healthz and surfaces the smoke/unit/lint/eval buttons
#   • Code edits under internal/, cmd/, tests/ re-trigger unit/integration
#     tests automatically (manual buttons still available)
#
# Tilt's built-in docker_compose() shells out to `docker compose`, which
# podman-rootless users don't have. We drive compose through local_resource
# so it works on every machine. Override via `COMPOSE=docker\ compose tilt up`.
#
# Modes:
#   MODE=dev  (default) — compose/dev.yml: Mockoon + service + tester UI
#   MODE=prod           — stack + dev + prod.override merged (Mongo, Valkey,
#                         Alloy, Tempo, Prom, Grafana + real store backends)
#
# Compose binary + mode:
compose_bin = os.getenv('COMPOSE', 'podman compose')
mode        = os.getenv('MODE', 'dev')

if mode == 'prod':
    compose_files = '-f compose/stack.yml -f compose/dev.yml -f compose/prod.override.yml'
    mode_label    = 'prod'
else:
    compose_files = '-f compose/dev.yml'
    mode_label    = 'dev'

def compose_cmd(verb):
    return '{bin} {files} {verb}'.format(bin=compose_bin, files=compose_files, verb=verb)

# Wrap a shell command so it sources .env.local first — matches the
# precedence of mise and `make up`. `set -a` auto-exports every var so
# child processes (podman compose, scripts) see them. Missing file is
# silently ignored; the preflight catches genuine missing-secret cases.
def with_env(cmd):
    return 'set -a; [ -f .env.local ] && . ./.env.local; set +a; ' + cmd

# --- Preflight: env check ------------------------------------------------
# Blocks every downstream resource until LLM_API_KEY is set. Without this,
# `podman compose up` dies with a "LLM_API_KEY must be set" error from the
# ${VAR:?} substitution, which is confusing if you're new to the repo.
local_resource(
    'env-check',
    cmd=with_env("""
        if [ -z "${LLM_API_KEY:-}" ]; then
            echo "✗ LLM_API_KEY unset — cp .env.local.example .env.local, fill in, then re-run"
            exit 1
        fi
        echo "✓ env ok — mode=%s"
    """ % mode_label),
    labels=['stack'],
    auto_init=True,
    allow_parallel=False,
)

# --- Stack lifecycle -----------------------------------------------------
# compose-up runs on `tilt up`. Auto-runs after env-check passes.
# compose-down is manual (click to tear down without exiting Tilt).
# Stack links — shown as clickable buttons next to every resource. In dev
# mode only the dev-stack URLs are reachable; prod mode also boots the full
# observability chain so the Grafana/Tempo/Prom/Mongo/Redis links resolve.
dev_links = [
    link('http://localhost:4000',  'Tester UI'),
    link('http://localhost:8080/healthz', 'Service /healthz'),
    link('http://localhost:3001',  'Mockoon (fake Guesty)'),
]
prod_links = dev_links + [
    link('http://localhost:3000',  'Grafana'),
    link('http://localhost:9090',  'Prometheus'),
    link('http://localhost:3200',  'Tempo'),
    link('http://localhost:12345', 'Alloy UI'),
    link('http://localhost:8081',  'Mongo Express'),
    link('http://localhost:5540',  'RedisInsight'),
]
stack_links = prod_links if mode == 'prod' else dev_links

local_resource(
    'compose-up',
    cmd=with_env(compose_cmd('up -d --build')),
    links=stack_links,
    labels=['stack'],
    resource_deps=['env-check'],
    auto_init=True,
    allow_parallel=False,
)

local_resource(
    'compose-down',
    cmd=with_env(compose_cmd('down')),
    labels=['stack'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)

# Auto-wait for /healthz so the tester UI / smoke button are actually
# clickable when this turns green.
local_resource(
    'wait-for-health',
    cmd='./scripts/wait-for-health.sh',
    labels=['stack'],
    resource_deps=['compose-up'],
    auto_init=True,
)

# --- Per-service log tails ----------------------------------------------
# Each `compose logs -f <service>` surfaces the container's stdout in
# Tilt's UI pane. ▶ restarts the tail if the container restarts under you.
def compose_logs(service, label, svc_links=[]):
    local_resource(
        service,
        serve_cmd=with_env(compose_cmd('logs -f ' + service)),
        links=svc_links,
        labels=[label],
        resource_deps=['compose-up'],
        readiness_probe=probe(
            period_secs=2,
            exec=exec_action(['true']),
        ),
    )

compose_logs('mockoon',   'mocks',   [link('http://localhost:3001', 'Mockoon API')])
compose_logs('inquiryiq', 'service', [link('http://localhost:8080/healthz', '/healthz')])
compose_logs('tester',    'ui',      [link('http://localhost:4000', 'Tester UI')])

# Observability log tails — only rendered in prod mode where these
# containers actually exist. Clicking the resource name opens their UI.
if mode == 'prod':
    compose_logs('grafana',       'observability', [link('http://localhost:3000',  'Grafana')])
    compose_logs('prometheus',    'observability', [link('http://localhost:9090',  'Prometheus')])
    compose_logs('tempo',         'observability', [link('http://localhost:3200',  'Tempo')])
    compose_logs('alloy',         'observability', [link('http://localhost:12345', 'Alloy UI')])
    compose_logs('mongo-express', 'stores',        [link('http://localhost:8081',  'Mongo Express')])
    compose_logs('redisinsight',  'stores',        [link('http://localhost:5540',  'RedisInsight')])

# --- Tests, lint, eval --------------------------------------------------
# Manual buttons so you control when they run — CI runs them on every
# change, but interactively you want to decide.
local_resource(
    'e2e-smoke',
    cmd=with_env('./scripts/wait-for-health.sh && ./scripts/e2e-smoke.sh'),
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
    cmd=with_env('make eval'),
    labels=['tests'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
)
