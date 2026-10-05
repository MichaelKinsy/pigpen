#!/usr/bin/env python3
"""Refresh release badges using the best recorded Pi extension download rank."""
import argparse
import datetime as dt
from html import escape
from html.parser import HTMLParser
import json
from pathlib import Path
import re
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parent.parent
SOURCE = 'https://pi.dev/packages?type=extension'


class PackageParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.packages = []

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == 'article' and 'data-package-name' in values:
            self.packages.append((values['data-package-name'], int(values['data-package-downloads'])))


def fetch(url):
    with urlopen(Request(url, headers={'User-Agent': 'pi-goal-x release ranking'}), timeout=30) as response:
        return response.read().decode('utf-8')


def collect(fetch_page=fetch):
    seen = set()
    total = None
    previous_downloads = float('inf')
    page = 1
    while True:
        html = fetch_page(SOURCE if page == 1 else f'{SOURCE}&page={page}')
        match = re.search(r'class="packages-count">\s*([\d,]+)-([\d,]+)\s*/\s*([\d,]+)', html)
        if not match:
            raise ValueError('Missing catalog count')
        start, end, count = (int(value.replace(',', '')) for value in match.groups())
        if total is None:
            total = count
        parser = PackageParser()
        parser.feed(html)
        if count != total or start != len(seen) + 1 or end > total or end - start + 1 != len(parser.packages) or not parser.packages:
            raise ValueError('Catalog pagination or count changed')
        found = None
        for name, downloads in parser.packages:
            if name in seen or downloads < 0 or downloads > previous_downloads:
                raise ValueError('Catalog repeats packages or is not sorted by downloads')
            seen.add(name)
            previous_downloads = downloads
            if name == 'pi-goal-x':
                found = {'rank': len(seen), 'extensions': total, 'downloads': downloads}
        if found:
            return {'date': dt.datetime.now(dt.timezone.utc).date().isoformat(), 'source': SOURCE, **found}
        if len(seen) >= total:
            raise ValueError('pi-goal-x not found in extension catalog')
        page += 1


def replace_once(pattern, replacement, text):
    result, count = re.subn(pattern, lambda _: replacement, text)
    if count != 1:
        raise ValueError(f'Expected one badge field: {pattern}')
    return result


def update(root, release, fetch_page=fetch):
    data_path = root / 'assets/ranking.json'
    data = json.loads(data_path.read_text()) if data_path.exists() else {'package': 'pi-goal-x', 'source': SOURCE, 'observations': []}
    observations = data['observations']
    if not any(item['release'] == release for item in observations):
        observations.append({'release': release, **collect(fetch_page)})
    release_index = next(i for i, item in enumerate(observations) if item['release'] == release)
    peak = min(observations[:release_index + 1], key=lambda item: (item['rank'], -dt.date.fromisoformat(item['date']).toordinal()))
    date = dt.date.fromisoformat(peak['date']).strftime('%b %d, %Y').replace(' 0', ' ')
    # Round upward so the badge never overstates its percentile.
    percent_tenths = (peak['rank'] * 1000 + peak['extensions'] - 1) // peak['extensions']
    top = f'TOP {percent_tenths / 10:.1f}%'
    meta = f"#{peak['rank']:,} of {peak['extensions']:,} by downloads · {date}"
    label = f'{top} of Pi coding agent extensions: {meta} (best recorded rank)'
    outputs = {}
    for theme in ('light', 'dark'):
        path = root / f'assets/badge-{theme}.svg'
        svg = path.read_text()
        svg = replace_once(r'aria-label="[^"]*"', f'aria-label="{escape(label, quote=True)}"', svg)
        for field, content in [('rank', top), ('label', 'of Pi coding agent extensions'), ('meta', meta)]:
            svg = re.sub(rf'(<text class="{field}"[^>]*>).*?(</text>)', lambda m: m[1] + escape(content) + m[2], svg)
            if len(re.findall(rf'<text class="{field}"', svg)) != 1:
                raise ValueError(f'Missing or duplicate {field} field')
        svg = svg.replace('https://pi.dev/packages"', SOURCE + '"')
        # Reserve enough room for a three-digit percentile and metadata.
        svg = svg.replace('width="420"', 'width="480"').replace('0 0 420 54', '0 0 480 54').replace('width="419"', 'width="479"')
        svg = svg.replace('x1="88"', 'x1="128"').replace('x2="88"', 'x2="128"').replace('x="103"', 'x="143"')
        outputs[path] = svg
    readme = root / 'README.md'
    text = replace_once(r'<img src="assets/badge-light.svg"[^>]*>', f'<img src="assets/badge-light.svg" alt="{escape(label, quote=True)}" width="480">', readme.read_text())
    outputs[readme] = text.replace('https://pi.dev/packages"', SOURCE + '"')
    outputs[data_path] = json.dumps(data, indent=2) + '\n'
    for path, content in outputs.items():
        path.write_text(content)
    return peak


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release', required=True)
    parser.add_argument('--record-release', action='store_true', help='leave main unchanged when this release is already recorded')
    args = parser.parse_args()
    if not re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', args.release):
        parser.error('Expected a stable vX.Y.Z release tag')
    data_path = ROOT / 'assets/ranking.json'
    already_recorded = args.record_release and data_path.exists() and any(
        item['release'] == args.release for item in json.loads(data_path.read_text())['observations']
    )
    if already_recorded:
        print('Release ranking already recorded; preserving current badges.')
    else:
        print(json.dumps(update(ROOT, args.release)))
