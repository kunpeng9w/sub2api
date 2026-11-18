#!/usr/bin/env python3
"""Require actual, successful Go test events, rather than a green filtered job."""
import argparse
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('log')
    parser.add_argument('required', nargs='+', help='package suffix:exact test name')
    args = parser.parse_args()
    passed, skipped = set(), set()
    for line in Path(args.log).read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        package, test = event.get('Package'), event.get('Test')
        if not package or not test:
            continue
        if event.get('Action') == 'pass':
            passed.add((package, test))
        elif event.get('Action') == 'skip':
            skipped.add((package, test))
    missing = []
    for spec in args.required:
        package, test = spec.split(':', 1)
        if not any(p.endswith('/' + package) and t == test for p, t in passed):
            missing.append(spec)
    print(json.dumps({'log': args.log, 'passed_test_events': len(passed),
                      'skipped_tests': sorted(f'{p}:{t}' for p, t in skipped),
                      'required': args.required, 'missing_or_not_passed': missing}, indent=2))
    if missing:
        raise SystemExit('Required tests did not execute successfully: ' + ', '.join(missing))


if __name__ == '__main__':
    main()
