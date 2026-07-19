#!/bin/sh
# RED Engine container entrypoint.
#
# Security model for config.json loss:
#   If config.json is absent AND RED_ADMIN_TOKEN is not set, the node starts
#   in read-only lockdown — content is served but the admin panel is disabled.
#   No stub config.json is auto-generated; doing so would create an unknown
#   token and lock the operator out of their own node.
#
#   To restore admin access after losing config.json:
#     Option A — set RED_ADMIN_TOKEN in the .env file and restart.
#     Option B — restore config.json from backup and restart.

if [ ! -f "/app/config.json" ] && [ -z "$RED_ADMIN_TOKEN" ]; then
    echo ""
    echo "=========================================================="
    echo "  WARNING: No config.json and RED_ADMIN_TOKEN is not set."
    echo "  The node will start in READ-ONLY LOCKDOWN mode."
    echo "  Admin panel is DISABLED until credentials are restored."
    echo ""
    echo "  To restore access:"
    echo "    1. Set RED_ADMIN_TOKEN in your .env file, OR"
    echo "    2. Restore config.json from backup."
    echo "  Then restart the container."
    echo "=========================================================="
    echo ""
fi

if [ "$(id -u)" = "0" ]; then
    # Running as root (standard Docker): fix permissions, then drop to reduser.
    chown -R reduser:redgroup /app/data
    [ -d "/app/state" ] && chown -R reduser:redgroup /app/state
    [ -f "/app/config.json" ] && chown reduser:redgroup /app/config.json
    exec su-exec reduser "$@"
else
    # Running as non-root (Podman rootless keep-id): permissions already mapped.
    exec "$@"
fi
