import argparse
from pathlib import Path
import shutil
import subprocess

fixtures = Path(__file__).parent
phase = argparse.ArgumentParser()
phase.add_argument('phase', choices=('tests', 'fix'))
args = phase.parse_args()
if args.phase == 'tests':
    for src, dst in [('bind_heredoc_test.go.txt', 'bind_heredoc_test.go'), ('issue_1951_test.go.txt', 'tests/issues/issue_1951_test.go')]:
        if Path(dst).exists():
            raise RuntimeError('Regression file already exists: ' + dst)
        shutil.copyfile(fixtures / src, dst)
    subprocess.run(['gofmt', '-w', 'bind_heredoc_test.go', 'tests/issues/issue_1951_test.go'], check=True)
else:
    expected = '58a70da9f1425192bc6abf6e6c3075be5355e88d'
    actual = subprocess.check_output(['git', 'hash-object', 'bind.go'], text=True).strip()
    if actual != expected:
        raise RuntimeError('Unexpected bind.go baseline: ' + actual)
    path = Path('bind.go')
    text = path.read_text()
    anchor = 'func isEscaped(query string, pos int) bool {'
    if text.count(anchor) != 1:
        raise RuntimeError('Nonunique helper anchor')
    text = text.replace(anchor, (fixtures / 'heredoc_helper.txt').read_text() + anchor, 1)
    loop = '\tfor i := 0; i < len(query); i++ {\n'
    if text.count(loop) != 4:
        raise RuntimeError('Expected four bind scanner loops')
    text = text.replace(loop, loop + '\t\tif end := state.skipHeredoc(query, i); end > i {\n\t\t\ti = end\n\t\t\tcontinue\n\t\t}\n')
    path.write_text(text)
    subprocess.run(['gofmt', '-w', 'bind.go'], check=True)
