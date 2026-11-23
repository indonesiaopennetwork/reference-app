#!/bin/sh
set -eu

mkdir -p /app/config

# Same result as `envsubst "$ENVSUBST_VARS"`: replaces only the ${NAME}
# placeholders listed in ENVSUBST_VARS (unset -> empty) and leaves every
# other $ untouched. Done in awk because the base image ships busybox awk
# but no envsubst, and its apk repo (apk.cgr.dev/chainguard) no longer
# serves gettext -- a clean build can't install it. Literal index()/substr()
# replacement, not gsub(), so & and \ in values (keys, URLs) stay as-is.
render() {
  awk -v vars="$ENVSUBST_VARS" '
    BEGIN {
      n = 0; s = vars
      while (match(s, /\$\{[A-Za-z_][A-Za-z0-9_]*\}/)) {
        names[++n] = substr(s, RSTART + 2, RLENGTH - 3)
        s = substr(s, RSTART + RLENGTH)
      }
    }
    {
      line = $0
      for (i = 1; i <= n; i++) {
        pat = "${" names[i] "}"; val = ENVIRON[names[i]]; done = ""
        while ((p = index(line, pat)) > 0) {
          done = done substr(line, 1, p - 1) val
          line = substr(line, p + length(pat))
        }
        line = done line
      }
      print line
    }' "$1"
}

for tmpl in /app/templates/*.tmpl; do
  out="/app/config/$(basename "${tmpl%.tmpl}")"
  render "$tmpl" > "$out"
done

for f in /app/templates/*; do
  case "$f" in
    *.tmpl) continue ;;
  esac
  cp "$f" "/app/config/$(basename "$f")"
done

exec "$@"
