import base64
import hashlib
import io
from contextlib import redirect_stdout
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import yaml

from sync_cos import BUCKET, MANIFESTS, check_latest, prepare_files, sync_files, upload_progress, create_cos_client, credentials, release_info, download_release


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.asset = self.root / "Portunus-0.5.0-mac-arm64.zip"
        self.asset.write_bytes(b"installer test payload")
        self.digest = base64.b64encode(hashlib.sha512(self.asset.read_bytes()).digest()).decode()
        for name in MANIFESTS:
            self.write_manifest(name)
        (self.root / (self.asset.name + ".blockmap")).write_text("blockmap")

    def write_manifest(self, name, **overrides):
        data = {
            "version": "0.5.0", "path": self.asset.name,
            "files": [{"url": self.asset.name, "sha512": self.digest, "size": self.asset.stat().st_size}],
        }
        data.update(overrides)
        (self.root / name).write_text(yaml.safe_dump(data))

    def test_installers_and_blockmaps_precede_all_manifests(self):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        client = Mock()
        guard = Mock(side_effect=lambda: self.assertEqual(client.upload_file.call_count, len(assets)))
        sync_files(client, assets, manifests, guard)
        guard.assert_called_once()
        calls = client.upload_file.call_args_list
        self.assertEqual([call.kwargs["Key"] for call in calls], ["releases/" + p.name for p in assets + manifests])
        for call in calls:
            self.assertEqual(call.kwargs["Bucket"], BUCKET)
        for call in calls[len(assets):]:
            self.assertEqual(call.kwargs["CacheControl"], "no-cache")

    def test_upload_failure_preserves_existing_manifests(self):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        client = Mock()
        client.upload_file.side_effect = RuntimeError("upload failed")
        guard = Mock()
        with self.assertRaisesRegex(RuntimeError, "upload failed"):
            sync_files(client, assets, manifests, guard)
        guard.assert_not_called()
        self.assertFalse(any(call.kwargs["Key"].endswith(".yml") for call in client.upload_file.call_args_list))

    def test_newer_release_during_upload_prevents_manifest_publish(self):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        client = Mock()
        with self.assertRaisesRegex(ValueError, "newer release"):
            sync_files(client, assets, manifests, Mock(side_effect=ValueError("newer release")))
        self.assertEqual(client.upload_file.call_count, len(assets))

    def test_incomplete_platform_manifests_fail_before_upload(self):
        (self.root / MANIFESTS[-1]).unlink()
        with self.assertRaises(FileNotFoundError):
            prepare_files(self.root, "v0.5.0")

    def test_wrong_version_is_rejected(self):
        self.write_manifest(MANIFESTS[0], version="0.4.0")
        with self.assertRaisesRegex(ValueError, "版本"):
            prepare_files(self.root, "v0.5.0")

    def test_corrupted_package_is_rejected(self):
        self.asset.write_bytes(b"corrupted")
        with self.assertRaisesRegex(ValueError, "SHA-512"):
            prepare_files(self.root, "v0.5.0")

    def test_missing_package_is_rejected(self):
        self.asset.unlink()
        with self.assertRaisesRegex(ValueError, "不存在"):
            prepare_files(self.root, "v0.5.0")

    def test_remote_and_traversing_paths_are_rejected(self):
        for url in ["https://other.example/app.zip", "../app.zip", "%2e%2e%2fapp.zip", "app.zip?token=123"]:
            with self.subTest(url=url):
                self.write_manifest(MANIFESTS[0], files=[{"url": url, "sha512": self.digest}])
                with self.assertRaisesRegex(ValueError, "非同目录"):
                    prepare_files(self.root, "v0.5.0")

    @patch.dict("os.environ", {"GITHUB_REPOSITORY": "owner/repo"})
    @patch("sync_cos.subprocess.check_output")
    def test_only_latest_release_can_be_synced(self, output):
        output.return_value = "v0.5.0\n"
        check_latest("v0.5.0")
        with self.assertRaisesRegex(ValueError, "不是 GitHub 最新正式版"):
            check_latest("v0.4.0")


    def test_upload_progress_is_reported_at_ten_percent_intervals(self):
        output = io.StringIO()
        report = upload_progress("installer.zip")
        with redirect_stdout(output):
            for consumed in [1, 5, 10, 11, 20, 99, 100]:
                report(consumed, 100)
        self.assertEqual(output.getvalue().splitlines(), [
            "上传进度：installer.zip 0%", "上传进度：installer.zip 10%",
            "上传进度：installer.zip 20%", "上传进度：installer.zip 90%",
            "上传进度：installer.zip 100%",
        ])


    @patch.dict("os.environ", {"COS_SECRET_ID": "test-id", "COS_SECRET_KEY": "test-key"})
    def test_upload_uses_official_public_domain(self):
        client = create_cos_client()
        self.assertEqual(
            client._conf.uri(bucket=BUCKET, path="releases/test"),
            "https://portunus-1321735248.cos.ap-shanghai.tencentcos.cn/releases/test",
        )


    @patch.dict("os.environ", {}, clear=True)
    @patch("sync_cos.sys.stdin.isatty", return_value=False)
    def test_missing_local_credentials_gives_actionable_error(self, _):
        with self.assertRaisesRegex(ValueError, "本地终端"):
            credentials()

    @patch.dict("os.environ", {"TENCENTCLOUD_SECRET_ID": "local-id", "TENCENTCLOUD_SECRET_KEY": "local-key"}, clear=True)
    def test_tencent_cloud_environment_credentials_are_supported(self):
        self.assertEqual(credentials(), ("local-id", "local-key"))

    @patch("sync_cos.subprocess.check_output")
    def test_prerelease_is_rejected_before_upload(self, output):
        output.return_value = '{"tagName":"v0.6.0-beta.1","isDraft":false,"isPrerelease":true}'
        with self.assertRaisesRegex(ValueError, "正式 Release"):
            release_info("v0.6.0-beta.1")

    @patch("sync_cos.subprocess.check_output")
    def test_local_latest_version_is_resolved_and_verified(self, output):
        output.side_effect = [
            '{"tagName":"v0.5.0","isDraft":false,"isPrerelease":false}',
            'v0.5.0\n',
        ]
        self.assertEqual(release_info(None), "v0.5.0")

    @patch("sync_cos.subprocess.run")
    def test_local_download_refreshes_manifests_and_reuses_packages(self, run):
        download_release("v0.5.0", self.root)
        self.assertEqual(run.call_count, 2)
        self.assertIn("--clobber", run.call_args_list[0].args[0])
        self.assertIn("--skip-existing", run.call_args_list[1].args[0])
        self.assertTrue(all(call.kwargs["check"] for call in run.call_args_list))
