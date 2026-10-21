#!/usr/bin/env bash
# Binaries built before go1.27.3 / go1.26.10 trust the compromised eMudhra
# intermediates (CVE-2026-111943).
set -euo pipefail

status=0
for bin in "$@"; do
  version=$(go version "$bin" | awk '{print $2}')
  if [[ ! $version =~ ^go1\.([0-9]+)(\.([0-9]+))? ]]; then
    echo "::error::$bin: cannot parse Go version '$version'"
    status=1
    continue
  fi
  minor=${BASH_REMATCH[1]}
  patch=${BASH_REMATCH[3]:-0}
  if (( minor > 27 )) || (( minor == 27 && patch >= 3 )) || (( minor == 26 && patch >= 10 )); then
    echo "$bin: $version ok"
  else
    echo "::error::$bin: built with $version, need go1.27.3+ or go1.26.10+"
    status=1
  fi
done
exit $status
