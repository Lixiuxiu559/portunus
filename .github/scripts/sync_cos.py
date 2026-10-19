"""将最新正式 Release 同步到 COS；所有安装包成功后才发布更新清单。"""

import base64
import hashlib
import logging
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import unquote, urlsplit

BUCKET = "portunus-1321735248"
REGION = "ap-shanghai"
PREFIX = "releases/"
MANIFESTS = ("latest.yml", "latest-mac.yml", "latest-linux.yml")
ASSET_SUFFIXES = (".exe", ".dmg", ".zip", ".AppImage", ".blockmap")


def check_latest(tag):
    """上传前及发布清单前复查，防止旧版本覆盖最新更新源。"""
    latest = subprocess.check_output(
        ["gh", "api", f"repos/{os.environ['GITHUB_REPOSITORY']}/releases/latest", "--jq", ".tag_name"],
        text=True,
    ).strip()
    if tag != latest:
        raise ValueError(f"{tag} 不是 GitHub 最新正式版（{latest}），停止同步")


def prepare_files(directory, tag):
    import yaml

    directory = Path(directory)
    assets = sorted(
        (p for p in directory.iterdir() if p.name.endswith(ASSET_SUFFIXES) and p.is_file()),
        key=lambda p: (p.stat().st_size, p.name),
    )
    manifests = [directory / name for name in MANIFESTS]
    version = tag.removeprefix("v")
    for path in manifests:
        data = yaml.safe_load(path.read_text(encoding="utf-8"))
        if not isinstance(data, dict) or data.get("version") != version:
            raise ValueError(f"{path.name} 版本与 {tag} 不一致")
        files = data.get("files")
        if not isinstance(files, list) or not files:
            raise ValueError(f"{path.name} 缺少 files 列表")
        for entry in files:
            url = urlsplit(entry["url"])
            name = unquote(url.path)
            # 清单与安装包共用一个目录，不接受外部 URL 或跨目录路径。
            if url.scheme or url.netloc or url.query or url.fragment or name != Path(name).name:
                raise ValueError(f"{path.name} 包含非同目录文件地址")
            asset = directory / name
            if asset not in assets:
                raise ValueError(f"{path.name} 引用的安装包不存在：{name}")
            with asset.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha512").digest()
            if base64.b64encode(digest).decode() != entry["sha512"]:
                raise ValueError(f"{name} SHA-512 校验失败")
            if "size" in entry and asset.stat().st_size != entry["size"]:
                raise ValueError(f"{name} 大小与更新清单不一致")
        # electron-updater 旧版元数据也携带 path，必须和 files 指向同一安装包。
        if data.get("path") and data["path"] not in [entry["url"] for entry in files]:
            raise ValueError(f"{path.name} 的 path 与 files 不一致")
    return assets, manifests


def upload_progress(name):
    last_percent = -10

    def report(consumed, total):
        nonlocal last_percent
        percent = min(100, int(consumed * 100 / max(total, 1))) // 10 * 10
        if percent > last_percent:
            last_percent = percent
            print(f"上传进度：{name} {percent}%", flush=True)

    return report


def sync_files(client, assets, manifests, before_manifests):
    for path in assets:
        print(f"上传安装包：{path.name}", flush=True)
        client.upload_file(
            Bucket=BUCKET, Key=PREFIX + path.name, LocalFilePath=str(path),
            PartSize=2, MAXThread=4, EnableMD5=True, StorageClass="STANDARD",
            progress_callback=upload_progress(path.name),
        )
    before_manifests()
    for path in manifests:
        print(f"发布更新清单：{path.name}", flush=True)
        client.upload_file(
            Bucket=BUCKET, Key=PREFIX + path.name, LocalFilePath=str(path),
            EnableMD5=True, StorageClass="STANDARD", CacheControl="no-cache",
            ContentType="application/yaml",
        )


def main(args):
    if len(args) == 2 and args[0] == "--check-latest":
        check_latest(args[1])
        return
    if len(args) != 2:
        raise ValueError("用法：sync_cos.py <安装包目录> <版本标签>")
    directory, tag = args
    check_latest(tag)
    assets, manifests = prepare_files(directory, tag)
    from qcloud_cos import CosConfig, CosS3Client

    logging.basicConfig(level=logging.WARNING, format="%(levelname)s %(name)s: %(message)s")
    client = CosS3Client(CosConfig(
        Region=REGION, SecretId=os.environ["COS_SECRET_ID"],
        SecretKey=os.environ["COS_SECRET_KEY"], Scheme="https", Timeout=120,
    ))
    sync_files(client, assets, manifests, lambda: check_latest(tag))
    print(f"{tag} 已同步到 COS：{BUCKET}/{PREFIX}")


if __name__ == "__main__":
    main(sys.argv[1:])
