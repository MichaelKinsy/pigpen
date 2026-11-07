#!/bin/sh
# pig-kagent.sh: check, render, install, upgrade and uninstall the pig-kagent chart.
#
# Needs helm; kubectl for everything except "render". "check" and "render" never change
# the cluster. The script never reads or prints Secret values: it only lists the key names
# of the Secrets the release refers to.
#
# Licensed under the Apache License, Version 2.0 (same as the rest of the harness tree).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
NL='
'
NS=kagent
REL=pig
CHART="$here/chart/pig-kagent"
SUBNS=""
CTX=""
TIMEOUT=300
SKIP_PREFLIGHT=0
DRY=0
VALARGS=""
fails=0
warns=0

ok()   { printf 'ok    %s\n' "$*"; }
note() { printf 'note  %s\n' "$*"; }
warn() { warns=$((warns + 1)); printf 'WARN  %s\n' "$*"; }
fail() { fails=$((fails + 1)); printf 'FAIL  %s\n' "$*"; }
die()  { printf 'pig-kagent.sh: %s\n' "$*" >&2; exit 2; }

usage() {
  cat <<'U'
usage: pig-kagent.sh <check|render|install|uninstall> [options]

  check       read-only preflight: kagent, Substrate, WorkerPool, labels, Secrets, grants
  render      print the manifests (helm template); needs no cluster
  install     preflight, helm upgrade --install, then wait for the Agent Ready condition
  uninstall   helm uninstall (removes only what the release created)

options:
  -n, --namespace NS         namespace of the Harness, Agent and ModelConfig (default kagent)
  -r, --release NAME         Helm release name (default pig)
  -f, --values FILE          values file (repeatable)
      --set KEY=VALUE        Helm value (repeatable), for example
                             --set image=ghcr.io/OWNER/pig-kagent@sha256:<64 hex>
      --chart REF            chart directory or oci://REGISTRY/PATH/pig-kagent[@sha256:DIGEST]
                             (default: chart/pig-kagent next to this script)
      --context CTX          kubectl and helm context (default: the current one)
      --substrate-namespace NS   skip the discovery of the Substrate namespace
      --dry-run              install: helm --dry-run=server, nothing is changed
      --skip-preflight       install without the checks (not recommended)
      --timeout SECONDS      wait for Agent Ready (default 300)
  -h, --help                 this text
U
}

[ $# -gt 0 ] || { usage; exit 2; }
cmd=$1
shift
case $cmd in -h|--help) usage; exit 0;; esac
while [ $# -gt 0 ]; do
  case $1 in
    -n|--namespace|-r|--release|-f|--values|--set|--chart|--context|--substrate-namespace|--timeout)
      [ $# -ge 2 ] || die "$1 needs a value" ;;
  esac
  case $1 in
    -n|--namespace) NS=$2; shift 2;;
    -r|--release) REL=$2; shift 2;;
    -f|--values) VALARGS="$VALARGS$NL-f$NL$2"; shift 2;;
    --set) VALARGS="$VALARGS$NL--set$NL$2"; shift 2;;
    --chart) CHART=$2; shift 2;;
    --context) CTX=$2; shift 2;;
    --substrate-namespace) SUBNS=$2; shift 2;;
    --timeout) TIMEOUT=$2; shift 2;;
    --dry-run) DRY=1; shift;;
    --skip-preflight) SKIP_PREFLIGHT=1; shift;;
    -h|--help) usage; exit 0;;
    *) die "unknown option $1";;
  esac
done
case $TIMEOUT in ''|*[!0-9]*) die "--timeout takes whole seconds";; esac

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need helm
[ "$cmd" = render ] || need kubectl

k() { if [ -n "$CTX" ]; then kubectl --context "$CTX" "$@"; else kubectl "$@"; fi; }
h() { if [ -n "$CTX" ]; then helm --kube-context "$CTX" "$@"; else helm "$@"; fi; }

# The chart is rendered once per question; every check reads what the chart will create, so the
# checks and the install cannot drift apart. Values are newline separated, so paths may hold spaces.
rendered() {
  (
    IFS=$NL
    set -f
    # shellcheck disable=SC2086
    h template "$REL" "$CHART" -n "$NS" $VALARGS "$@"
  )
}
# field TEMPLATE JSONPATH: a value from one rendered template (needs the kagent CRDs for kubectl to map the kind).
field() {
  rendered --show-only "templates/$1" 2>/dev/null |
    k create --dry-run=client --validate=false -f - -o jsonpath="$2" 2>/dev/null || true
}

check_crd() { # group plural version remedy
  v=$(k get crd "$2.$1" -o jsonpath='{range .spec.versions[?(@.served==true)]}{.name}{" "}{end}' 2>/dev/null || true)
  if [ -z "$v" ]; then
    fail "CRD $2.$1 not found. $4"
  else
    case " $v " in
      *" $3 "*) ok "CRD $2.$1 serves $3";;
      *) fail "CRD $2.$1 serves [$v], the chart targets $3. Use a chart version that matches the installed release.";;
    esac
  fi
}

secret_has_key() { # namespace name key; prints nothing, reads key names only
  # shellcheck disable=SC2016 # the dollar signs are go-template variables, not shell ones
  k get secret -n "$1" "$2" -o go-template='{{range $key, $_ := .data}}{{$key}}{{"\n"}}{{end}}' 2>/dev/null | grep -qx "$3"
}

# Secrets a rendered release refers to, as "name/key" words.
release_secrets() {
  field modelconfig.yaml '{.spec.apiKeySecret}/{.spec.apiKeySecretKey}{"\n"}'
  field remotemcpserver.yaml '{range .spec.headersFrom[?(@.valueFrom.type=="Secret")]}{.valueFrom.name}/{.valueFrom.key}{"\n"}{end}'
  field egresspolicy.yaml '{range .spec.destinations[*]}{.authentication.secretKeyRef.name}/{.authentication.secretKeyRef.key}{"\n"}{end}'
}

# credential_grant NS: succeeds when the Substrate credential provider lets atespace NS read Secrets in NS.
credential_grant() {
  cm=$(k get cm -n "$SUBNS" -o name 2>/dev/null | grep 'k8s-credential-provider-namespace-policy' | head -n 1 || true)
  if [ -z "$cm" ]; then
    warn "credential provider namespace policy ConfigMap not found in $SUBNS; cannot confirm that Secrets in $NS are readable"
    return 0
  fi
  pol=$(k get -n "$SUBNS" "$cm" -o jsonpath='{.data.namespace-policy\.yaml}' 2>/dev/null | tr -d ' "\n' | tr '}' '\n')
  case $pol in
    *atespace:*) ;;
    *) warn "cannot read the namespacePolicies in $cm; confirm that atespace $NS may read Secrets in $NS"; return 0;;
  esac
  if printf '%s\n' "$pol" | grep "atespace:$NS\(,\|\$\)" | grep -q "allowedNamespaces:\[\([^]]*,\)\{0,1\}$NS\(,[^]]*\)\{0,1\}\]"; then
    ok "credential provider lets atespace $NS read Secrets in $NS"
  else
    fail "credential provider namespacePolicies has no atespace $NS allowing namespace $NS (Substrate chart value credentialProvider.namespacePolicies: [{atespace: $NS, allowedNamespaces: [$NS]}])"
  fi
}

# owned KIND NAME: warn-free guard against overwriting an object that another release or hand-made manifest owns.
check_owned() {
  rel=$(k get "$1" "$2" -n "$NS" -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)
  if k get "$1" "$2" -n "$NS" >/dev/null 2>&1; then
    if [ "$rel" = "$REL" ]; then
      ok "$1 $NS/$2 belongs to release $REL"
    else
      fail "$1 $NS/$2 exists and is not owned by Helm release $REL (owner: ${rel:-none}); pick another --release or fullnameOverride"
    fi
  fi
}

# check_shared_harness: harness.create is false, so the Agent runs on a Harness somebody else manages.
check_shared_harness() {
  hn=$(field agent.yaml '{.spec.harnessRef.name}')
  [ -n "$hn" ] || return 0
  if ! k get harnesses.api.kagent.dev "$hn" -n "$NS" >/dev/null 2>&1; then
    fail "Harness $NS/$hn not found (harness.create is false; create it first, for example with another release)"
    return 0
  fi
  if [ -n "$(k get harnesses.api.kagent.dev "$hn" -n "$NS" -o jsonpath='{.spec.byo}' 2>/dev/null)" ]; then
    ok "Harness $NS/$hn exists and is a byo Harness"
  else
    fail "Harness $NS/$hn is not a byo Harness; the PiG harness image only runs as one"
    return 0
  fi
  have=$(k get harnesses.api.kagent.dev "$hn" -n "$NS" -o jsonpath='{.spec.workload.image}' 2>/dev/null || true)
  want=$(field agenttemplate.yaml '{.metadata.annotations.pig\.dev/harness-image}')
  if [ "$have" = "$want" ]; then
    ok "Harness $NS/$hn runs image $have"
  else
    warn "Harness $NS/$hn runs image ${have:-none}, not the image value of this release ($want); the Agent runs what the Harness names"
  fi
}

preflight() {
  k version >/dev/null 2>&1 || die "kubectl cannot reach a cluster (context: $(k config current-context 2>/dev/null || echo none))"
  ok "cluster reachable (context $(k config current-context 2>/dev/null || echo "${CTX:-unknown}"))"
  if ! err=$(rendered 2>&1 >/dev/null); then
    fail "chart or values invalid: $err"
    return 0
  fi
  ok "chart renders and the values pass the schema (digest-pinned image, command set)"

  for plural in harnesses agenttemplates agents egresspolicies modelconfigs remotemcpservers; do
    check_crd api.kagent.dev "$plural" v1alpha3 "Install kagent first (kagent install, or helm install kagent-crds oci://ghcr.io/kagent-dev/kagent/helm/kagent-crds)."
  done
  [ "$fails" -eq 0 ] || return 0
  if k explain harness.spec.byo --api-version=api.kagent.dev/v1alpha3 >/dev/null 2>&1; then
    ok "Harness CRD has spec.byo"
  else
    fail "Harness CRD has no spec.byo: this kagent is too old for BYO harnesses"
  fi
  if k get crd agents.kagent.dev >/dev/null 2>&1; then
    warn "legacy agents.kagent.dev is also installed: use 'kubectl get agents.api.kagent.dev' for this Agent"
  fi

  ctl=$(k get deploy -A -l app.kubernetes.io/name=kagent,app.kubernetes.io/component=controller \
    -o jsonpath='{range .items[*]}{.metadata.namespace}{"/"}{.metadata.name}{"@"}{.metadata.labels.app\.kubernetes\.io/version}{"\n"}{end}' 2>/dev/null | head -n 1 || true)
  if [ -z "$ctl" ]; then
    fail "no kagent controller Deployment found (labels app.kubernetes.io/name=kagent, app.kubernetes.io/component=controller)"
  else
    ctlns=${ctl%%/*}
    ver=${ctl##*@}
    tested=$(h show chart "$CHART" 2>/dev/null | sed -n 's/^ *pig\.dev\/tested-kagent: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p')
    ok "kagent controller $ctl"
    if [ "${ver#v}" != "${tested#v}" ]; then
      warn "kagent $ver is installed; this chart was tested with ${tested:-an unknown version}. Compatibility is not verified."
    fi
    if [ "$(k get ns "$ctlns" -o jsonpath='{.metadata.labels.ate\.dev/api-access}' 2>/dev/null)" = true ]; then
      ok "namespace $ctlns is labeled ate.dev/api-access=true"
    else
      fail "namespace $ctlns lacks ate.dev/api-access=true (kubectl label namespace $ctlns ate.dev/api-access=true --overwrite)"
    fi
  fi

  check_crd ate.dev workerpools v1 "Install Substrate first (kagent install does it)."
  [ -n "$SUBNS" ] || SUBNS=$(k get pods -A -l app=ate-api-server -o jsonpath='{.items[0].metadata.namespace}' 2>/dev/null || true)
  if [ -z "$SUBNS" ]; then
    fail "Substrate not found (no pod labeled app=ate-api-server); pass --substrate-namespace"
  else
    ok "Substrate namespace $SUBNS"
    args=$(k get pods -n "$SUBNS" -l app=ate-api-server -o jsonpath='{.items[0].spec.containers[*].args}' 2>/dev/null || true)
    for g in PerTemplateSnapshotLocation GoldenEgressPolicy; do
      case $args in
        *"$g"*) ok "Substrate preview gate $g is enabled";;
        *) fail "Substrate preview gate $g is not enabled (ate-api-server --preview)";;
      esac
    done
  fi

  wp=$(field harness.yaml '{.spec.substrate.workerPoolRef.name}')
  if [ -n "$wp" ]; then
    if k get workerpool.ate.dev "$wp" -n "$NS" >/dev/null 2>&1; then
      ok "WorkerPool $NS/$wp exists"
    else
      pools=$(k get workerpool.ate.dev -n "$NS" -o name 2>/dev/null | tr '\n' ' ' || true)
      fail "WorkerPool $NS/$wp not found; pools in $NS: ${pools:-none} (set substrate.workerPoolRef, or install kagent with substrateWorkerPool.create=true)"
    fi
  fi
  loc=$(field harness.yaml '{.spec.substrate.snapshotPolicy.location}')
  if [ -n "$loc" ]; then
    used=$(k get harnesses.api.kagent.dev -A -o jsonpath='{range .items[*]}{.spec.substrate.snapshotPolicy.location}{"\n"}{end}' 2>/dev/null | sort -u | tr '\n' ' ' || true)
    if [ -n "$used" ]; then
      case " $used " in
        *" $loc "*) ok "snapshot location $loc is used by an existing Harness";;
        *) warn "snapshot location $loc differs from the ones other Harnesses use: ${used}. It must be a location Substrate's object store serves.";;
      esac
    else
      note "snapshot location $loc; it must be a location Substrate's object store serves"
    fi
  fi

  mc=$(field agenttemplate.yaml '{.spec.modelConfig.name}')
  if [ -n "$mc" ] && ! rendered 2>/dev/null | grep -q '^kind: ModelConfig'; then
    if k get modelconfig.api.kagent.dev "$mc" -n "$NS" >/dev/null 2>&1; then
      ok "ModelConfig $NS/$mc exists"
    else
      fail "ModelConfig $NS/$mc not found (create it, or set modelConfig.create=true)"
    fi
    url=$(k get modelconfig.api.kagent.dev "$mc" -n "$NS" -o jsonpath='{.spec.openAI.baseUrl}{.spec.anthropic.baseUrl}{.spec.ollama.host}' 2>/dev/null || true)
    sec=$(k get modelconfig.api.kagent.dev "$mc" -n "$NS" -o jsonpath='{.spec.apiKeySecret}/{.spec.apiKeySecretKey}' 2>/dev/null || true)
  else
    url=$(field modelconfig.yaml '{.spec.openAI.baseUrl}{.spec.anthropic.baseUrl}{.spec.ollama.host}')
    sec=""
  fi
  if [ -n "${url:-}" ]; then
    case $url in
      *://localhost*|*://127.*|*://[0-9]*.[0-9]*.[0-9]*.[0-9]*|*://\[*) fail "model endpoint $url is an IP address or localhost; Substrate egress allows DNS names only";;
      *) ok "model endpoint $url is a DNS name";;
    esac
  fi

  secrets=$( { printf '%s\n' "${sec:-}"; release_secrets; } | grep -v '^/*$' | sort -u | tr '\n' ' ' || true)
  if [ -n "$secrets" ]; then
    for s in $secrets; do
      if secret_has_key "$NS" "${s%%/*}" "${s#*/}"; then
        ok "Secret $NS/${s%%/*} has key ${s#*/}"
      else
        fail "Secret $NS/${s%%/*} is missing or has no key ${s#*/} (create it; this chart never does)"
      fi
    done
    [ -z "$SUBNS" ] || credential_grant
  fi

  hname=$(field harness.yaml '{.metadata.name}')
  if [ -n "$hname" ]; then
    check_owned harness.api.kagent.dev "$hname"
  else
    check_shared_harness
  fi
  check_owned agenttemplate.api.kagent.dev "$(field agenttemplate.yaml '{.metadata.name}')"
  agname=$(field agent.yaml '{.metadata.name}')
  [ -z "$agname" ] || check_owned agent.api.kagent.dev "$agname"

  n=$(field egresspolicy.yaml '{range .spec.destinations[*]}{.origin}{"\n"}{end}' | grep -c . || true)
  note "egress: model, MCP and telemetry origins are inferred by kagent; this release adds ${n:-0} origin(s). Tools such as git and package managers need origins of their own."
  note "egress: origins are DNS names (a leading *. covers one label); IP addresses are rejected."
}

report() {
  printf '\n%s failed, %s warnings\n' "$fails" "$warns"
  [ "$fails" -eq 0 ]
}

wait_ready() { # name
  name=$1
  # The image the Agent has to run: this release's, or what an existing Harness names when it owns the Harness.
  want=$(field agenttemplate.yaml '{.metadata.annotations.pig\.dev/harness-image}')
  if [ -z "$(field harness.yaml '{.metadata.name}')" ]; then
    want=$(k get harnesses.api.kagent.dev "$(field agent.yaml '{.spec.harnessRef.name}')" -n "$NS" -o jsonpath='{.spec.workload.image}' 2>/dev/null || true)
  fi
  deadline=$(( $(date +%s) + TIMEOUT ))
  while :; do
    st=$(k get agents.api.kagent.dev "$name" -n "$NS" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}|{.status.desiredRevision}|{.status.latestSuccessfulRevision}|{.status.workloadImage}' 2>/dev/null || true)
    ready=${st%%|*}
    rest=${st#*|}
    desired=${rest%%|*}
    rest=${rest#*|}
    latest=${rest%%|*}
    img=${rest#*|}
    # After an upgrade the old revision keeps Ready=True until the controller has seen the new one.
    if [ "$ready" = True ] && [ "$desired" = "$latest" ] && { [ -z "$img" ] || [ "$img" = "$want" ]; }; then
      ok "Agent $NS/$name is Ready"
      return 0
    fi
    [ "$(date +%s)" -lt "$deadline" ] || break
    sleep 3
  done
  printf 'Conditions of Agent %s/%s:\n' "$NS" "$name"
  k get agents.api.kagent.dev "$name" -n "$NS" -o jsonpath='{range .status.conditions[*]}  {.type}={.status} {.reason}: {.message}{"\n"}{end}' 2>&1 || true
  k get agents.api.kagent.dev "$name" -n "$NS" -o jsonpath='{range .status.warnings[*]}  warning: {@}{"\n"}{end}' 2>/dev/null || true
  die "Agent $name is not Ready after ${TIMEOUT}s (the Harness has no status; conditions live on the Agent)"
}

case $cmd in
  check)
    preflight
    report;;
  render)
    rendered;;
  install)
    if [ "$SKIP_PREFLIGHT" -eq 0 ]; then
      preflight
      report || die "preflight failed; fix the FAIL lines, or use --skip-preflight"
    fi
    mode=""
    [ "$DRY" -eq 0 ] || mode="--dry-run=server"
    (
      IFS=$NL
      set -f
      # shellcheck disable=SC2086
      h upgrade --install "$REL" "$CHART" -n "$NS" $VALARGS $mode
    )
    [ "$DRY" -eq 0 ] || exit 0
    ag=$(field agent.yaml '{.metadata.name}')
    [ -z "$ag" ] || wait_ready "$ag";;
  uninstall)
    if h status "$REL" -n "$NS" >/dev/null 2>&1; then
      h uninstall "$REL" -n "$NS"
    else
      note "release $REL is not installed in $NS; nothing to do"
    fi;;
  *)
    usage
    exit 2;;
esac
