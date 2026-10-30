#!/usr/bin/env bash
# Creates a tmux session named "play" whose windows print fake, moving output.
#   play-session.sh          create the session (replaces an existing "play")
#   play-session.sh kill     remove it
#   play-session.sh <mode>   run one generator (used internally by the windows)
set -u
SELF=$(readlink -f "$0")
SESSION=${PLAY_SESSION:-play}

R=$'\e[31m' G=$'\e[32m' Y=$'\e[33m' B=$'\e[34m' M=$'\e[35m' C=$'\e[36m' D=$'\e[2m' N=$'\e[0m'
pick() { local a=("$@"); printf '%s' "${a[RANDOM % ${#a[@]}]}"; }
ts() { date '+%Y-%m-%d %H:%M:%S.%3N'; }
nap() { sleep "0.$((RANDOM % 9 + 1))"; }

api_log() {
  local paths=(/api/users /api/orders /api/orders/42 /health /api/login /api/cart /static/app.js /api/search?q=tmux)
  local methods=(GET GET GET POST PUT DELETE)
  while true; do
    local code=$(pick 200 200 200 200 201 204 304 400 401 404 500)
    local col=$G; ((code >= 400)) && col=$Y; ((code >= 500)) && col=$R
    printf '%s %s%-6s%s %-22s %s%d%s %4dms %s\n' "$(ts)" "$C" "$(pick "${methods[@]}")" "$N" \
      "$(pick "${paths[@]}")" "$col" "$code" "$N" $((RANDOM % 900 + 3)) "${D}10.0.$((RANDOM%255)).$((RANDOM%255))${N}"
    nap
  done
}

worker_log() {
  local jobs=(send_email resize_image sync_inventory charge_card rebuild_index export_csv)
  while true; do
    local lvl=$(pick INFO INFO INFO INFO DEBUG WARN ERROR) col=$G
    case $lvl in DEBUG) col=$D ;; WARN) col=$Y ;; ERROR) col=$R ;; esac
    local job=$(pick "${jobs[@]}") id=$((RANDOM * 7 % 100000))
    local msg
    case $lvl in
      ERROR) msg="job $job#$id failed: $(pick 'connection reset by peer' 'timeout after 30s' 'deadlock detected' 'NoneType has no attribute id')" ;;
      WARN)  msg="job $job#$id retry $((RANDOM % 5 + 1))/5" ;;
      DEBUG) msg="queue depth=$((RANDOM % 300)) workers=$((RANDOM % 8 + 1))" ;;
      *)     msg="job $job#$id done in $((RANDOM % 5000))ms" ;;
    esac
    printf '%s [%s%-5s%s] worker-%d: %s\n' "$(ts)" "$col" "$lvl" "$N" $((RANDOM % 4 + 1)) "$msg"
    sleep "0.$((RANDOM % 6))$((RANDOM % 9 + 1))"
  done
}

build() {
  while true; do
    printf '%s$ make all%s\n' "$B" "$N"
    local total=$((RANDOM % 30 + 20)) i
    for ((i = 1; i <= total; i++)); do
      printf '[%3d%%] %sCC%s src/%s/%s.o\n' $((i * 100 / total)) "$M" "$N" \
        "$(pick core net ui storage util)" "$(pick parser buffer socket render cache hash tree pool)"
      ((RANDOM % 12 == 0)) && printf '%swarning:%s unused variable %s\n' "$Y" "$N" "'$(pick tmp ret len ctx)'"
      sleep "0.$((RANDOM % 4))$((RANDOM % 9 + 1))"
    done
    printf '%sLD%s bin/app\n' "$C" "$N"; sleep 1
    if ((RANDOM % 4 == 0)); then
      printf '%s✗ build failed%s (exit 2)\n\n' "$R" "$N"
    else
      printf '%s✓ build ok%s in %d.%ds\n\n' "$G" "$N" $((RANDOM % 60)) $((RANDOM % 10))
    fi
    sleep 3
  done
}

tests() {
  while true; do
    printf '%s$ pytest -q%s\n' "$B" "$N"
    local pass=0 fail=0 f
    for f in test_auth test_orders test_cart test_search test_api test_models test_utils; do
      printf '%-18s ' "tests/$f.py"
      local n=$((RANDOM % 20 + 5))
      for ((i = 0; i < n; i++)); do
        if ((RANDOM % 30 == 0)); then printf '%sF%s' "$R" "$N"; ((fail++)); else printf '%s.%s' "$G" "$N"; ((pass++)); fi
        sleep 0.08
      done
      echo
    done
    local col=$G; ((fail)) && col=$R
    printf '%s%d passed, %d failed%s\n\n' "$col" "$pass" "$fail" "$N"
    sleep 4
  done
}

metrics() {
  local cpu=40 mem=55 rps=300 i
  bar() {
    local f=$(($1 * 40 / 100)) j s=$2
    for ((j = 0; j < 40; j++)); do ((j == f)) && s+=$N$D; ((j < f)) && s+='█' || s+='░'; done
    printf '%s%s' "$s" "$N"
  }
  clamp() { local v=$1; ((v < 2)) && v=2; ((v > 98)) && v=98; echo $v; }
  while true; do
    cpu=$(clamp $((cpu + RANDOM % 21 - 10)))
    mem=$(clamp $((mem + RANDOM % 7 - 3)))
    rps=$((rps + RANDOM % 81 - 40)); ((rps < 0)) && rps=0
    clear
    printf '%s  host: prod-web-01   %s%s\n\n' "$C" "$(date +%T)" "$N"
    printf '  CPU  %3d%% ' "$cpu"; bar "$cpu" "$G"; echo
    printf '  MEM  %3d%% ' "$mem"; bar "$mem" "$Y"; echo
    printf '  RPS  %4d\n\n' "$rps"
    printf '  %-8s %6s %6s  %s\n' PID CPU% MEM% COMMAND
    for i in 1 2 3 4 5 6; do
      printf '  %-8d %5d.%d %5d.%d  %s\n' $((RANDOM % 30000 + 1000)) $((RANDOM % 60)) $((RANDOM % 10)) \
        $((RANDOM % 20)) $((RANDOM % 10)) "$(pick nginx postgres node redis-server python3 java)"
    done
    sleep 1
  done
}

deploy() {
  local envs=(staging canary prod)
  while true; do
    local v="v1.$((RANDOM % 20)).$((RANDOM % 50))" e
    printf '%s==> deploying %s%s\n' "$M" "$v" "$N"
    for e in "${envs[@]}"; do
      printf '  %s: pulling image' "$e"
      for i in 1 2 3 4 5; do printf '.'; sleep 0.4; done
      printf ' rolling %d pods' $((RANDOM % 6 + 2)); sleep 1
      if ((RANDOM % 6 == 0)); then printf ' %sFAILED%s, rolling back\n' "$R" "$N"; sleep 2; break; fi
      printf ' %sok%s\n' "$G" "$N"; sleep 1
    done
    echo; sleep 5
  done
}

chat() {
  local who=(alice bob carol dave eve)
  local said=('anyone seen the 500s on /api/cart?' 'deploying now' 'lgtm' 'rebasing, one sec'
    'the build is flaky again' 'coffee?' 'ship it' 'can you review #482?' 'reverting that, my bad'
    'p99 looks better after the cache change' 'who owns the worker queue?' '👍')
  while true; do
    printf '%s%s%s %s<%s>%s %s\n' "$D" "$(date +%H:%M)" "$N" "$C" "$(pick "${who[@]}")" "$N" "$(pick "${said[@]}")"
    sleep $((RANDOM % 4 + 1))
  done
}

# procs looks like top, with made-up processes, so nothing real is shown.
procs() {
  local cmds=(nginx postgres node redis-server python3 java sidekiq envoy) users=(app postgres redis www-data root)
  local up=$((RANDOM % 90 + 3)) i
  while true; do
    clear
    printf 'top - %s up %d days,  load average: %d.%02d, %d.%02d, %d.%02d\n' "$(date +%T)" "$up" \
      $((RANDOM % 4)) $((RANDOM % 100)) $((RANDOM % 3)) $((RANDOM % 100)) $((RANDOM % 2)) $((RANDOM % 100))
    printf 'Tasks: %d total, %d running   %%Cpu(s): %d.%d us, %d.%d sy\n\n' $((RANDOM % 80 + 180)) $((RANDOM % 5 + 1)) \
      $((RANDOM % 40)) $((RANDOM % 10)) $((RANDOM % 10)) $((RANDOM % 10))
    printf '%s  %-7s %-9s %6s %6s  %s%s\n' "$D" PID USER %CPU %MEM COMMAND "$N"
    for i in 1 2 3 4 5 6 7 8; do
      printf '  %-7d %-9s %4d.%d %4d.%d  %s\n' $((RANDOM % 30000 + 1000)) "$(pick "${users[@]}")" \
        $((RANDOM % 60)) $((RANDOM % 10)) $((RANDOM % 15)) $((RANDOM % 10)) "$(pick "${cmds[@]}")"
    done
    sleep 2
  done
}

db_queries() {
  local tables=(users orders order_items carts sessions products events)
  while true; do
    local ms=$((RANDOM % 400)) col=$G q
    ((ms > 200)) && col=$Y; ((ms > 350)) && col=$R
    case $((RANDOM % 4)) in
      0) q="SELECT * FROM $(pick "${tables[@]}") WHERE id = $RANDOM" ;;
      1) q="UPDATE $(pick "${tables[@]}") SET updated_at = now() WHERE id = $RANDOM" ;;
      2) q="INSERT INTO events (kind, user_id) VALUES ('$(pick click view buy)', $RANDOM)" ;;
      *) q="DELETE FROM sessions WHERE expires_at < now() -- $((RANDOM % 90)) rows" ;;
    esac
    printf '%s %s%4dms%s %s\n' "$(ts)" "$col" "$ms" "$N" "$q"
    nap
  done
}

pings() {
  local hosts=(db-1 db-2 cache-1 api-1 api-2 queue-1)
  while true; do
    local h=$(pick "${hosts[@]}")
    if ((RANDOM % 25 == 0)); then
      printf '%-8s %srequest timeout%s\n' "$h" "$R" "$N"
    else
      printf '%-8s %s%3d ms%s  ttl=%d\n' "$h" "$G" $((RANDOM % 80 + 1)) "$N" $((RANDOM % 10 + 54))
    fi
    sleep 0.5
  done
}

git_log() {
  local who=(alice bob carol dave eve)
  local verbs=(fix add remove refactor bump update)
  local things=("cart totals" "login rate limit" "search index" "flaky test" "deps" "README" "worker retries")
  while true; do
    printf '%s%07x%s %s %s %s(%s)%s\n' "$Y" $((RANDOM * RANDOM % 268435456)) "$N" \
      "$(pick "${verbs[@]}")" "$(pick "${things[@]}")" "$D" "$(pick "${who[@]}")" "$N"
    sleep $((RANDOM % 5 + 2))
  done
}

pods() {
  local names=(api worker web cron redis postgres) i
  while true; do
    clear
    printf '%sNAME                        READY   STATUS              RESTARTS   AGE%s\n' "$D" "$N"
    for i in 1 2 3 4 5 6 7 8; do
      local st=Running col=$G ready=1
      case $((RANDOM % 12)) in
        0) st=CrashLoopBackOff col=$R ready=0 ;;
        1) st=ContainerCreating col=$Y ready=0 ;;
      esac
      printf '%-27s %d/1     %s%-19s%s %-10d %dm\n' "$(pick "${names[@]}")-$((RANDOM % 900 + 100))-x$((RANDOM % 90 + 10))" \
        "$ready" "$col" "$st" "$N" $((RANDOM % 4)) $((RANDOM % 600))
    done
    sleep 2
  done
}

ticker() {
  local syms=(ACME GLOBX INITE UMBR SOYL) price=(120 45 310 88 12) i
  while true; do
    i=$((RANDOM % ${#syms[@]}))
    local d=$((RANDOM % 7 - 3)) col=$G arrow=▲
    ((d < 0)) && col=$R arrow=▼
    price[i]=$((price[i] + d > 1 ? price[i] + d : 2))
    printf '%s %-6s %6d.%02d %s%s %d.%02d%%%s\n' "$(date +%T)" "${syms[i]}" "${price[i]}" $((RANDOM % 100)) \
      "$col" "$arrow" $((${d#-})) $((RANDOM % 100)) "$N"
    sleep 0.7
  done
}

cron_log() {
  local jobs=(backup-db rotate-logs send-digest renew-certs cleanup-tmp reindex)
  while true; do
    local j=$(pick "${jobs[@]}")
    printf '%s %sCRON%s starting %s\n' "$(ts)" "$M" "$N" "$j"
    sleep $((RANDOM % 3 + 1))
    if ((RANDOM % 8 == 0)); then
      printf '%s %sCRON%s %s %sexited 1%s\n' "$(ts)" "$M" "$N" "$j" "$R" "$N"
    else
      printf '%s %sCRON%s %s %sok%s\n' "$(ts)" "$M" "$N" "$j" "$G" "$N"
    fi
    sleep $((RANDOM % 4 + 2))
  done
}

idle() {
  cd ~ && exec bash -i
}

# create makes 13 windows: a centred overview shows the window in focus with a
# ring of 12 around it, so 13 fill it with no empty cells, and the even
# overview of 13 has no gaps either.
create() {
  tmux kill-session -t "$SESSION" 2>/dev/null
  tmux new-session -d -s "$SESSION" -n api "$SELF api_log"
  tmux new-window -t "$SESSION" -n workers "$SELF worker_log"
  tmux new-window -t "$SESSION" -n build "$SELF build"
  tmux split-window -t "$SESSION:build" -h "$SELF tests"
  tmux new-window -t "$SESSION" -n metrics "$SELF metrics"
  tmux new-window -t "$SESSION" -n deploy "$SELF deploy"
  tmux split-window -t "$SESSION:deploy" -v -l 30% "$SELF chat"
  tmux new-window -t "$SESSION" -n shell
  tmux new-window -t "$SESSION" -n top "$SELF procs"
  tmux new-window -t "$SESSION" -n db "$SELF db_queries"
  tmux new-window -t "$SESSION" -n ping "$SELF pings"
  tmux new-window -t "$SESSION" -n git "$SELF git_log"
  tmux new-window -t "$SESSION" -n pods "$SELF pods"
  tmux new-window -t "$SESSION" -n ticker "$SELF ticker"
  tmux new-window -t "$SESSION" -n cron "$SELF cron_log"
  tmux select-window -t "$SESSION:api"
  echo "session '$SESSION' created: $(tmux list-windows -t "$SESSION" -F '#W' | paste -sd' ')"
}

case ${1:-create} in
  create) create ;;
  kill) tmux kill-session -t "$SESSION" ;;
  api_log | worker_log | build | tests | metrics | deploy | chat | idle | procs | \
    db_queries | pings | git_log | pods | ticker | cron_log) "$1" ;;
  *) echo "usage: $0 [create|kill]" >&2; exit 1 ;;
esac
