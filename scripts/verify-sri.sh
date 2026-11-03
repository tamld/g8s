#!/usr/bin/env bash
# Mechanical Subresource Integrity (SRI) & CSP Inline-Style Verifier
set -euo pipefail

CSS_FILE="assets/site/styles.css"
JS_FILE="assets/site/main.js"

if [ ! -f "$CSS_FILE" ] || [ ! -f "$JS_FILE" ]; then
  echo "❌ FAIL: Target assets do not exist ($CSS_FILE or $JS_FILE)"
  exit 1
fi

CSS_HASH="sha384-$(openssl dgst -sha384 -binary "$CSS_FILE" | openssl base64 -A)"
JS_HASH="sha384-$(openssl dgst -sha384 -binary "$JS_FILE" | openssl base64 -A)"

echo "==> [SRI Gate] Expected CSS SHA-384: $CSS_HASH"
echo "==> [SRI Gate] Expected JS  SHA-384: $JS_HASH"

for html in index.html sites/g8s.html sites/aegis.html; do
  if [ ! -f "$html" ]; then
    continue
  fi
  if [ "$html" = "index.html" ]; then
    C_HASH="$CSS_HASH"; J_HASH="$JS_HASH"
  else
    if [ -f assets/css/flowaxon.css ] && [ -f assets/js/flowaxon.js ]; then
      C_HASH="sha384-$(openssl dgst -sha384 -binary assets/css/flowaxon.css | openssl base64 -A)"
      J_HASH="sha384-$(openssl dgst -sha384 -binary assets/js/flowaxon.js | openssl base64 -A)"
    else
      continue
    fi
  fi
  if ! grep -Fq "$C_HASH" "$html"; then
    echo "❌ FAIL: $html does not contain matching CSS SRI hash ($C_HASH)"
    exit 1
  fi
  if ! grep -Fq "$J_HASH" "$html"; then
    echo "❌ FAIL: $html does not contain matching JS SRI hash ($J_HASH)"
    exit 1
  fi
  if grep -Eq ' style="' "$html"; then
    echo "❌ FAIL: $html contains inline style=\"...\" violating CSP style-src 'self'"
    exit 1
  fi
done

echo "✅ PASS: HTML portal matches exact SHA-384 SRI hashes and contains zero inline styles."
