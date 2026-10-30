#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
venv_dir="${TMPDIR:-/tmp}/portunus-cos-sync-venv"

command -v gh >/dev/null || { echo '请先安装 GitHub CLI 并运行 gh auth login'; exit 1; }
python3 -c 'import sys; assert sys.version_info >= (3, 11), "需要 Python 3.11+"'
if [[ ! -x "$venv_dir/bin/python" || ! -f "$venv_dir/pyvenv.cfg" ]]; then
  # --clear 可修复中断残留的目录，避免误用指向系统 Python 的假 venv。
  python3 -m venv --clear "$venv_dir"
fi
"$venv_dir/bin/python" -m pip install --quiet --disable-pip-version-check -r "$script_dir/cos-requirements.txt"
exec "$venv_dir/bin/python" "$script_dir/sync_cos.py" "$@"
