#!/usr/bin/env bash
# The image's smoke test (those-who-run/paddock#131), run by image.yml in
# those-who-run/.github against the image it built, named in $IMAGE: on every PR
# from .github/workflows/image.yml, and on a release tag from release.yml, before
# the build is published. It runs the image as it would be deployed and checks
# that it:
# - reports the version it was given, $1 (`dev` on a PR, X.Y.Z on tag vX.Y.Z);
# - runs as distroless's non-root user;
# - is under 40 MB;
# - holds no Node and no Python;
# - answers GET /healthz 200 within 10 s, ok and with that version;
# - serves the web app's index.html on /.
set -euo pipefail

want=${1:?usage: image_smoke.sh <version>}
: "${IMAGE:?IMAGE names the image to test}"

version=$(docker run --rm "$IMAGE" -version)
if [ "$version" != "$want" ]; then
  echo "the image reports version '$version', not '$want'" >&2
  exit 1
fi

user=$(docker image inspect --format '{{.Config.User}}' "$IMAGE")
if [ "$user" != 65532:65532 ]; then
  echo "the image runs as '$user', not distroless's nonroot user" >&2
  exit 1
fi

size=$(docker image inspect --format '{{.Size}}' "$IMAGE")
echo "the image is $size bytes"
if [ "$size" -ge 40000000 ]; then
  echo "the image is over 40 MB" >&2
  exit 1
fi

cid=$(docker create "$IMAGE")
files=$(docker export "$cid" | tar -t)
docker rm "$cid" >/dev/null
if grep -E '(^|/)(node|nodejs|npm|python[0-9.]*)$' <<<"$files"; then
  echo "the image carries Node or Python" >&2
  exit 1
fi

id=$(docker run --detach --publish 127.0.0.1:8080:8080 "$IMAGE")
trap 'docker logs "$id"; docker rm --force "$id" >/dev/null' EXIT
health=""
for _ in $(seq 20); do
  if health=$(curl --fail --silent --show-error http://127.0.0.1:8080/healthz); then break; fi
  health=""
  sleep 0.5
done
if [ -z "$health" ]; then
  echo "GET /healthz did not answer 200 within 10 s" >&2
  exit 1
fi
echo "GET /healthz: $health"
if ! jq --exit-status --arg want "$want" '.ok == true and .version == $want' <<<"$health" >/dev/null; then
  echo "GET /healthz is not ok with version '$want'" >&2
  exit 1
fi

page=$(curl --fail --silent --show-error http://127.0.0.1:8080/)
if ! grep -q '<div id="root">' <<<"$page"; then
  echo "GET / did not serve the web app's index.html:" >&2
  echo "$page" >&2
  exit 1
fi
echo "GET / served the web app"
