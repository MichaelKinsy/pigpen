import importlib.util
from pathlib import Path
import tempfile
import unittest
import shutil
import json
import xml.etree.ElementTree as ET

spec = importlib.util.spec_from_file_location('ranking', Path(__file__).with_name('update-ranking.py'))
ranking = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ranking)


def page(start, total, packages):
    cards = ''.join(f'<article data-package-name="{name}" data-package-downloads="{downloads}">' for name, downloads in packages)
    return f'<span class="packages-count">{start}-{start + len(packages) - 1} / {total}</span>{cards}'


class RankingTests(unittest.TestCase):
    def test_pagination(self):
        pages = iter([page(1, 3, [('other', 100), ('another', 90)]), page(3, 3, [('pi-goal-x', 80)])])
        self.assertEqual(ranking.collect(lambda _: next(pages))['rank'], 3)

    def test_invalid_catalogs(self):
        for html in ['', page(1, 2, []), page(1, 2, [('pi-goal-x', 1), ('other', 2)]), page(1, 2, [('other', 2), ('other', 1)]), page(1, 1, [('other', 1)])]:
            with self.subTest(html=html), self.assertRaises(ValueError):
                ranking.collect(lambda _: html)

    def test_changed_count(self):
        pages = iter([page(1, 2, [('other', 100)]), page(2, 3, [('pi-goal-x', 80)])])
        with self.assertRaises(ValueError):
            ranking.collect(lambda _: next(pages))

    def test_render_and_retry(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copytree(ranking.ROOT / 'assets', root / 'assets', ignore=shutil.ignore_patterns('ranking.json'))
            shutil.copy(ranking.ROOT / 'README.md', root / 'README.md')
            first = ranking.update(root, 'v1.0.0', lambda _: page(1, 3209, [('pi-goal-x', 80)]))
            self.assertEqual(first['rank'], 1)
            snapshots = {p: p.read_bytes() for p in root.rglob('*') if p.is_file()}
            ranking.update(root, 'v1.0.0', lambda _: self.fail('Retry must not fetch'))
            self.assertEqual(snapshots, {p: p.read_bytes() for p in snapshots})
            for theme in ('light', 'dark'):
                svg = ET.parse(root / f'assets/badge-{theme}.svg').getroot()
                self.assertIn('TOP 0.1%', svg.attrib['aria-label'])
                self.assertIn(svg.attrib['aria-label'], (root / 'README.md').read_text())
            ranking.update(root, 'v1.0.1', lambda _: page(1, 3209, [('other', 100), ('pi-goal-x', 80)]))
            data = json.loads((root / 'assets/ranking.json').read_text())
            self.assertEqual(len(data['observations']), 2)
            self.assertEqual(ranking.update(root, 'v1.0.0')['rank'], 1)

    def test_missing_badge_does_not_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copytree(ranking.ROOT / 'assets', root / 'assets', ignore=shutil.ignore_patterns('ranking.json'))
            (root / 'README.md').write_text('No badge')
            original = (root / 'assets/badge-light.svg').read_bytes()
            with self.assertRaises(ValueError):
                ranking.update(root, 'v1.0.0', lambda _: page(1, 1, [('pi-goal-x', 1)]))
            self.assertEqual(original, (root / 'assets/badge-light.svg').read_bytes())
            self.assertFalse((root / 'assets/ranking.json').exists())


if __name__ == '__main__':
    unittest.main()
