#!/bin/bash
# /pair - Pair Programming Mode
set -e

subcommand="${1:-status}"
shift || true

case "$subcommand" in
    start)
        partner="$1"
        echo "👥 **Pair Programming Started**"
        echo ""
        echo "Partner: $partner"
        echo "Session: pair_$(date +%s)"
        echo ""
        echo "✅ Pair mode active!"
        ;;
    stop)
        echo "👥 **Pair Programming Stopped**"
        echo ""
        echo "✅ Pair mode deactivated"
        ;;
    status)
        echo "👥 **Pair Programming Status**"
        echo ""
        echo "Status: inactive"
        echo ""
        echo "Use /pair start <partner> para iniciar"
        ;;
esac
