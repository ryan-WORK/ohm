#!/usr/bin/env bash
set -e
cd "$(dirname "$0")"
mkdir -p bin
go build -o bin/ohm .
echo "ohm: built bin/ohm"
