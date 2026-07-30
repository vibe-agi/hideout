#!/bin/sh
set -eu

if [ "$#" -lt 3 ] || [ "$#" -gt 4 ]; then
  echo "usage: reference-workload.sh WORKSPACE DNS_NAME HTTP_URL [SOCKS5_URL]" >&2
  exit 2
fi

workspace="$1"
dns_name="$2"
http_url="$3"
socks_url="${4:-}"
fixture_dir="$workspace/hideout-observation-fixture"

mkdir -p "$fixture_dir/nested"
printf '%s\n' "fixture-line-one" >"$fixture_dir/source.txt"
cat "$fixture_dir/source.txt" >/dev/null
printf '%s\n' "fixture-line-two" >>"$fixture_dir/source.txt"
: >"$fixture_dir/truncated.txt"
chmod 0600 "$fixture_dir/source.txt"
mv "$fixture_dir/source.txt" "$fixture_dir/renamed.txt"
hardlink_created=0
if ln "$fixture_dir/renamed.txt" "$fixture_dir/hardlink.txt" 2>/dev/null; then
  hardlink_created=1
fi
ln -s "renamed.txt" "$fixture_dir/symlink.txt"
if [ "$hardlink_created" -eq 1 ]; then
  rm "$fixture_dir/hardlink.txt"
fi
rm "$fixture_dir/symlink.txt"
rmdir "$fixture_dir/nested"

sh -c 'printf "%s\n" child >"$1/child.txt"; sh -c "true"' sh "$fixture_dir"

sh -c '(sleep 0.05; printf "%s\n" reparented >"$1/reparented.txt") &' sh "$fixture_dir"
attempt=0
while [ ! -f "$fixture_dir/reparented.txt" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 200 ]; then
    echo "reparented fixture child did not complete" >&2
    exit 1
  fi
  sleep 0.01
done

child=0
while [ "$child" -lt 64 ]; do
  sh -c 'true'
  child=$((child + 1))
done

# The observation fixture may deliberately use a name that resolves to
# NXDOMAIN. The DNS response itself is the evidence; name resolution success
# is not required.
getent ahosts "$dns_name" >/dev/null || true
curl --fail --silent --show-error "$http_url" >/dev/null
if [ -n "$socks_url" ]; then
  curl --fail --silent --show-error --proxy "$socks_url" "$http_url" >/dev/null
fi

rm "$fixture_dir/child.txt" "$fixture_dir/reparented.txt"
