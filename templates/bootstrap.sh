#!/bin/bash
set -e

if [ -z "$1" ]; then
    echo "Usage: ./bootstrap.sh <module-path>"
    echo "Example: ./bootstrap.sh github.com/yourorg/myapp"
    exit 1
fi

MODULE_PATH=$1

echo "Replacing module path with: $MODULE_PATH"

# Replace in all Go files
find . -type f -name "*.go" -exec sed -i '' "s|github.com/yourorg/myapp|$MODULE_PATH|g" {} +

# Replace in go.mod
sed -i '' "s|github.com/yourorg/myapp|$MODULE_PATH|g" go.mod

# Replace in skimatik.yaml if it has any references
if [ -f skimatik.yaml ]; then
    sed -i '' "s|github.com/yourorg/myapp|$MODULE_PATH|g" skimatik.yaml
fi

echo ""
echo "Bootstrap complete! Next steps:"
echo ""
echo "  1. cp .env.example .env"
echo "  2. Edit .env with your database credentials"
echo "  3. make db-up"
echo "  4. make migrate-up"
echo "  5. skimatik generate"
echo "  6. make run"
echo ""
