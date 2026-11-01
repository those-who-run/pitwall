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
# - serves the web app's index.html on /, on a session's page and on v1's results
#   card link, /?session=<id>, which the app turns into its session's page;
# - answers the API: GetEngineStatus over Connect's GET, offline with no NATS.
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

# The page on /, on a client route, and on v1's card link (ADR 0009 and 0011:
# those links are permanent), each the app's index.html.
for path in / /sessions/01JB9QZ4V3X8M2K7T5R1N6PDWA '/?session=20261017-162118'; do
  page=$(curl --fail --silent --show-error "http://127.0.0.1:8080$path")
  if ! grep -q '<div id="root">' <<<"$page"; then
    echo "GET $path did not serve the web app's index.html:" >&2
    echo "$page" >&2
    exit 1
  fi
  echo "GET $path served the web app"
done

# The API, as the web app calls it. With no NATS the engineer is offline, and the
# answer is still 200 (paddock#125 R2).
api=$(curl --fail --silent --show-error \
  'http://127.0.0.1:8080/twr.pitwall.v1.PitwallService/GetEngineStatus?connect=v1&encoding=json&message=%7B%7D')
echo "GetEngineStatus: $api"
if ! jq --exit-status '.status.activity == "ACTIVITY_OFFLINE"' <<<"$api" >/dev/null; then
  echo "GetEngineStatus did not answer offline" >&2
  exit 1
fi
