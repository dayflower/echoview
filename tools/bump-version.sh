#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: bash tools/bump-version.sh {major|minor|patch}" >&2
  exit 2
}

if [[ $# -ne 1 ]]; then
  usage
fi

case "$1" in
  major|minor|patch) bump=$1 ;;
  *) usage ;;
esac

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [[ -n $(git status --porcelain) ]]; then
  echo "Working tree is not clean; commit or stash changes before tagging." >&2
  exit 1
fi

if ! branch=$(git symbolic-ref --quiet --short HEAD); then
  echo "Check out a branch before tagging." >&2
  exit 1
fi

if ! remote_branch=$(git ls-remote --heads origin "refs/heads/$branch"); then
  echo "Could not read the origin branch." >&2
  exit 1
fi
if [[ -z $remote_branch || ${remote_branch##*$'\t'} != "refs/heads/$branch" ]]; then
  echo "Push the current branch to origin before tagging." >&2
  exit 1
fi
if [[ ${remote_branch%%$'\t'*} != "$(git rev-parse HEAD)" ]]; then
  echo "The current branch differs from origin/$branch; sync it before tagging." >&2
  exit 1
fi

if ! remote_tags=$(git ls-remote --tags --refs origin 'refs/tags/v*'); then
  echo "Could not read tags from origin." >&2
  exit 1
fi

major=0
minor=0
patch=0
latest=
while IFS=$'\t' read -r _ ref; do
  tag=${ref#refs/tags/}
  if [[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    tag_major=${BASH_REMATCH[1]}
    tag_minor=${BASH_REMATCH[2]}
    tag_patch=${BASH_REMATCH[3]}
    if [[ -z $latest ]] ||
       (( tag_major > major ||
          (tag_major == major && tag_minor > minor) ||
          (tag_major == major && tag_minor == minor && tag_patch > patch) )); then
      latest=$tag
      major=$tag_major
      minor=$tag_minor
      patch=$tag_patch
    fi
  fi
done <<< "$remote_tags"

case "$bump" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac

next_tag="v$major.$minor.$patch"
if git rev-parse --quiet --verify "refs/tags/$next_tag" >/dev/null; then
  echo "Local tag $next_tag already exists; resolve it before retrying." >&2
  exit 1
fi

git tag "$next_tag"
if ! git push origin "refs/tags/$next_tag"; then
  echo "Push failed. Local tag $next_tag remains; inspect it before retrying." >&2
  exit 1
fi

echo "Pushed $next_tag to origin (previous release: ${latest:-none})."
