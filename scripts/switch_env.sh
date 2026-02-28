#!/usr/bin/env bash

# switch_env.sh — Select active environment from opt/envs/
# Copies opt/envs/.env_<target> → .env (and envcfg_<target>.yaml → envcfg.yaml if present)
#
# Usage:
#   ./scripts/switch_env.sh <environment_name>
#   ./scripts/switch_env.sh              # auto-detect from hostname
#
# This is a LOCAL DEV tool only. In prod/test only root .env is used directly.

set -euo pipefail
cd "$(dirname "$0")/.."

TARGET=${1:-}

# Auto-detect from hostname if no argument
if [ -z "$TARGET" ]; then
    CURRENT_HOST=$(hostname)
    if [ -f "opt/envs/.env_$CURRENT_HOST" ] || [ -f "opt/envs/.env_$CURRENT_HOST.gpg" ]; then
        TARGET="$CURRENT_HOST"
        echo "Detected Host: $CURRENT_HOST → Selecting '$TARGET'"
    else
        echo "Unknown host: $CURRENT_HOST"
        echo "Usage: ./scripts/switch_env.sh <environment_name>"
        echo ""
        echo "Available environments in opt/envs/:"
        ls opt/envs/ 2>/dev/null || echo "  (none — create opt/envs/.env_<name> first)"
        exit 1
    fi
fi

SOURCE_ENV="opt/envs/.env_${TARGET}"
SOURCE_CFG="opt/envs/envcfg_${TARGET}.yaml"

# Unlock encrypted .env if needed
if [ ! -f "$SOURCE_ENV" ]; then
    if [ -f "${SOURCE_ENV}.gpg" ]; then
        echo "Encrypted version found. Unlocking..."
        if [ -f "./scripts/vault.sh" ]; then
            ./scripts/vault.sh unlock "$SOURCE_ENV"
        else
            echo "Error: vault.sh not found. Decrypt manually: gpg -d ${SOURCE_ENV}.gpg > $SOURCE_ENV"
            exit 1
        fi
    else
        echo "Error: '$SOURCE_ENV' does not exist."
        echo ""
        echo "Available environments in opt/envs/:"
        ls opt/envs/ 2>/dev/null || echo "  (none)"
        exit 1
    fi
fi

# Copy secrets — filter out deployment-only vars
grep -vE "^(REMOTE_|VAULT_PASS)" "$SOURCE_ENV" > .env
echo "✓ Secrets: $SOURCE_ENV → .env"

# Copy config overlay if present
if [ -f "$SOURCE_CFG" ]; then
    cp "$SOURCE_CFG" envcfg.yaml
    echo "✓ Config:  $SOURCE_CFG → envcfg.yaml"
fi

echo ""
echo "Switched to '$TARGET'"
echo "---"
grep "^PG_HOST\|^OLLAMA\|^AI_PROVIDER\|^ENV_NAME" .env 2>/dev/null || true
