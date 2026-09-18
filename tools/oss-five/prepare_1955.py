"""Prepare issue 1955 independently of the heredoc fix."""
import argparse
from pathlib import Path
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('phase', choices=('tests', 'fix'))
phase = parser.parse_args().phase
if phase == 'tests':
    for filename in ('bind_named_dollar_test.go', 'issue_1955_test.go'):
        dst = Path(filename if filename.startswith('bind_') else 'tests/issues/' + filename)
        if dst.exists():
            raise RuntimeError('Regression already exists: ' + str(dst))
        shutil.copyfile(Path(__file__).parent / (filename + '.txt'), dst)
        subprocess.run(['gofmt', '-w', str(dst)], check=True)
else:
    path = Path('bind.go')
    actual = subprocess.check_output(['git', 'hash-object', str(path)], text=True).strip()
    if actual != '8523f70618299308e379be361410ee786eeb0edb':
        raise RuntimeError('Unexpected bind.go baseline: ' + actual)
    old = '''// isNameChar reports whether ch is valid in a named placeholder (@name); it
// mirrors the previous bindNamedRe pattern `@[a-zA-Z0-9_]+`.
func isNameChar(ch byte) bool {
\treturn ch == '_' ||'''
    new = '''// isNameChar reports whether ch is valid in a named placeholder (@name).
// ClickHouse allows dollar signs in identifier and parameter names.
func isNameChar(ch byte) bool {
\treturn ch == '_' || ch == '$' ||'''
    text = path.read_text()
    if text.count(old) != 1:
        raise RuntimeError('Nonunique replacement anchor')
    path.write_text(text.replace(old, new, 1))
    subprocess.run(['gofmt', '-w', str(path)], check=True)
