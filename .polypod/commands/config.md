#!/bin/bash
# /config-profile - Config Profiles
set -e

profile="${1:-status}"

case "$profile" in
    dev)
        echo "⚙️ **Switching to:** dev profile"
        ;;
    prod)
        echo "⚙️ **Switching to:** prod profile"
        ;;
    status)
        echo "⚙️ **Current Profile:** default"
        echo ""
        echo "**Profiles:**"
        echo "  - default"
        echo "  - dev"
        echo "  - prod"
        ;;
esac
