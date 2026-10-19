"""将最新正式 Release 同步到 COS；所有安装包成功后才发布更新清单。"""

import argparse
import base64
import getpass
import json
import hashlib
import logging
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib.parse import unquote, urlsplit

REPOSITORY = "Lixiuxiu559/portunus"
BUCKET = "portunus-1321735248"
REGION = "ap-shanghai"
PREFIX = "releases/"
MANIFESTS = ("latest.yml", "latest-mac.yml", "latest-linux.yml")
ASSET_SUFFIXES = (".exe", ".dmg", ".zip", ".AppImage", ".blockmap")


def check_latest(tag):
    """上传前及发布清单前复查，防止旧版本覆盖最新更新源。"""
    latest = subprocess.check_output(
        ["gh", "api", f"repos/{REPOSITORY}/releases/latest", "--jq", ".tag_name"],
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


def credentials():
    secret_id = os.environ.get("COS_SECRET_ID") or os.environ.get("TENCENTCLOUD_SECRET_ID")
    secret_key = os.environ.get("COS_SECRET_KEY") or os.environ.get("TENCENTCLOUD_SECRET_KEY")
    if not secret_id or not secret_key:
        if not sys.stdin.isatty():
            raise ValueError("本机尚未配置 COS 凭证。请在本地终端运行脚本，按隐藏提示输入密钥；不要发送到聊天或写入仓库。")
        secret_id = secret_id or getpass.getpass("COS SecretId（输入隐藏）：")
        secret_key = secret_key or getpass.getpass("COS SecretKey（输入隐藏）：")
    if not secret_id.strip() or not secret_key.strip():
        raise ValueError("COS 凭证不能为空")
    return secret_id.strip(), secret_key.strip()


def create_cos_client():
    from qcloud_cos import CosConfig, CosS3Client

    secret_id, secret_key = credentials()
    return CosS3Client(CosConfig(
        Region=REGION, SecretId=secret_id, SecretKey=secret_key,
        Scheme="https", Timeout=120,
        EnableOldDomain=False, EnableInternalDomain=False,
    ))


def release_info(tag):
    command = ["gh", "release", "view"]
    if tag:
        command.append(tag)
    command.extend(["--repo", REPOSITORY, "--json", "tagName,isDraft,isPrerelease"])
    release = json.loads(subprocess.check_output(command, text=True))
    if release["isDraft"] or release["isPrerelease"]:
        raise ValueError("只允许同步正式 Release")
    tag = release["tagName"]
    if not tag.startswith("v") or Path(tag).name != tag or "/" in tag or "\\" in tag:
        raise ValueError("版本标签格式不正确")
    check_latest(tag)
    return tag


def download_release(tag, directory):
    directory.mkdir(parents=True, exist_ok=True)
    common = ["gh", "release", "download", tag, "--repo", REPOSITORY, "--dir", str(directory)]
    subprocess.run(common + ["--pattern", "latest*.yml", "--clobber"], check=True)
    # 已下载文件可以复用，后续 SHA-512 校验会阻止损坏安装包上传。
    patterns = [arg for suffix in ASSET_SUFFIXES for arg in ["--pattern", "*" + suffix]]
    subprocess.run(common + patterns + ["--skip-existing"], check=True)


def main(args):
    parser = argparse.ArgumentParser(description="从本机同步 GitHub 最新正式版到 COS")
    parser.add_argument("--tag", help="版本标签，默认使用最新正式版")
    parser.add_argument("--assets-dir", type=Path, help="使用已下载的安装包目录，省略则自动下载")
    parser.add_argument("--download-only", action="store_true", help="只下载并校验，不连接 COS")
    options = parser.parse_args(args)
    logging.basicConfig(level=logging.WARNING, format="%(levelname)s %(name)s: %(message)s")
    tag = release_info(options.tag)
    directory = options.assets_dir or Path(tempfile.gettempdir()) / "portunus-cos-releases" / tag
    if not options.assets_dir:
        print(f"下载正式版 {tag} 到 {directory}", flush=True)
        download_release(tag, directory)
    assets, manifests = prepare_files(directory, tag)
    print(f"校验通过：{len(assets)} 个安装文件，{len(manifests)} 个更新清单", flush=True)
    if options.download_only:
        print(f"安装包目录：{directory}")
        return
    client = create_cos_client()
    sync_files(client, assets, manifests, lambda: check_latest(tag))
    print(f"{tag} 已同步到 COS：{BUCKET}/{PREFIX}")


if __name__ == "__main__":
    main(sys.argv[1:])
