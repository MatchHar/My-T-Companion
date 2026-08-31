#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

mkdir -p "$test_dir/bin" "$test_dir/gh-state"
cat > "$test_dir/bin/gh" <<'FAKE_GH'
#!/usr/bin/env bash
set -euo pipefail
{
  printf 'CMD'
  printf ' %q' "$@"
  printf '\n'
} >> "${TEST_GH_LOG:?}"
state_dir="${TEST_GH_STATE_DIR:?}"
state_file="$state_dir/state"
assets_file="$state_dir/assets"
state="$(cat "$state_file" 2>/dev/null || printf absent)"
if [[ "${1:-} ${2:-}" == "release view" ]]; then
  if [[ "${TEST_GH_MODE:-new}" == "mismatch" ]]; then
    if [[ " $* " == *" --json isDraft,isImmutable "* ]]; then
      printf 'false\tfalse\n'
    elif [[ " $* " == *" --json assets "* ]]; then
      printf 'my-t-companion-9.9.9.tar.gz\tsha256:deadbeef\n'
    fi
    exit 0
  fi
  if [[ "${TEST_GH_MODE:-new}" == "mutable" ]]; then
    if [[ " $* " == *" --json isDraft,isImmutable "* ]]; then
      printf 'false\tfalse\n'
    elif [[ " $* " == *" --json assets "* ]] && [[ -f "$assets_file" ]]; then
      cat "$assets_file"
    fi
    exit 0
  fi
  if [[ "$state" == "absent" ]]; then
    exit 1
  fi
  if [[ " $* " == *" --json isDraft,isImmutable "* ]]; then
    if [[ "$state" == "draft" ]]; then
      printf 'true\tfalse\n'
    else
      printf 'false\ttrue\n'
    fi
  elif [[ " $* " == *" --json assets "* ]] && [[ -f "$assets_file" ]]; then
    cat "$assets_file"
  fi
  exit 0
fi
if [[ "${1:-} ${2:-}" == "release create" ]]; then
  printf 'draft\n' > "$state_file"
  exit 0
fi
if [[ "${1:-} ${2:-}" == "release upload" ]]; then
  shift 3
  : > "$assets_file"
  for asset in "$@"; do
    if command -v sha256sum >/dev/null 2>&1; then
      digest="$(sha256sum "$asset" | awk '{print $1}')"
    else
      digest="$(shasum -a 256 "$asset" | awk '{print $1}')"
    fi
    printf '%s\tsha256:%s\n' "$(basename "$asset")" "$digest" >> "$assets_file"
  done
  exit 0
fi
if [[ "${1:-} ${2:-}" == "release edit" ]] && [[ " $* " == *" --draft=false "* ]]; then
  printf 'published\n' > "$state_file"
  exit 0
fi
exit 0
FAKE_GH
chmod +x "$test_dir/bin/gh"

case_dir="$test_dir/case"
mkdir -p "$case_dir/dist"
printf '9.9.9 release\n' > "$case_dir/dist/my-t-companion-9.9.9.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$case_dir/dist" && sha256sum my-t-companion-9.9.9.tar.gz > my-t-companion-9.9.9.tar.gz.sha256)
else
  (cd "$case_dir/dist" && shasum -a 256 my-t-companion-9.9.9.tar.gz > my-t-companion-9.9.9.tar.gz.sha256)
fi
printf 'historical release\n' > "$case_dir/dist/my-t-companion-9.9.8.tar.gz"
printf '# Test release\n' > "$case_dir/RELEASE_NOTES_9.9.9.md"
printf '# 测试版本\n' > "$case_dir/RELEASE_NOTES_9.9.9.zh-Hans.md"

new_log="$test_dir/new.log"
printf 'absent\n' > "$test_dir/gh-state/state"
(
  cd "$case_dir"
  PATH="$test_dir/bin:$PATH" TEST_GH_LOG="$new_log" TEST_GH_MODE=new \
    TEST_GH_STATE_DIR="$test_dir/gh-state" \
    "$repo_dir/scripts/publish-release.sh" v9.9.9
)
grep -F 'release create v9.9.9 --draft' "$new_log" >/dev/null
grep -F 'dist/my-t-companion-9.9.9.tar.gz' "$new_log" >/dev/null
grep -F 'dist/my-t-companion-9.9.9.tar.gz.sha256' "$new_log" >/dev/null
grep -F 'RELEASE_NOTES_9.9.9.zh-Hans.md' "$new_log" >/dev/null
grep -F 'release edit v9.9.9' "$new_log" | grep -F -- '--draft=false' >/dev/null
test "$(cat "$test_dir/gh-state/state")" = published
if grep -F 'my-t-companion-9.9.8' "$new_log" >/dev/null; then
  echo 'historical release asset leaked into current release' >&2
  exit 1
fi

published_log="$test_dir/published.log"
(
  cd "$case_dir"
  PATH="$test_dir/bin:$PATH" TEST_GH_LOG="$published_log" TEST_GH_MODE=new \
    TEST_GH_STATE_DIR="$test_dir/gh-state" \
    "$repo_dir/scripts/publish-release.sh" v9.9.9
)
if grep -F 'release create' "$published_log" >/dev/null || \
   grep -F 'release edit' "$published_log" >/dev/null || \
   grep -F 'release upload' "$published_log" >/dev/null; then
  echo 'matching published release was mutated' >&2
  exit 1
fi

mutable_log="$test_dir/mutable.log"
if (
  cd "$case_dir"
  PATH="$test_dir/bin:$PATH" TEST_GH_LOG="$mutable_log" TEST_GH_MODE=mutable \
    TEST_GH_STATE_DIR="$test_dir/gh-state" \
    "$repo_dir/scripts/publish-release.sh" v9.9.9
); then
  echo 'expected a mutable published release to fail' >&2
  exit 1
fi
if grep -F 'release create' "$mutable_log" >/dev/null || \
   grep -F 'release edit' "$mutable_log" >/dev/null || \
   grep -F 'release upload' "$mutable_log" >/dev/null; then
  echo 'mutable published release was modified' >&2
  exit 1
fi

mismatch_log="$test_dir/mismatch.log"
if (
  cd "$case_dir"
  PATH="$test_dir/bin:$PATH" TEST_GH_LOG="$mismatch_log" TEST_GH_MODE=mismatch \
    TEST_GH_STATE_DIR="$test_dir/gh-state" \
    "$repo_dir/scripts/publish-release.sh" v9.9.9
); then
  echo 'expected immutable digest mismatch to fail' >&2
  exit 1
fi
if grep -F 'release create' "$mismatch_log" >/dev/null || \
   grep -F 'release edit' "$mismatch_log" >/dev/null || \
   grep -F 'release upload' "$mismatch_log" >/dev/null; then
  echo 'release was mutated after immutable digest mismatch' >&2
  exit 1
fi

printf 'release publishing tests passed\n'
