#!/usr/bin/env bash
# Test the exact audit sources with minimal model/interface stubs, without
# compiling the production service/Ent graph. This does not test integration.
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
backend_dir="$repo_dir/backend"
audit_test_dir=$(mktemp -d "$backend_dir/internal/egressaudittest.XXXXXX")
cleanup_audit_test() {
    python3 - "$audit_test_dir" <<'PY_CLEANUP'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
assert p.name.startswith("egressaudittest.") and p.parent.name == "internal"
for name in ("egress_audit.go", "egress_audit_ws.go", "egress_audit_ws_dial.go", "egress_audit_test.go", "egress_audit_network.go", "egress_audit_ws_network.go", "egress_audit_network_test.go", "model_test.go"):
    (p / name).unlink(missing_ok=True)
p.rmdir()
PY_CLEANUP
}
trap cleanup_audit_test EXIT
cp "$backend_dir/internal/service/egress_audit.go" \
   "$backend_dir/internal/service/egress_audit_ws.go" \
   "$backend_dir/internal/service/egress_audit_ws_dial.go" \
   "$backend_dir/internal/service/egress_audit_network.go" \
   "$backend_dir/internal/service/egress_audit_ws_network.go" \
   "$backend_dir/internal/service/egress_audit_network_test.go" \
   "$backend_dir/internal/service/egress_audit_test.go" "$audit_test_dir/"
cat > "$audit_test_dir/model_test.go" <<'GO'
package service
import ("context"; "net/http")
type Proxy struct { ID int64; Name string }
type Account struct { ID int64; Name string; ProxyID *int64; Proxy *Proxy }
type HTTPUpstream interface{}
type openAIWSClientConn interface{}
type openAIWSClientDialer interface { Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) }
type ProxyExitInfo struct { IP string }
type ProxyExitInfoProber interface {
    ProbeProxy(context.Context, string) (*ProxyExitInfo, int64, error)
}
GO
cd "$backend_dir"
GOMEMLIMIT="${GOMEMLIMIT:-220MiB}" GOMAXPROCS="${GOMAXPROCS:-1}" \
    go test -p 1 "$@" "./internal/$(basename -- "$audit_test_dir")" -count=1
