#!/usr/bin/env bash
# Offline-friendly Terraform quality gate. init downloads only the pinned provider;
# it never creates, changes, or destroys Alibaba Cloud resources.
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v terraform >/dev/null || { echo "terraform is required" >&2; exit 1; }

terraform -chdir="$ROOT" fmt -check -recursive
terraform -chdir="$ROOT" init -backend=false -input=false
terraform -chdir="$ROOT" validate
echo "terraform fmt/init/validate: OK"
