#!/bin/sh

set -eu

if [ "$#" -ne 3 ]; then
	printf 'usage: %s URL DESTINATION REVISION\n' "$0" >&2
	exit 2
fi

url=$1
destination=$2
revision=$3

case "$destination" in
	internal/pdf/testdata/external/*) ;;
	*)
		printf 'refusing corpus destination outside internal/pdf/testdata/external: %s\n' "$destination" >&2
		exit 2
		;;
esac
case "$destination" in
	*/../*|*/..|../*|*/./*|*/.)
		printf 'refusing non-canonical corpus destination: %s\n' "$destination" >&2
		exit 2
		;;
esac
case "$revision" in
	''|*[!0-9a-fA-F]*)
		printf 'revision must be a hexadecimal object id: %s\n' "$revision" >&2
		exit 2
		;;
esac
if [ "${#revision}" -ne 40 ] && [ "${#revision}" -ne 64 ]; then
	printf 'revision must be a full object id: %s\n' "$revision" >&2
	exit 2
fi

destination_parent_path=$(dirname "$destination")
path_component=$destination_parent_path
while [ "$path_component" != . ]; do
	if [ -L "$path_component" ]; then
		printf 'refusing corpus destination through symlink: %s\n' "$path_component" >&2
		exit 2
	fi
	path_component=$(dirname "$path_component")
done
mkdir -p "$destination_parent_path"

external_root=$(cd internal/pdf/testdata/external && pwd -P)
destination_parent=$(cd "$destination_parent_path" && pwd -P)
case "$destination_parent" in
	"$external_root"|"$external_root"/*) ;;
	*)
		printf 'refusing corpus destination through a path outside %s: %s\n' "$external_root" "$destination" >&2
		exit 2
		;;
esac
if [ -L "$destination" ]; then
	printf 'refusing symlink corpus destination: %s\n' "$destination" >&2
	exit 2
fi

if [ -e "$destination" ] && [ ! -d "$destination/.git" ]; then
	printf 'corpus destination exists but is not a Git checkout: %s\n' "$destination" >&2
	exit 1
fi

new_checkout=false
if [ ! -d "$destination/.git" ]; then
	case "$url" in
		https://huggingface.co/*) git clone --no-checkout "$url" "$destination" ;;
		*) git clone --filter=blob:none --no-checkout "$url" "$destination" ;;
	esac
	new_checkout=true
fi

actual_url=$(git -C "$destination" remote get-url origin)
if [ "$actual_url" != "$url" ]; then
	printf 'unexpected origin for %s: %s\n' "$destination" "$actual_url" >&2
	exit 1
fi

if [ "$new_checkout" = false ] && [ -n "$(git -C "$destination" status --porcelain)" ]; then
	printf 'refusing to update dirty corpus checkout: %s\n' "$destination" >&2
	exit 1
fi

case "$url" in
	https://huggingface.co/*)
		git -C "$destination" config --unset-all remote.origin.promisor 2>/dev/null || true
		git -C "$destination" config --unset-all remote.origin.partialclonefilter 2>/dev/null || true
		git -C "$destination" fetch --refetch --depth 1 origin "$revision"
		;;
	*) git -C "$destination" fetch --depth 1 origin "$revision" ;;
esac
# `clone --no-checkout` still points HEAD at the remote default branch. If the
# requested revision is that same commit, a plain checkout is a no-op and can
# leave both the index and worktree empty. Force materialization only when the
# destination has no worktree entries; existing checkouts keep the conservative
# non-forced path above.
if [ "$new_checkout" = true ]; then
	git -C "$destination" checkout --detach --force "$revision"
else
	git -C "$destination" checkout --detach "$revision"
fi

if command -v git-lfs >/dev/null 2>&1; then
	git -C "$destination" lfs pull
elif git -C "$destination" grep -Iq 'filter=lfs' HEAD -- .gitattributes 2>/dev/null; then
	printf 'git-lfs is required for corpus %s\n' "$destination" >&2
	exit 1
fi

printf '%s %s\n' "$destination" "$(git -C "$destination" rev-parse HEAD)"
