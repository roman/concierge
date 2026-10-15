set -euo pipefail

test "$(go list -m)" = github.com/roman/concierge
transport_version="$(go list -m -f '{{.Version}}' github.com/roman/tincan)"
case "$transport_version" in
  *-$transportRevision) ;;
  *)
    printf 'Transport Go version %s does not match runtime revision %s\n' "$transport_version" "$transportRevision" >&2
    exit 1
    ;;
esac
go list -deps ./cmd/concierge > concierge-dependencies
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/roman/tincan/pkg/inbox|github.com/roman/tincan/pkg/notify|github.com/roman/tincan/pkg/herdr) ;;
    github.com/roman/tincan/*)
      printf 'Forbidden transport dependency: %s\n' "$dependency" >&2
      exit 1
      ;;
  esac
done < concierge-dependencies
test_flags=()
if [ "$darwin" = 1 ]; then
  test_flags+=(-ldflags "-X=github.com/roman/concierge/pkg/presentation.presentationNamespace=$TMPDIR/presentation-locks")
fi
go test "${test_flags[@]}" ./...
