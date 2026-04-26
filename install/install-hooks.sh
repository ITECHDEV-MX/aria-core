#!/usr/bin/env bash
# install-hooks.sh — convenience installer for ARIA passive capture.
#
# Detects whether the current directory is an iTechDev repo (matches
# ITECHDEV-MX in the origin remote) and runs `aria-core hooks install`.
# If not run from an iTechDev repo and --force is not passed, the script
# prints a warning and exits without modifying hooks.
#
# Usage:
#   install/install-hooks.sh [--global] [--force] [--repo=PATH]
set -eu

GLOBAL=""
FORCE=""
REPO=""

for arg in "$@"; do
  case "$arg" in
    --global) GLOBAL="--global" ;;
    --force)  FORCE="--force" ;;
    --repo=*) REPO="${arg#--repo=}" ;;
    -h|--help)
      cat <<USAGE
install-hooks.sh — install ARIA passive-capture hooks

Options:
  --global       install globally (~/.git-hooks-global)
  --force        overwrite foreign hooks
  --repo=PATH    target a specific repo (default: cwd)
USAGE
      exit 0
      ;;
  esac
done

if ! command -v aria-core >/dev/null 2>&1; then
  echo "error: aria-core binary not on PATH" >&2
  exit 1
fi

TARGET_DIR="${REPO:-$(pwd)}"

if [ -z "$GLOBAL" ]; then
  REMOTE=$(git -C "$TARGET_DIR" remote get-url origin 2>/dev/null || echo "")
  if [ -z "$REMOTE" ]; then
    echo "warning: $TARGET_DIR has no origin remote — hooks will be installed anyway" >&2
  elif ! echo "$REMOTE" | grep -qi "ITECHDEV-MX"; then
    if [ -z "$FORCE" ]; then
      echo "skip: $TARGET_DIR is not an iTechDev repo (origin: $REMOTE). Re-run with --force to override." >&2
      exit 0
    fi
  fi
fi

CMD="aria-core hooks install"
[ -n "$REPO" ]   && CMD="$CMD --repo=$REPO"
[ -n "$GLOBAL" ] && CMD="$CMD $GLOBAL"
[ -n "$FORCE" ]  && CMD="$CMD $FORCE"

echo "+ $CMD"
exec $CMD
