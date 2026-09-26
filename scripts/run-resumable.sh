#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
STATE_ROOT="${LOCAL_MINIMCP_STATE_DIR:-${XDG_STATE_HOME:-${HOME}/.local/state}/local-minimcp/jobs}"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

usage() {
    cat <<'USAGE'
Usage:
  scripts/run-resumable.sh start [--wake-lock] [--expect-marker TEXT] JOB -- COMMAND [ARG...]
  scripts/run-resumable.sh status JOB
  scripts/run-resumable.sh log JOB [LINES]
  scripts/run-resumable.sh cleanup JOB
  scripts/run-resumable.sh list

Jobs persist under ~/.local/state/local-minimcp/jobs by default.
The command itself and its arguments are never written to job metadata.
USAGE
}

atomic_write() {
    local path="$1"
    local value="$2"
    local tmp="${path}.tmp.$$"
    printf '%s\n' "$value" > "$tmp"
    mv -f "$tmp" "$path"
}

validate_job() {
    local job="$1"
    [[ "$job" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]] || \
        fail "invalid job id: use 1-96 characters from [A-Za-z0-9._-]"
}

validate_marker() {
    local marker="$1"
    [ -z "$marker" ] && return 0
    [[ "$marker" =~ ^[A-Za-z0-9][A-Za-z0-9._:=-]{0,127}$ ]] || \
        fail "invalid expected marker"
}

job_dir() {
    printf '%s/%s\n' "$STATE_ROOT" "$1"
}

lock_is_held() {
    local dir="$1"
    [ -e "$dir/lock" ] || return 1
    if flock -n "$dir/lock" -c true >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

meta_value() {
    local dir="$1"
    local key="$2"
    [ -f "$dir/meta" ] || return 0
    sed -n "s/^${key}=//p" "$dir/meta" | head -n 1
}

cmd_status() {
    local job="$1"
    validate_job "$job"
    local dir
    dir="$(job_dir "$job")"
    [ -d "$dir" ] || fail "job not found: $job"

    local state rc="" marker marker_seen="no" pid=""
    marker="$(meta_value "$dir" expected_marker)"
    [ -f "$dir/pid" ] && pid="$(cat "$dir/pid" 2>/dev/null || true)"

    if lock_is_held "$dir"; then
        state="RUNNING"
    elif [ -f "$dir/rc" ]; then
        rc="$(cat "$dir/rc")"
        if [ "$rc" = "0" ]; then
            if [ -n "$marker" ]; then
                if grep -Fq -- "$marker" "$dir/log" 2>/dev/null; then
                    marker_seen="yes"
                    state="VERIFIED"
                else
                    state="FINISHED"
                fi
            else
                state="FINISHED"
            fi
        else
            state="FAILED"
        fi
    else
        state="INTERRUPTED"
    fi

    printf 'JOB=%s\n' "$job"
    printf 'STATE=%s\n' "$state"
    [ -n "$pid" ] && printf 'PID=%s\n' "$pid"
    [ -n "$rc" ] && printf 'RC=%s\n' "$rc"
    if [ -n "$marker" ]; then
        printf 'EXPECTED_MARKER=%s\n' "$marker"
        printf 'MARKER_SEEN=%s\n' "$marker_seen"
    fi
    printf 'LOG=%s\n' "$dir/log"
}

cmd_start() {
    local wake_lock=0
    local expected_marker=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --wake-lock)
                wake_lock=1
                shift
                ;;
            --expect-marker)
                [ "$#" -ge 2 ] || fail "--expect-marker requires a value"
                expected_marker="$2"
                shift 2
                ;;
            --)
                fail "job id is required before --"
                ;;
            -*)
                fail "unknown start option: $1"
                ;;
            *)
                break
                ;;
        esac
    done

    [ "$#" -ge 3 ] || fail "start requires JOB -- COMMAND [ARG...]"
    local job="$1"
    shift
    [ "$1" = "--" ] || fail "expected -- before command"
    shift
    [ "$#" -gt 0 ] || fail "command is required"

    validate_job "$job"
    validate_marker "$expected_marker"
    for cmd in flock nohup setsid date mkdir mv basename; do
        command -v "$cmd" >/dev/null 2>&1 || fail "$cmd is required"
    done
    if [ "$wake_lock" -eq 1 ]; then
        command -v termux-wake-lock >/dev/null 2>&1 || fail "--wake-lock requires Termux:API termux-wake-lock"
        command -v termux-wake-unlock >/dev/null 2>&1 || fail "--wake-lock requires Termux:API termux-wake-unlock"
    fi

    mkdir -p "$STATE_ROOT"
    chmod 0700 "$STATE_ROOT"
    local dir
    dir="$(job_dir "$job")"
    if ! mkdir "$dir" 2>/dev/null; then
        if [ -d "$dir" ] && lock_is_held "$dir"; then
            fail "job is already running: $job"
        fi
        fail "job state already exists: $job (inspect it, then cleanup before reusing the id)"
    fi
    chmod 0700 "$dir"
    : > "$dir/log"
    : > "$dir/lock"

    local started command_name
    started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    command_name="$(basename "$1")"
    cat > "$dir/meta" <<META
job=$job
started_at=$started
command_name=$command_name
expected_marker=$expected_marker
wake_lock=$wake_lock
META
    atomic_write "$dir/state" "STARTING"

    nohup setsid flock -n "$dir/lock" "$SELF" __worker "$dir" "$wake_lock" -- "$@" \
        </dev/null >>"$dir/log" 2>&1 &
    local launcher_pid=$!
    atomic_write "$dir/pid" "$launcher_pid"

    local i=0
    while [ "$i" -lt 60 ]; do
        if [ -f "$dir/rc" ] || [ "$(cat "$dir/state" 2>/dev/null || true)" = "RUNNING" ]; then
            break
        fi
        i=$((i + 1))
        sleep 0.05
    done

    cmd_status "$job"
}

cmd_log() {
    local job="$1"
    local lines="${2:-80}"
    validate_job "$job"
    [[ "$lines" =~ ^[0-9]+$ ]] || fail "LINES must be an integer"
    local dir
    dir="$(job_dir "$job")"
    [ -f "$dir/log" ] || fail "job log not found: $job"
    tail -n "$lines" "$dir/log"
}

cmd_cleanup() {
    local job="$1"
    validate_job "$job"
    local dir
    dir="$(job_dir "$job")"
    [ -d "$dir" ] || fail "job not found: $job"
    lock_is_held "$dir" && fail "refusing cleanup while job is running: $job"
    rm -rf "$dir"
    printf 'JOB_CLEANED=%s\n' "$job"
}

cmd_list() {
    mkdir -p "$STATE_ROOT"
    local found=0 dir job
    shopt -s nullglob
    for dir in "$STATE_ROOT"/*; do
        [ -d "$dir" ] || continue
        found=1
        job="$(basename "$dir")"
        cmd_status "$job"
        printf '\n'
    done
    [ "$found" -eq 1 ] || echo "NO_JOBS"
}

worker() {
    local dir="$1"
    local wake_lock="$2"
    shift 2
    [ "$1" = "--" ] || exit 64
    shift

    atomic_write "$dir/pid" "$$"
    atomic_write "$dir/state" "RUNNING"

    MCP_PI_RESUMABLE_JOB_ID="$(meta_value "$dir" job)"
    export MCP_PI_RESUMABLE_JOB_ID
    export MCP_PI_RESUMABLE_STATE_DIR="$dir"

    local wake_acquired=0
    if [ "$wake_lock" -eq 1 ]; then
        termux-wake-lock
        wake_acquired=1
    fi
    release_wake_lock() {
        if [ "$wake_acquired" -eq 1 ]; then
            termux-wake-unlock >/dev/null 2>&1 || true
        fi
    }
    trap release_wake_lock EXIT

    set +e
    "$@"
    local rc=$?
    set -e

    atomic_write "$dir/finished_at" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    atomic_write "$dir/rc" "$rc"
    if [ "$rc" -eq 0 ]; then
        atomic_write "$dir/state" "FINISHED"
    else
        atomic_write "$dir/state" "FAILED"
    fi
    exit "$rc"
}

case "${1:-}" in
    start)
        shift
        cmd_start "$@"
        ;;
    status)
        [ "$#" -eq 2 ] || { usage >&2; exit 2; }
        cmd_status "$2"
        ;;
    log)
        [ "$#" -ge 2 ] && [ "$#" -le 3 ] || { usage >&2; exit 2; }
        cmd_log "$2" "${3:-80}"
        ;;
    cleanup)
        [ "$#" -eq 2 ] || { usage >&2; exit 2; }
        cmd_cleanup "$2"
        ;;
    list)
        [ "$#" -eq 1 ] || { usage >&2; exit 2; }
        cmd_list
        ;;
    __worker)
        shift
        worker "$@"
        ;;
    -h|--help|help|"")
        usage
        ;;
    *)
        usage >&2
        fail "unknown command: $1"
        ;;
esac
