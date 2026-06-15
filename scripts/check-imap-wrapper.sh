#!/usr/bin/env bash
# CI gate: every imapclient.* call must live in internal/mailbox/imap.go.
# Single greppable surface for the IMAP wrapper. See SPEC §IMAP wrapper invariant.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

hits=$(grep -RnE '\bimapclient\.' internal/ 2>/dev/null \
       | grep -v 'internal/mailbox/imap.go' \
       | grep -vE ':[[:space:]]*//' \
       | grep -vE ':[[:space:]]*\*' \
       || true)

if [[ -n "$hits" ]]; then
  echo "FAIL: imapclient.* used outside internal/mailbox/imap.go" >&2
  echo "$hits" >&2
  exit 1
fi

echo "OK: imapclient.* confined to internal/mailbox/imap.go"
