#!/usr/bin/env bash
set -euo pipefail

COUNT=3
greeting="hello"

say() {
    local who="$1"
    echo "${greeting}, ${who}"
}

build() {
    for i in $(seq 1 "$COUNT"); do
        say "world $i"
    done
}

if [ "$COUNT" -gt 1 ]; then
    build
fi
