#!/usr/bin/env python3
"""Read-only source metrics and identical per-file merge rehearsals; no checkout changes."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile

BASELINE = '0161258af6ca167f5885c145b454d3a982839939'
UPSTREAM = '10ba8f9b4dc2e6023d4a9879ab77b21841babd10'
TARGET = 'b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b'
PROTECTED_EGRESS = (
    'backend/internal/service/account_egress.go',
    'backend/internal/service/account_egress_context.go',
    'backend/internal/service/openai_plugin_transport.go',
    'backend/internal/service/openai_ws_pool.go',
    'backend/internal/service/openai_ws_v2_passthrough_adapter.go',
    'backend/internal/service/channel_monitor_access.go',
    'backend/internal/service/account_monitor_usage.go',
    'backend/internal/service/proxy_expiry_service.go',
    'backend/internal/repository/http_upstream.go',
    'backend/internal/repository/proxy_repo.go',
    'backend/internal/repository/claude_oauth_service.go',
)


def git(*args):
    return subprocess.check_output(['git', *args])


def names(base, ref):
    return set(git('diff', '--name-only', '-z', base, ref).decode().rstrip('\0').split('\0')) - {''}


def production(path):
    # Report schemas separately from generated Ent/Wire; retain all app source.
    return (path.startswith(('backend/', 'frontend/src/'))
            and not path.endswith(('_test.go', '.spec.ts', '.test.ts', '/wire_gen.go'))
            and '/__tests__/' not in path
            and (not path.startswith('backend/ent/') or path.startswith('backend/ent/schema/'))
            and path.endswith(('.go', '.vue', '.ts', '.js')))


def metrics(ref):
    upstream_paths = set(git('ls-tree', '-r', '--name-only', UPSTREAM).decode().splitlines())
    rows = []
    for line in git('diff', '--no-renames', '--numstat', UPSTREAM, ref).decode().splitlines():
        added, deleted, path = line.split('\t', 2)
        if production(path):
            rows.append({'path': path, 'added': int(added), 'deleted': int(deleted),
                         'existing_upstream_file': path in upstream_paths})
    shared = [row for row in rows if row['existing_upstream_file']]
    return {'production_files': len(rows), 'shared_files': len(shared),
            'shared_added': sum(row['added'] for row in shared),
            'shared_deleted': sum(row['deleted'] for row in shared), 'files': rows}


def blob(ref, path):
    # A missing file is the empty side of a deletion/addition in the rehearsal.
    result = subprocess.run(['git', 'cat-file', '-e', f'{ref}:{path}'], capture_output=True)
    return git('show', f'{ref}:{path}') if result.returncode == 0 else b''


HOOK_PATTERN = re.compile(
    rb'\b(?:ResolveAccountProxyURL|ResolveAccountProxyFromRepository|ResolveProxyBinding|'
    rb'ProxyRouteForTarget|WithAccountEgress|DetachedAccountEgress|RecordAccountEgress|'
    rb'resolveEgressForRequest|resolveEgressSnapshot|doOpenAIUpstream)\('
    rb'|monitorrequest\.CheckRequest\(|httpUpstream\.Do(?:WithTLS)?\('
)


def hooks(content):
    return sorted(line.strip().decode() for line in content.splitlines()
                  if HOOK_PATTERN.search(line) and not line.lstrip().startswith(b'func '))


def rehearse(ref):
    merge_base = git('merge-base', ref, TARGET).decode().strip()
    common = sorted(names(merge_base, ref) & names(merge_base, TARGET))
    results = []
    with tempfile.TemporaryDirectory(prefix='sub2api-merge-rehearsal-') as directory:
        paths = [Path(directory) / name for name in ('candidate', 'base', 'target')]
        for path in common:
            values = [blob(ref, path), blob(merge_base, path), blob(TARGET, path)]
            if any(b'\0' in value for value in values):
                results.append({'path': path, 'result': 'binary_requires_review'})
                continue
            for output, value in zip(paths, values):
                output.write_bytes(value)
            result = subprocess.run(['git', 'merge-file', '-p', *map(str, paths)],
                                    capture_output=True)
            if result.returncode < 0 or result.returncode > 127:
                raise RuntimeError(f'merge-file failed for {path}: {result.stderr.decode()}')
            results.append({'path': path,
                            'result': 'conflict' if result.returncode else 'text_auto_merge',
                            'conflict_blocks': result.stdout.count(b'<<<<<<< '),
                            'candidate_egress_hooks': hooks(values[0]),
                            'merged_egress_hooks': hooks(result.stdout) if not result.returncode else None,
                            'egress_hooks_unchanged': hooks(values[0]) == hooks(result.stdout) if not result.returncode else None})
    return {'candidate': ref, 'target': TARGET, 'merge_base': merge_base,
            'method': 'git merge-file -p candidate base target; default diff3 algorithm',
            'common_paths': len(common),
            'conflict_files': sum(row['result'] == 'conflict' for row in results),
            'files': results}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', default='HEAD')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    candidate = git('rev-parse', args.candidate).decode().strip()
    for ref in (BASELINE, candidate):
        subprocess.run(['git', 'merge-base', '--is-ancestor', UPSTREAM, ref], check=True)
        if subprocess.run(['git', 'merge-base', '--is-ancestor', TARGET, ref]).returncode == 0:
            raise SystemExit('The v0.2.22 target must not be merged into either candidate')
    subprocess.run(['git', 'merge-base', '--is-ancestor', BASELINE, candidate], check=True)
    report = {'source_sha': candidate, 'git_version': git('--version').decode().strip(),
              'upstream': UPSTREAM, 'baseline': BASELINE, 'target': TARGET,
              'protected_egress_changed': sorted(names(BASELINE, candidate) & set(PROTECTED_EGRESS)),
              'metrics_scope': 'Go/Vue/TS/JS app source, excluding tests, generated Ent/Wire and ops',
              'baseline_metrics': metrics(BASELINE), 'candidate_metrics': metrics(candidate),
              'baseline_rehearsal': rehearse(BASELINE), 'candidate_rehearsal': rehearse(candidate),
              'warning': 'Text auto-merge is not semantic validation or v0.2.22 upgrade approval'}
    if report['protected_egress_changed']:
        raise SystemExit('Protected egress implementation changed: ' + ', '.join(report['protected_egress_changed']))
    Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({key: {field: report[key][field] for field in ('common_paths', 'conflict_files')}
                      for key in ('baseline_rehearsal', 'candidate_rehearsal')}, indent=2))


if __name__ == '__main__':
    main()
