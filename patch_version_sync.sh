#!/bin/bash
# Modify version-sync.yml to directly compare GO_MOD_VERSION with WORKFLOW_VERSION
sed -i 's/GO_MOD_MAJOR_MINOR=$(echo "$GO_MOD_VERSION" | cut -d. -f1,2)/GO_MOD_MAJOR_MINOR=$GO_MOD_VERSION/g' .github/workflows/version-sync.yml
