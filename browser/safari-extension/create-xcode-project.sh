#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
EXTENSION_SOURCE="$REPO_ROOT/browser/chrome-extension"
PROJECT_ROOT="${1:-$SCRIPT_DIR/build/dbrain-link-saver-safari}"

xcrun safari-web-extension-converter "$EXTENSION_SOURCE" \
	--project-location "$PROJECT_ROOT" \
	--app-name "dbrain Link Saver" \
	--bundle-identifier "com.darron.dbrain.linksaver" \
	--swift \
	--macos-only \
	--no-open \
	--no-prompt \
	--force
