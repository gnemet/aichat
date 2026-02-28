#!/bin/bash

# 🛡️ Digital Blacksmith: Vault Manager
# This script handles GPG symmetric encryption for sensitive configuration files.

ACTION=$1 # lock, unlock, or status
FILE=$2   # target file (e.g., .env)

usage() {
    echo "Usage: $0 [lock|unlock|status|verify|diff] [file_path]"
    echo "Example: $0 lock .env"
    echo ""
    echo "❌ Error: VAULT_PASS environment variable MUST be set."
    exit 1
}

check_vault_pass() {
    if [[ -z "$VAULT_PASS" ]]; then
        if [[ -f "./.vault_pass" ]]; then
            VAULT_PASS=$(cat "./.vault_pass")
        elif [[ -f "$HOME/.vault_pass" ]]; then
            VAULT_PASS=$(cat "$HOME/.vault_pass")
        fi
    fi

    if [[ -z "$VAULT_PASS" ]]; then
        echo "❌ Error: VAULT_PASS environment variable is not set and no .vault_pass file found."
        echo "Please set it: export VAULT_PASS='your_secret_password'"
        echo "OR create a file: echo 'your_password' > ~/.vault_pass"
        exit 1
    fi
}

if [[ -z "$ACTION" || -z "$FILE" ]]; then
    usage
fi

# 📂 Default directory for environment files
DEFAULT_VAULT_DIR="opt/envs"
TARGET_FILE="$FILE"

# 🔍 Auto-resolve shortcuts (e.g., 'zenbook' -> 'opt/envs/.env_zenbook')
if [[ ! -f "$FILE" && ! -f "$FILE.gpg" ]]; then
    if [[ -f "$DEFAULT_VAULT_DIR/.env_$FILE" || -f "$DEFAULT_VAULT_DIR/.env_$FILE.gpg" ]]; then
        FILE="$DEFAULT_VAULT_DIR/.env_$FILE"
        echo "💡 Auto-resolved shortcut to: $FILE"
        # We set TARGET_FILE to the resolved path so it unlocks in-place within opt/envs/
        TARGET_FILE="$FILE"
    fi
fi

check_gpg() {
    if ! command -v gpg &> /dev/null; then
        echo "❌ Error: gpg is not installed."
        exit 1
    fi
}

lock() {
    check_gpg
    check_vault_pass
    if [[ ! -f "$FILE" ]]; then
        echo "❌ Error: File '$FILE' not found."
        exit 1
    fi

    echo "🔒 Encrypting $FILE..."
    gpg --symmetric --cipher-algo AES256 --batch --yes --passphrase="$VAULT_PASS" --pinentry-mode loopback "$FILE"

    if [[ -f "$FILE.gpg" ]]; then
        echo "✅ Created $FILE.gpg"
        echo "💡 You can now safely commit $FILE.gpg to the repository."
    else
        echo "❌ Encryption failed."
        exit 1
    fi
}

unlock() {
    check_gpg
    check_vault_pass
    if [[ ! -f "$FILE.gpg" ]]; then
        echo "❌ Error: Encrypted file '$FILE.gpg' not found."
        exit 1
    fi

    echo "🔓 Decrypting $FILE.gpg..."
    TMP_FILE=$(mktemp)
    
    gpg --decrypt --batch --yes --passphrase="$VAULT_PASS" --pinentry-mode loopback "$FILE.gpg" > "$TMP_FILE"

    if [[ $? -eq 0 ]]; then
        mv "$TMP_FILE" "$TARGET_FILE"
        echo "✅ Restored $TARGET_FILE"
    else
        rm -f "$TMP_FILE"
        echo "❌ Decryption failed. Please check the GPG error above."
        exit 1
    fi
}

verify() {
    check_gpg
    if [[ ! -f "$FILE" ]]; then
        echo "❌ Error: Raw file '$FILE' not found."
        exit 1
    fi
    if [[ ! -f "$FILE.gpg" ]]; then
        echo "❌ Error: Encrypted file '$FILE.gpg' not found."
        exit 1
    fi

    echo "🔍 Verifying integrity of $FILE.gpg against $FILE..."
    
    # Get hash of raw file
    RAW_HASH=$(sha256sum "$FILE" | awk '{print $1}')
    
    # Get hash of decrypted content
    GPG_HASH=$(gpg --decrypt --batch --yes --passphrase="$VAULT_PASS" --pinentry-mode loopback "$FILE.gpg" 2>/dev/null | sha256sum | awk '{print $1}')

    if [[ "$RAW_HASH" == "$GPG_HASH" ]]; then
        echo "✨ Verification SUCCESS: Content matches (SHA256: $RAW_HASH)"
    else
        echo "❌ Verification FAILED: Content mismatch!"
        echo "Raw: $RAW_HASH"
        echo "GPG: $GPG_HASH"
        exit 1
    fi
}

diff_files() {
    check_gpg
    if [[ ! -f "$FILE" ]]; then
        echo "❌ Error: Raw file '$FILE' not found."
        exit 1
    fi
    if [[ ! -f "$FILE.gpg" ]]; then
        echo "❌ Error: Encrypted file '$FILE.gpg' not found."
        exit 1
    fi

    echo "🔍 Diffing $FILE against $FILE.gpg..."
    TMP_FILE=$(mktemp)
    
    gpg --decrypt --batch --yes --passphrase="$VAULT_PASS" --pinentry-mode loopback "$FILE.gpg" > "$TMP_FILE" 2>/dev/null

    if [[ $? -eq 0 ]]; then
        diff --color=always -u "$TMP_FILE" "$FILE"
        DIFF_RESULT=$?
        rm -f "$TMP_FILE"
        if [[ $DIFF_RESULT -eq 0 ]]; then
            echo "✨ No differences found."
        fi
    else
        rm -f "$TMP_FILE"
        echo "❌ Decryption failed."
        exit 1
    fi
}

status() {
    if [[ -f "$FILE" && -f "$FILE.gpg" ]]; then
        echo "🔍 Status for '$FILE': Both raw and encrypted files exist."
        # Compare timestamps
        if [[ "$FILE" -nt "$FILE.gpg" ]]; then
            echo "⚠️  Warning: Raw file is newer than encrypted file. Run '$0 lock $FILE' to update."
        else
            echo "✨ Everything is up to date."
        fi
    elif [[ -f "$FILE.gpg" ]]; then
        echo "🔍 Status for '$FILE': Only encrypted file exists. Run '$0 unlock $FILE' to restore raw file."
    elif [[ -f "$FILE" ]]; then
        echo "🔍 Status for '$FILE': Only raw file exists. Run '$0 lock $FILE' to protect it."
    else
        echo "🔍 Status for '$FILE': No files found."
    fi
}

case "$ACTION" in
    lock) lock ;;
    unlock) unlock ;;
    verify) verify ;;
    diff) diff_files ;;
    status) status ;;
    *) usage ;;
esac
